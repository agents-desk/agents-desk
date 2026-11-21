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

// Capabilities 返回聊天插件的能力：文字消息，有已读回执，客户可以有多个会话、自己选择继续哪一个。
func (Connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text"}, ReadReceipts: true, MultipleConversations: true}
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
