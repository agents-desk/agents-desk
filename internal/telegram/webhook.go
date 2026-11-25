package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
)

// Webhook 处理 POST /webhooks/telegram/{channel}：接收 Telegram 官方直连或外部业务网关转发的 Update，
// 校验 X-Telegram-Bot-Api-Secret-Token，按需通过外部身份头关联客户，并将文字与媒体元数据毫秒级写入会话内核
// （不阻塞于外部文件下载，媒体由后台队列自动拉取封面/图片/语音，视频与大文件保持远端态等待按需拉取）。
type Webhook struct {
	db      *bun.DB
	crypt   *crypt.Crypt
	receive *conversation.Receive
	down    conversation.Downstream
}

// NewWebhook 返回 Telegram Webhook 处理器。
func NewWebhook(db *bun.DB, c *crypt.Crypt, receive *conversation.Receive, down conversation.Downstream) *Webhook {
	return &Webhook{db: db, crypt: c, receive: receive, down: down}
}

type updatePayload struct {
	UpdateID int64        `json:"update_id"`
	Message  *incomingMsg `json:"message"`
}

type incomingMsg struct {
	MessageID int64          `json:"message_id"`
	From      *telegramUser  `json:"from"`
	Chat      telegramChat   `json:"chat"`
	Text      string         `json:"text"`
	Caption   string         `json:"caption"`
	Photo     []photoSize    `json:"photo"`
	Video     *videoFile     `json:"video"`
	VideoNote *videoNoteFile `json:"video_note"`
	Animation *videoFile     `json:"animation"`
	Voice     *voiceFile     `json:"voice"`
	Audio     *audioFile     `json:"audio"`
	Sticker   *stickerFile   `json:"sticker"`
	Document  *docFile       `json:"document"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type photoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

type videoFile struct {
	FileID    string     `json:"file_id"`
	Width     int        `json:"width"`
	Height    int        `json:"height"`
	Duration  int        `json:"duration"`
	Thumbnail *photoSize `json:"thumbnail"`
	FileName  string     `json:"file_name"`
	MimeType  string     `json:"mime_type"`
	FileSize  int64      `json:"file_size"`
}

type videoNoteFile struct {
	FileID    string     `json:"file_id"`
	Length    int        `json:"length"`
	Duration  int        `json:"duration"`
	Thumbnail *photoSize `json:"thumbnail"`
	FileSize  int64      `json:"file_size"`
}

type voiceFile struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type audioFile struct {
	FileID    string     `json:"file_id"`
	Duration  int        `json:"duration"`
	Performer string     `json:"performer"`
	Title     string     `json:"title"`
	FileName  string     `json:"file_name"`
	MimeType  string     `json:"mime_type"`
	FileSize  int64      `json:"file_size"`
	Thumbnail *photoSize `json:"thumbnail"`
}

type stickerFile struct {
	FileID     string     `json:"file_id"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	IsAnimated bool       `json:"is_animated"`
	IsVideo    bool       `json:"is_video"`
	Thumbnail  *photoSize `json:"thumbnail"`
	Emoji      string     `json:"emoji"`
	FileSize   int64      `json:"file_size"`
}

type docFile struct {
	FileID    string     `json:"file_id"`
	FileName  string     `json:"file_name"`
	MimeType  string     `json:"mime_type"`
	FileSize  int64      `json:"file_size"`
	Thumbnail *photoSize `json:"thumbnail"`
}

// ServeHTTP 实现 http.Handler，供 chi 挂载到 POST /webhooks/telegram/{channel}。
func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "channel")
	if _, err := uuid.Parse(id); err != nil {
		http.NotFound(w, r)
		return
	}
	ch, err := findPublic(ctx, h.db, id)
	if errors.Is(err, channel.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "load telegram channel for webhook", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s, err := settingsOf(ch)
	if err != nil {
		slog.ErrorContext(ctx, "decode telegram settings", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	secret, err := h.crypt.Decrypt(s.WebhookSecret)
	if err != nil {
		slog.ErrorContext(ctx, "decrypt telegram webhook secret", "channel_id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	provided := r.Header.Get(SecretHeader)
	if provided == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(provided)) != 1 {
		slog.WarnContext(ctx, "telegram webhook secret mismatch", "channel_id", id)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var upd updatePayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&upd); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if upd.Message == nil || upd.Message.Chat.Type != "private" || upd.Message.Chat.ID == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	msg := upd.Message
	if msg.From != nil && msg.From.IsBot {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	fromName := displayNameOf(msg.From, chatID)
	extUserID := strings.TrimSpace(r.Header.Get(ExternalIDHeader))
	extEmail := strings.TrimSpace(r.Header.Get(ExternalEmailHeader))
	extName := strings.TrimSpace(r.Header.Get(ExternalNameHeader))
	if len(extUserID) > 200 {
		extUserID = extUserID[:200]
	}

	// 可信外部网关附带了产品用户编号时，先关联或合并联系人，打通网页端与 Telegram 同一客户档案。
	if extUserID != "" {
		profileName := firstNonEmpty(extName, fromName)
		err = database.RunInTx(ctx, h.db, nil, func(ctx context.Context, tx database.Tx) error {
			_, convIDs, err := contact.LinkUser(ctx, tx, ch.TenantID, ch.ID, chatID, extUserID, contact.Profile{
				Name:  profileName,
				Email: extEmail,
			})
			if err != nil {
				return err
			}
			return conversation.NotifyUpdated(ctx, tx, h.down, ch.TenantID, convIDs)
		})
		if err != nil {
			slog.ErrorContext(ctx, "link telegram contact to external user", "channel_id", id, "external_user_id", extUserID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	sys := identity.Subject{Type: identity.System, ID: "telegram:" + ch.ID, Tenant: ch.TenantID}
	text := strings.TrimSpace(firstNonEmpty(msg.Text, msg.Caption))
	incoming, fallbackText := extractIncomingAttachments(msg)
	if text == "" && len(incoming) == 0 {
		text = fallbackText
	}
	if text == "" && len(incoming) == 0 {
		slog.InfoContext(ctx, "ignore unsupported empty telegram message", "channel_id", id, "message_id", msg.MessageID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	extMsgID := fmt.Sprintf("%d:%d", msg.Chat.ID, msg.MessageID)
	_, err = h.receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:           ch.ID,
		From:                chatID,
		FromName:            firstNonEmpty(extName, fromName),
		ExternalID:          extMsgID,
		Text:                text,
		IncomingAttachments: incoming,
	})
	if err != nil {
		slog.ErrorContext(ctx, "receive telegram message failed", "channel_id", id, "message_id", msg.MessageID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// extractIncomingAttachments 将 Telegram 消息体中的各类媒体转换为会话层的 IncomingAttachment 元数据。
// 图片、语音与小文件设置 AutoFetch=true 由后台异步落盘；视频和大文件设置 AutoFetch=false 仅异步拉取封面，
// 主文件等待成员点击按需拉取；超过 Telegram 20 MiB 下载上限的直接标明 channel.media_too_large。
func extractIncomingAttachments(msg *incomingMsg) ([]conversation.IncomingAttachment, string) {
	thumbID := func(p *photoSize) string {
		if p == nil {
			return ""
		}
		return p.FileID
	}
	tooLarge := func(size int64) string {
		if size > int64(MaxDownloadSize) {
			return channel.ErrMediaTooLarge.Code()
		}
		return ""
	}

	switch {
	case len(msg.Photo) > 0:
		best := msg.Photo[len(msg.Photo)-1]
		return []conversation.IncomingAttachment{{
			Kind:        conversation.AttachmentImage,
			Size:        best.FileSize,
			Width:       best.Width,
			Height:      best.Height,
			SourceRef:   best.FileID,
			AutoFetch:   best.FileSize <= int64(conversation.MaxImageSize),
			Unavailable: tooLarge(best.FileSize),
		}}, ""

	case msg.Video != nil:
		v := msg.Video
		mime := firstNonEmpty(v.MimeType, "video/mp4")
		return []conversation.IncomingAttachment{{
			Kind:           conversation.AttachmentVideo,
			Filename:       firstNonEmpty(v.FileName, "video.mp4"),
			ContentType:    mime,
			Size:           v.FileSize,
			Width:          v.Width,
			Height:         v.Height,
			DurationMs:     v.Duration * 1000,
			SourceRef:      v.FileID,
			ThumbSourceRef: thumbID(v.Thumbnail),
			AutoFetch:      false,
			Unavailable:    tooLarge(v.FileSize),
		}}, ""

	case msg.VideoNote != nil:
		vn := msg.VideoNote
		return []conversation.IncomingAttachment{{
			Kind:           conversation.AttachmentVideo,
			Filename:       "video_note.mp4",
			ContentType:    "video/mp4",
			Size:           vn.FileSize,
			Width:          vn.Length,
			Height:         vn.Length,
			DurationMs:     vn.Duration * 1000,
			SourceRef:      vn.FileID,
			ThumbSourceRef: thumbID(vn.Thumbnail),
			AutoFetch:      false,
			Unavailable:    tooLarge(vn.FileSize),
		}}, ""

	case msg.Animation != nil:
		a := msg.Animation
		mime := firstNonEmpty(a.MimeType, "video/mp4")
		kind := conversation.AttachmentVideo
		if strings.HasPrefix(strings.ToLower(mime), "image/") {
			kind = conversation.AttachmentImage
		}
		return []conversation.IncomingAttachment{{
			Kind:           kind,
			Filename:       firstNonEmpty(a.FileName, "animation.mp4"),
			ContentType:    mime,
			Size:           a.FileSize,
			Width:          a.Width,
			Height:         a.Height,
			DurationMs:     a.Duration * 1000,
			SourceRef:      a.FileID,
			ThumbSourceRef: thumbID(a.Thumbnail),
			AutoFetch:      a.FileSize <= int64(autoFetchAnimationThreshold),
			Unavailable:    tooLarge(a.FileSize),
		}}, ""

	case msg.Voice != nil:
		v := msg.Voice
		return []conversation.IncomingAttachment{{
			Kind:        conversation.AttachmentAudio,
			Filename:    "voice.ogg",
			ContentType: firstNonEmpty(v.MimeType, "audio/ogg"),
			Size:        v.FileSize,
			DurationMs:  v.Duration * 1000,
			IsVoice:     true,
			SourceRef:   v.FileID,
			AutoFetch:   v.FileSize <= int64(conversation.MaxAudioSize),
			Unavailable: tooLarge(v.FileSize),
		}}, ""

	case msg.Audio != nil:
		a := msg.Audio
		name := a.FileName
		if name == "" && a.Title != "" {
			name = a.Title + ".mp3"
		}
		return []conversation.IncomingAttachment{{
			Kind:           conversation.AttachmentAudio,
			Filename:       firstNonEmpty(name, "audio.mp3"),
			ContentType:    firstNonEmpty(a.MimeType, "audio/mpeg"),
			Size:           a.FileSize,
			DurationMs:     a.Duration * 1000,
			SourceRef:      a.FileID,
			ThumbSourceRef: thumbID(a.Thumbnail),
			AutoFetch:      a.FileSize <= int64(autoFetchFileThreshold),
			Unavailable:    tooLarge(a.FileSize),
		}}, ""

	case msg.Sticker != nil:
		st := msg.Sticker
		if st.IsVideo {
			return []conversation.IncomingAttachment{{
				Kind:           conversation.AttachmentVideo,
				Filename:       "sticker.webm",
				ContentType:    "video/webm",
				Size:           st.FileSize,
				Width:          st.Width,
				Height:         st.Height,
				SourceRef:      st.FileID,
				ThumbSourceRef: thumbID(st.Thumbnail),
				AutoFetch:      st.FileSize <= int64(autoFetchAnimationThreshold),
				Unavailable:    tooLarge(st.FileSize),
			}}, ""
		}
		// TGS 矢量贴纸在浏览器无法原生展示，优先拉取静态缩略图；无缩略图时回退其 emoji
		if st.IsAnimated {
			if st.Thumbnail != nil && st.Thumbnail.FileID != "" {
				return []conversation.IncomingAttachment{{
					Kind:        conversation.AttachmentImage,
					Filename:    "sticker.webp",
					ContentType: "image/webp",
					Size:        st.Thumbnail.FileSize,
					Width:       st.Thumbnail.Width,
					Height:      st.Thumbnail.Height,
					SourceRef:   st.Thumbnail.FileID,
					AutoFetch:   true,
				}}, ""
			}
			return nil, strings.TrimSpace(st.Emoji)
		}
		return []conversation.IncomingAttachment{{
			Kind:        conversation.AttachmentImage,
			Filename:    "sticker.webp",
			ContentType: "image/webp",
			Size:        st.FileSize,
			Width:       st.Width,
			Height:      st.Height,
			SourceRef:   st.FileID,
			AutoFetch:   st.FileSize <= int64(conversation.MaxImageSize),
			Unavailable: tooLarge(st.FileSize),
		}}, ""

	case msg.Document != nil:
		d := msg.Document
		kind := kindForDocumentMIME(d.MimeType)
		auto := kind != conversation.AttachmentVideo && d.FileSize <= int64(autoFetchFileThreshold)
		return []conversation.IncomingAttachment{{
			Kind:           kind,
			Filename:       firstNonEmpty(d.FileName, "file"),
			ContentType:    d.MimeType,
			Size:           d.FileSize,
			SourceRef:      d.FileID,
			ThumbSourceRef: thumbID(d.Thumbnail),
			AutoFetch:      auto,
			Unavailable:    tooLarge(d.FileSize),
		}}, ""
	}
	return nil, ""
}

func kindForDocumentMIME(ct string) conversation.AttachmentKind {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch {
	case strings.HasPrefix(ct, "image/"):
		return conversation.AttachmentImage
	case strings.HasPrefix(ct, "video/"):
		return conversation.AttachmentVideo
	case strings.HasPrefix(ct, "audio/"):
		return conversation.AttachmentAudio
	default:
		return conversation.AttachmentFile
	}
}

func displayNameOf(u *telegramUser, chatID string) string {
	if u == nil {
		return "Telegram " + chatID
	}
	full := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if full != "" {
		return full
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return "Telegram " + chatID
}
