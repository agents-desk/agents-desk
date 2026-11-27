package widget

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/conversation"
)

// MessageBody 是客户在聊天窗口中发消息的请求体：文字和附件不能同时为空。
type MessageBody struct {
	Text        string                         `json:"text,omitempty" maxLength:"10000"`
	Attachments []conversation.AttachmentInput `json:"attachments,omitempty" maxItems:"10" doc:"通过 /files 上传的媒体附件列表"`
	ClientID    string                         `json:"client_id,omitempty" pattern:"^[A-Za-z0-9_-]{1,64}$" doc:"聊天窗口为这条消息生成的编号；网络出错重发时带同一个编号，消息只记录一次"`
	TicketID    string                         `json:"ticket_id,omitempty" format:"uuid" doc:"可选：客户从工单详情发起补充信息时附带的工单 ID，用于关联当前活跃会话并唤醒等待工单"`
}

// Sent 是客户发出的消息：所在的会话和时间线中的这一项。
type Sent struct {
	ConversationID string                      `json:"conversation_id"`
	Item           conversation.ContactItemDTO `json:"item"`
}

// SentOutput 是发消息的响应。
type SentOutput struct {
	Body Sent
}

// SendMessage 是客户在聊天窗口中发消息：继续一个会话，或者发起新会话。
type SendMessage struct {
	db      *bun.DB
	receive *conversation.Receive
	signer  *crypt.Crypt
}

// NewSendMessage 返回客户发消息的动作；receive 是写入渠道收到的消息的动作，signer 用于为私有附件生成短期签名地址。
func NewSendMessage(db *bun.DB, receive *conversation.Receive, signer ...*crypt.Crypt) *SendMessage {
	var c *crypt.Crypt
	if len(signer) > 0 {
		c = signer[0]
	}
	return &SendMessage{db: db, receive: receive, signer: c}
}

// Handle 以联系人 sub 的身份在本次请求的聊天插件中发消息 b：归入该联系人的当前活跃会话，或按重开窗口重开/新建会话。
// 同一个 b.ClientID 重发时返回之前记录的那一条。聊天插件不存在或已停用时返回 channel.ErrNotFound。
func (a *SendMessage) Handle(ctx context.Context, sub identity.Subject, b MessageBody) (*Sent, error) {
	ch, err := channelFrom(ctx)
	if err != nil {
		return nil, err
	}
	var from string
	err = a.db.NewSelect().Table("contact_identities").Column("external_id").
		Where("tenant_id = ? AND channel_id = ? AND contact_id = ?", sub.Tenant, ch.ID, sub.ID).Limit(1).Scan(ctx, &from)
	if err != nil {
		return nil, errors.Wrap(err, "load contact identity")
	}
	p := conversation.ReceiveParams{
		ChannelID:   ch.ID,
		From:        from,
		Text:        b.Text,
		Attachments: b.Attachments,
		TicketID:    b.TicketID,
	}
	if b.ClientID != "" {
		p.ExternalID = sub.ID + ":" + b.ClientID // 编号由客户生成，加上联系人的前缀，不会与别人的重复
	}
	res, err := a.receive.Handle(ctx, sub, p)
	if err != nil {
		return nil, err
	}
	item := res.Item.ForContact()
	item.ConversationID = res.ConversationID
	item.Attachments = conversation.SignContactAttachments(item.Attachments, ch.ID, a.signer)
	return &Sent{ConversationID: res.ConversationID, Item: item}, nil
}
