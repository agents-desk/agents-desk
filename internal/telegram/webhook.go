package telegram

import (
	"bytes"
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
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
)

// Webhook 处理 POST /webhooks/telegram/{channel}：接收 Telegram 官方直连或外部业务网关转发的 Update，
// 校验 X-Telegram-Bot-Api-Secret-Token，按需通过外部身份头关联客户，下载图片/文件附件，再写入会话内核。
type Webhook struct {
	db      *bun.DB
	crypt   *crypt.Crypt
	files   *filesystem.Service
	client  *Client
	receive *conversation.Receive
	down    conversation.Downstream
}

// NewWebhook 返回 Telegram Webhook 处理器。
func NewWebhook(db *bun.DB, c *crypt.Crypt, files *filesystem.Service, client *Client, receive *conversation.Receive, down conversation.Downstream) *Webhook {
	if client == nil {
		client = NewClient(nil)
	}
	return &Webhook{db: db, crypt: c, files: files, client: client, receive: receive, down: down}
}

type updatePayload struct {
	UpdateID int64        `json:"update_id"`
	Message  *incomingMsg `json:"message"`
}

type incomingMsg struct {
	MessageID int64         `json:"message_id"`
	From      *telegramUser `json:"from"`
	Chat      telegramChat  `json:"chat"`
	Text      string        `json:"text"`
	Caption   string        `json:"caption"`
	Photo     []photoSize   `json:"photo"`
	Document  *docFile      `json:"document"`
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

type docFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
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
	var attachmentIDs []string

	if len(msg.Photo) > 0 || msg.Document != nil {
		botToken, err := h.crypt.Decrypt(s.BotToken)
		if err != nil {
			slog.ErrorContext(ctx, "decrypt telegram bot token for media", "channel_id", id, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		fileID, fallbackName, mimeHint := pickMedia(msg)
		uploadedID, fallbackNote, err := h.ingestMedia(ctx, sys, botToken, fileID, fallbackName, mimeHint)
		if err != nil {
			slog.WarnContext(ctx, "ingest telegram media failed", "channel_id", id, "message_id", msg.MessageID, "error", err)
			if text == "" {
				text = fallbackNote
			}
		} else if uploadedID != "" {
			attachmentIDs = []string{uploadedID}
		}
	}

	if text == "" && len(attachmentIDs) == 0 {
		slog.InfoContext(ctx, "ignore unsupported empty telegram message", "channel_id", id, "message_id", msg.MessageID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	extMsgID := fmt.Sprintf("%d:%d", msg.Chat.ID, msg.MessageID)
	_, err = h.receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:         ch.ID,
		From:              chatID,
		FromName:          firstNonEmpty(extName, fromName),
		ExternalID:        extMsgID,
		Text:              text,
		AttachmentFileIDs: attachmentIDs,
	})
	if err != nil && len(attachmentIDs) > 0 && errors.Is(err, filesystem.ErrNotAccepted) {
		// 附件不在会话白名单（例如不支持的格式或超限图片）时，回退记录可读说明，避免 Webhook 500 无限重试。
		slog.WarnContext(ctx, "telegram attachment rejected by whitelist, recording text fallback", "channel_id", id, "message_id", msg.MessageID)
		fallback := text
		if fallback == "" {
			_, name, _ := pickMedia(msg)
			fallback = "[Attachment: " + fallbackFilename(name, "file") + "]"
		}
		_, err = h.receive.Handle(ctx, sys, conversation.ReceiveParams{
			ChannelID:  ch.ID,
			From:       chatID,
			FromName:   firstNonEmpty(extName, fromName),
			ExternalID: extMsgID,
			Text:       fallback,
		})
	}
	if err != nil {
		slog.ErrorContext(ctx, "receive telegram message failed", "channel_id", id, "message_id", msg.MessageID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// ingestMedia 从 Telegram 下载媒体并上传为临时文件，供 conversation.Receive 在事务中校验与确认。
func (h *Webhook) ingestMedia(ctx context.Context, owner identity.Subject, botToken, fileID, nameHint, mimeHint string) (string, string, error) {
	if h.files == nil {
		return "", "[Attachment: " + fallbackFilename(nameHint, "file") + "]", errors.New("filesystem service not configured")
	}
	data, inferredName, detectedType, err := h.client.DownloadFile(ctx, botToken, fileID)
	if err != nil {
		return "", "[Attachment: " + fallbackFilename(nameHint, "file") + "]", err
	}
	contentType := firstNonEmpty(mimeHint, detectedType)
	filename := firstNonEmpty(nameHint, inferredName)
	if filename == "" || !strings.Contains(filename, ".") {
		filename = "file" + extForContentType(contentType)
	}
	f, err := h.files.Upload(ctx, owner, filesystem.FileInput{
		Filename:    filename,
		ContentType: contentType,
		Size:        int64(len(data)),
	}, bytes.NewReader(data))
	if err != nil {
		return "", "[Attachment: " + fallbackFilename(filename, "file") + "]", err
	}
	return f.ID.String(), "", nil
}

func extForContentType(ct string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0])) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "application/pdf":
		return ".pdf"
	default:
		return ""
	}
}

func pickMedia(msg *incomingMsg) (fileID, name, mime string) {
	if len(msg.Photo) > 0 {
		best := msg.Photo[len(msg.Photo)-1]
		return best.FileID, "", ""
	}
	if msg.Document != nil {
		return msg.Document.FileID, msg.Document.FileName, msg.Document.MimeType
	}
	return "", "", ""
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
