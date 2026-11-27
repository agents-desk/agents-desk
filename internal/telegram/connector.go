package telegram

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/filesystem"

	"agents-desk/internal/channel"
)

// maxCaptionRunes 是 Telegram 媒体消息 caption 的最大字符数。
const maxCaptionRunes = 1024

// Connector 是 Telegram 机器人私聊渠道的连接器：把坐席或系统的回复经 Telegram Bot API 发给对方，
// 并实现 channel.MediaFetcher 供会话层异步或按需流式拉取 Telegram 媒体。
type Connector struct {
	crypt  *crypt.Crypt
	files  *filesystem.Service
	client *Client
}

var _ channel.MediaFetcher = Connector{}

// NewConnector 返回 Telegram 渠道的连接器；crypt 解密 Bot Token，files 读取消息附带的媒体流，client 调用 Bot API。
func NewConnector(c *crypt.Crypt, files *filesystem.Service, client *Client) Connector {
	if client == nil {
		client = NewClient(nil)
	}
	return Connector{crypt: c, files: files, client: client}
}

// Capabilities 返回 Telegram 机器人的能力：Webhook 传输，支持 1:1 私聊、群内 @ 机器人、引用回复和 @ 发言人，支持文字与多媒体附件。
func (Connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		Transport:       channel.TransportWebhook,
		MessageTypes:    []string{"text", "image", "video", "audio", "file"},
		SupportsDirect:  true,
		SupportsGroup:   true,
		SupportsMention: true,
		SupportsReplyTo: true,
		ReadReceipts:    false,
	}
}

// FetchMedia 实现 channel.MediaFetcher：按 Telegram file_id 解密渠道 Bot Token 后流式拉取媒体正文。
func (c Connector) FetchMedia(ctx context.Context, ch *channel.Channel, ref string, maxBytes int64) (*channel.FetchedMedia, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, channel.ErrMediaUnavailable.New()
	}
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	botToken, err := c.crypt.Decrypt(s.BotToken)
	if err != nil {
		return nil, errors.Wrap(err, "decrypt telegram bot token")
	}
	return c.client.OpenFile(ctx, botToken, ref, maxBytes)
}

// Send 把时间线中的回复 m 发给 Telegram 用户或群聊（m.ExternalTo 为 chat_id），支持引用回复与群内 @ 提及，
// 返回 <chat_id>:<message_id> 格式的外部编号并报告已送达。
func (c Connector) Send(ctx context.Context, m channel.Outgoing) (string, bool, error) {
	chatID := strings.TrimSpace(m.Target.ExternalTo)
	if chatID == "" {
		chatID = strings.TrimSpace(m.ExternalTo)
	}
	if chatID == "" {
		return "", false, errors.New("telegram: empty recipient chat id")
	}
	s, err := settingsOf(m.Channel)
	if err != nil {
		return "", false, err
	}
	botToken, err := c.crypt.Decrypt(s.BotToken)
	if err != nil {
		return "", false, errors.Wrap(err, "decrypt telegram bot token")
	}
	outText, parseMode := formatTelegramText(m.Text, m.Mentions)
	sendOpt := SendOptions{
		ReplyToMessageID: parseTelegramReplyID(m.ReplyToExternalID),
		ParseMode:        parseMode,
	}
	if len(m.Attachments) == 0 {
		msgID, err := c.client.SendMessage(ctx, botToken, chatID, outText, sendOpt)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("%s:%d", chatID, msgID), true, nil
	}
	if c.files == nil {
		return "", false, errors.New("telegram: filesystem service is required to send attachments")
	}
	// 文字超过 Telegram caption 上限时先单独发送文字消息，再逐个发送附件
	caption := outText
	captionParseMode := parseMode
	var lastMsgID int64
	if caption != "" && utf8.RuneCountInString(m.Text) > maxCaptionRunes {
		lastMsgID, err = c.client.SendMessage(ctx, botToken, chatID, caption, sendOpt)
		if err != nil {
			return "", false, err
		}
		caption = ""
		captionParseMode = ""
		sendOpt.ReplyToMessageID = 0
	}
	for i, att := range m.Attachments {
		fileUUID, err := uuid.Parse(att.FileID)
		if err != nil {
			return "", false, errors.Wrap(err, "parse attachment file id")
		}
		rc, _, err := c.files.Open(ctx, fileUUID)
		if err != nil {
			return "", false, errors.Wrap(err, "open attachment file")
		}
		attCaption := ""
		attOpt := SendOptions{}
		if i == 0 {
			attCaption = caption
			attOpt = SendOptions{ReplyToMessageID: sendOpt.ReplyToMessageID, ParseMode: captionParseMode}
		}
		durSec := att.DurationMs / 1000
		switch att.Kind {
		case "image":
			lastMsgID, err = c.client.SendPhoto(ctx, botToken, chatID, att.Filename, rc, attCaption, attOpt)
		case "video":
			lastMsgID, err = c.client.SendVideo(ctx, botToken, chatID, att.Filename, rc, attCaption, durSec, att.Width, att.Height, attOpt)
		case "audio":
			if att.IsVoice && strings.HasPrefix(strings.ToLower(att.ContentType), "audio/ogg") {
				lastMsgID, err = c.client.SendVoice(ctx, botToken, chatID, att.Filename, rc, attCaption, durSec, attOpt)
			} else {
				lastMsgID, err = c.client.SendAudio(ctx, botToken, chatID, att.Filename, rc, attCaption, durSec, attOpt)
			}
		default:
			lastMsgID, err = c.client.SendDocument(ctx, botToken, chatID, att.Filename, rc, attCaption, attOpt)
		}
		_ = rc.Close()
		if err != nil {
			return "", false, err
		}
	}
	return fmt.Sprintf("%s:%d", chatID, lastMsgID), true, nil
}

// parseTelegramReplyID 从 "<chat_id>:<msg_id>" 或纯数字 "<msg_id>" 中解析 Telegram message_id。
func parseTelegramReplyID(extID string) int64 {
	extID = strings.TrimSpace(extID)
	if extID == "" {
		return 0
	}
	if _, rawID, ok := strings.Cut(extID, ":"); ok {
		extID = rawID
	}
	id, err := strconv.ParseInt(extID, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// formatTelegramText 在携带外部 @ 提及列表时生成 Telegram HTML 文本（tg://user?id=... 触发真实群提醒），否则保持纯文本。
func formatTelegramText(text string, mentions []channel.ExternalMention) (string, string) {
	if len(mentions) == 0 {
		return text, ""
	}
	var parts []string
	for _, m := range mentions {
		extID := strings.TrimSpace(m.ExternalID)
		if extID == "" {
			continue
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = extID
		}
		if _, err := strconv.ParseInt(extID, 10, 64); err == nil {
			parts = append(parts, fmt.Sprintf(`<a href="tg://user?id=%s">@%s</a>`, html.EscapeString(extID), html.EscapeString(name)))
		} else {
			parts = append(parts, "@"+html.EscapeString(name))
		}
	}
	if len(parts) == 0 {
		return text, ""
	}
	prefix := strings.Join(parts, " ")
	escaped := html.EscapeString(text)
	if escaped == "" {
		return prefix, "HTML"
	}
	return prefix + " " + escaped, "HTML"
}
