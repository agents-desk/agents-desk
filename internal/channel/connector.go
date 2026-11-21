package channel

import (
	"context"

	"github.com/cockroachdb/errors"
)

// Capabilities 是连接器的能力声明。
type Capabilities struct {
	MessageTypes []string // 支持的消息类型，例如 text、image、file
	ReadReceipts bool     // 能不能报告对方已读
	GroupChat    bool     // 是不是群聊类渠道
	// MultipleConversations 表示对方可以同时有多个会话，自己选择继续哪一个（例如网页聊天插件）；
	// 为 false 时渠道只有一个聊天窗口，消息归入对方最近的会话。
	MultipleConversations bool
}

// Outgoing 是要发到外部渠道的一条消息。
type Outgoing struct {
	Channel        *Channel
	ConversationID string // 消息所在的会话
	ItemID         string // 时间线中这条消息的 ID，连接器可以用它去重
	Seq            int64  // 这条消息在时间线中的序号
	ContactID      string // 对方：联系人的 ID
	ExternalTo     string // 对方在渠道中的外部编号
	Text           string // 发给对方的文字
}

// Connector 是一种渠道的连接器：把时间线中的消息发到外部渠道。收到的消息和回执由连接器调用会话的业务动作写入。
type Connector interface {
	// Capabilities 返回连接器的能力声明。
	Capabilities() Capabilities
	// Send 把消息 m 发到外部渠道，返回消息在渠道中的外部编号；delivered 为 true 表示发出即已送达。
	// 同一条消息可能被调用多次，连接器按 m.ItemID 去重。
	Send(ctx context.Context, m Outgoing) (externalID string, delivered bool, err error)
}

// Registry 按渠道的种类找到连接器，在入口中装配。
type Registry map[Kind]Connector

// Connector 返回种类 kind 的连接器；没有登记时返回错误。
func (r Registry) Connector(kind Kind) (Connector, error) {
	c, ok := r[kind]
	if !ok {
		return nil, errors.Newf("channel: no connector for kind %q", kind)
	}
	return c, nil
}

// TestConnector 是测试渠道的连接器：消息不发往任何地方，直接记为已送达。开发、演示和测试用。
type TestConnector struct{}

// Capabilities 返回测试渠道的能力：只有文字，没有已读回执。
func (TestConnector) Capabilities() Capabilities {
	return Capabilities{MessageTypes: []string{"text"}}
}

// Send 不发送，用时间线中的消息 ID 作为外部编号，并报告已送达。
func (TestConnector) Send(_ context.Context, m Outgoing) (string, bool, error) {
	return "test-" + m.ItemID, true, nil
}

// DefaultRegistry 返回不依赖其他资源的内置连接器：测试渠道。需要资源的连接器（网页聊天插件）由入口加入。
func DefaultRegistry() Registry { return Registry{Test: TestConnector{}} }
