package telegram

import (
	"context"
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

// Capabilities 返回 Telegram 私聊的能力：支持文字、图片、视频、音频和文件；单聊天窗口；不支持已读回执。
func (Connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		MessageTypes:          []string{"text", "image", "video", "audio", "file"},
		ReadReceipts:          false,
		MultipleConversations: false,
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

// Send 把时间线中的回复 m 发给 Telegram 用户（m.ExternalTo 为 chat_id），返回 Telegram 生成的消息编号并报告已送达。
func (c Connector) Send(ctx context.Context, m channel.Outgoing) (string, bool, error) {
	if m.ExternalTo == "" {
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
	if len(m.Attachments) == 0 {
		msgID, err := c.client.SendMessage(ctx, botToken, m.ExternalTo, m.Text)
		if err != nil {
			return "", false, err
		}
		return strconv.FormatInt(msgID, 10), true, nil
	}
	if c.files == nil {
		return "", false, errors.New("telegram: filesystem service is required to send attachments")
	}
	// 文字超过 Telegram caption 上限时先单独发送文字消息，再逐个发送附件
	caption := m.Text
	var lastMsgID int64
	if caption != "" && utf8.RuneCountInString(caption) > maxCaptionRunes {
		lastMsgID, err = c.client.SendMessage(ctx, botToken, m.ExternalTo, caption)
		if err != nil {
			return "", false, err
		}
		caption = ""
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
		if i == 0 {
			attCaption = caption
		}
		durSec := att.DurationMs / 1000
		switch att.Kind {
		case "image":
			lastMsgID, err = c.client.SendPhoto(ctx, botToken, m.ExternalTo, att.Filename, rc, attCaption)
		case "video":
			lastMsgID, err = c.client.SendVideo(ctx, botToken, m.ExternalTo, att.Filename, rc, attCaption, durSec, att.Width, att.Height)
		case "audio":
			if att.IsVoice && strings.HasPrefix(strings.ToLower(att.ContentType), "audio/ogg") {
				lastMsgID, err = c.client.SendVoice(ctx, botToken, m.ExternalTo, att.Filename, rc, attCaption, durSec)
			} else {
				lastMsgID, err = c.client.SendAudio(ctx, botToken, m.ExternalTo, att.Filename, rc, attCaption, durSec)
			}
		default:
			lastMsgID, err = c.client.SendDocument(ctx, botToken, m.ExternalTo, att.Filename, rc, attCaption)
		}
		_ = rc.Close()
		if err != nil {
			return "", false, err
		}
	}
	return strconv.FormatInt(lastMsgID, 10), true, nil
}
