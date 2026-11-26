package channel

import (
	"context"
	"io"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrMediaTooLarge 是外部渠道的媒体超出该渠道接口允许下载的大小上限（例如 Telegram Bot API 20 MiB 限制）。
	ErrMediaTooLarge = errorx.Define(errorx.InvalidArgument, "channel.media_too_large")
	// ErrMediaUnavailable 是外部渠道上的媒体已失效、被删除或渠道不支持按需拉取。
	ErrMediaUnavailable = errorx.Define(errorx.NotFound, "channel.media_unavailable")
)

// Capabilities 是连接器的能力声明。
type Capabilities struct {
	MessageTypes []string // 支持的消息类型，例如 text、image、video、audio、file
	ReadReceipts bool     // 能不能报告对方已读
	GroupChat    bool     // 是不是群聊类渠道
}

// Attachment 是发到外部渠道的一条消息附带的媒体附件。
type Attachment struct {
	ID          string // 附件 ID
	FileID      string // 主文件 ID
	ThumbFileID string // 封面缩略图文件 ID
	Kind        string // "image"、"video"、"audio" 或 "file"
	Filename    string // 原始文件名
	ContentType string // MIME 类型
	Size        int64  // 文件大小（字节）
	Width       int    // 图片或视频宽度（像素），非图文/视频为 0
	Height      int    // 图片或视频高度（像素），非图文/视频为 0
	DurationMs  int    // 音频或视频时长（毫秒）
	IsVoice     bool   // 是否为语音条消息
	URL         string // 文件的公开地址
	ThumbURL    string // 缩略图或视频封面地址
}

// Outgoing 是要发到外部渠道的一条消息。
type Outgoing struct {
	Channel        *Channel
	ConversationID string       // 消息所在的会话
	ItemID         string       // 时间线中这条消息的 ID，连接器可以用它去重
	Seq            int64        // 这条消息在时间线中的序号
	ContactID      string       // 对方：联系人的 ID
	ExternalTo     string       // 对方在渠道中的外部编号
	Text           string       // 发给对方的文字
	Attachments    []Attachment // 附带的媒体附件
}

// FetchedMedia 是连接器按外部引用流式拉取到的媒体正文与提示信息。调用方负责关闭 Body。
type FetchedMedia struct {
	Body        io.ReadCloser // 媒体正文流
	Filename    string        // 渠道推断的文件名（可为空）
	ContentType string        // 渠道声明的 MIME 类型（可为空）
	Size        int64         // 已知字节数；未知时为 -1（filesystem.UnknownSize）
}

// MediaFetcher 是支持按外部引用异步或按需拉取媒体流的渠道连接器可选接口。
type MediaFetcher interface {
	FetchMedia(ctx context.Context, ch *Channel, ref string, maxBytes int64) (*FetchedMedia, error)
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

// Capabilities 返回测试渠道的能力：文字、图片、视频、音频和文件，没有已读回执。
func (TestConnector) Capabilities() Capabilities {
	return Capabilities{MessageTypes: []string{"text", "image", "video", "audio", "file"}}
}

// Send 不发送，用时间线中的消息 ID 作为外部编号，并报告已送达。
func (TestConnector) Send(_ context.Context, m Outgoing) (string, bool, error) {
	return "test-" + m.ItemID, true, nil
}

// DefaultRegistry 返回不依赖其他资源的内置连接器：测试渠道。需要资源的连接器（网页聊天插件、Telegram）由入口加入。
func DefaultRegistry() Registry { return Registry{Test: TestConnector{}} }
