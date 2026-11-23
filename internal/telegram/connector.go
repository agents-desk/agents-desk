package telegram

import (
	"context"
	"strconv"
	"unicode/utf8"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/filesystem"

	"agents-desk/internal/channel"
)

// maxCaptionRunes 是 Telegram 媒体消息 caption 的最大字符数。
const maxCaptionRunes = 1024

// Connector 是 Telegram 机器人私聊渠道的连接器：把坐席或系统的回复经 Telegram Bot API 发给对方。
type Connector struct {
	crypt  *crypt.Crypt
	files  *filesystem.Service
	client *Client
}

// NewConnector 返回 Telegram 渠道的连接器；crypt 解密 Bot Token，files 读取消息附带的图片和文件流，client 调用 Bot API。
func NewConnector(c *crypt.Crypt, files *filesystem.Service, client *Client) Connector {
	if client == nil {
		client = NewClient(nil)
	}
	return Connector{crypt: c, files: files, client: client}
}

// Capabilities 返回 Telegram 私聊的能力：支持文字、图片和文件；单聊天窗口（按重开窗口归入会话）；不支持已读回执。
func (Connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		MessageTypes:          []string{"text", "image", "file"},
		ReadReceipts:          false,
		MultipleConversations: false,
	}
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
		if att.Kind == "image" {
			lastMsgID, err = c.client.SendPhoto(ctx, botToken, m.ExternalTo, att.Filename, rc, attCaption)
		} else {
			lastMsgID, err = c.client.SendDocument(ctx, botToken, m.ExternalTo, att.Filename, rc, attCaption)
		}
		_ = rc.Close()
		if err != nil {
			return "", false, err
		}
	}
	return strconv.FormatInt(lastMsgID, 10), true, nil
}
