package widget

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/broadcast"

	"agents-desk/internal/channel"
	"agents-desk/internal/push"
)

// Connector 是聊天插件的连接器：成员的回复已经在时间线中，客户经接口读取；发出就是推送到客户的实时频道，
// 让打开着的聊天窗口重新读取，所以发出即送达。客户读到后经 MarkReadByContact 记为已读。
type Connector struct {
	hub *broadcast.Hub
}

// NewConnector 返回聊天插件的连接器，hub 把推送送到各实例上的实时连接。
func NewConnector(hub *broadcast.Hub) Connector { return Connector{hub: hub} }

// Capabilities 返回聊天插件的能力：基于内置实时通道推送，支持 1:1 对话、已读回执、满意度评价和线索收集表单。
func (Connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		Transport:        channel.TransportRealtimeHub,
		MessageTypes:     []string{"text", "image", "video", "audio", "file"},
		SupportsDirect:   true,
		SupportsRating:   true,
		SupportsLeadForm: true,
		ReadReceipts:     true,
	}
}

// Push 把会话的变化 ev 推送到联系人 contactID 在聊天插件 channelID 中的实时频道，作为 conversation.Downstream 的
// ContactPush：会话的状态变了，或者客户在另一个页面发了消息。其他渠道的会话没有人订阅这个频道，推送随即丢弃。
func Push(hub *broadcast.Hub) func(ctx context.Context, channelID, contactID string, ev push.Event) error {
	return func(ctx context.Context, channelID, contactID string, ev push.Event) error {
		return errors.Wrap(hub.PublishJSON(ctx, Channel(channelID, contactID), ev), "push to widget")
	}
}

// Send 把会话有新消息推送到联系人的实时频道，用时间线中的消息 ID 作为外部编号，并报告已送达。
// 推送失败时返回错误，任务稍后重试。
func (c Connector) Send(ctx context.Context, m channel.Outgoing) (string, bool, error) {
	ev := push.Event{Type: push.Conversation, ID: m.ConversationID, Seq: m.Seq}
	if err := c.hub.PublishJSON(ctx, Channel(m.Channel.ID, m.ContactID), ev); err != nil {
		return "", false, errors.Wrap(err, "push to widget")
	}
	return m.ItemID, true, nil
}
