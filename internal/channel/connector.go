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

// TransportMode 是渠道连接器的底层接入模式，由各连接器自行封装，核心业务层按 Capabilities 驱动而非按传输模式分支。
type TransportMode string

const (
	TransportWebhook        TransportMode = "webhook"         // 第三方平台经 HTTP Webhook 回调推送（如 Telegram Bot）
	TransportLongConnection TransportMode = "long_connection" // 主程序常驻后台长连接（如企微智能机器人 WS、Telegram 个人号 MTProto）
	TransportRealtimeHub    TransportMode = "realtime_hub"    // 浏览器经应用内实时频道收发（如网页聊天插件、商务沟通链接）
	TransportVirtual        TransportMode = "virtual"         // 进程内虚拟测试渠道
)

// Capabilities 是连接器的能力声明，用于驱动自动化、收件箱回复交互、实时推送和媒体处理。
type Capabilities struct {
	Transport         TransportMode // 底层传输方式
	MessageTypes      []string      // 支持的消息类型，例如 text、image、video、audio、file
	ReadReceipts      bool          // 是否支持报告对方已读回执
	SupportsDirect    bool          // 是否支持 1:1 单聊
	SupportsGroup     bool          // 是否支持群内 @ 机器人或外部群聊
	SupportsMention   bool          // 出站消息是否支持 @ 外部群发言人
	SupportsReplyTo   bool          // 出站消息是否支持引用回复指定消息
	SupportsRating    bool          // 是否支持会话关闭后向客户展示评价邀请
	SupportsLeadForm  bool          // 是否支持向访客线索展示留资引导
	SupportsStreaming bool          // 是否支持流式消息更新（企微智能机器人及未来 AI 流式回复）
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

// ExternalMention 描述出站群聊回复中需要 @ 的一位外部发言人。
type ExternalMention struct {
	ExternalUserID string `json:"external_user_id,omitempty" doc:"系统内部 external_users.id（可选）"`
	ExternalID     string `json:"external_id" doc:"第三方平台中的用户编号（如企微 userid、Telegram user_id）"`
	Name           string `json:"name" doc:"发言人显示名称"`
}

// DeliveryTargetKind 是出站投递目标的种类。
type DeliveryTargetKind string

const (
	TargetContact DeliveryTargetKind = "contact" // 1:1 单聊联系人
	TargetGroup   DeliveryTargetKind = "group"   // 外部群（群内 @ 机器人或商务外部群）
)

// DeliveryTarget 描述出站消息的投递目标与平台地址。
type DeliveryTarget struct {
	Kind            DeliveryTargetKind // contact 或 group
	ContactID       string             // TargetContact 时为联系人 ID
	ExternalGroupID string             // TargetGroup 时为外部群 ID（服务型外部群非空）
	ExternalTo      string             // 对方或群在渠道中的外部地址（如 userid、chat_id、peer key）
}

// Outgoing 是要发到外部渠道的一条消息。
type Outgoing struct {
	Channel           *Channel
	ConversationID    string            // 消息所在的会话
	ConversationKind  string            // 会话的种类（service、ticket 或 chat）；实时频道用它决定推送事件的类型
	ItemID            string            // 时间线中这条消息的 ID，连接器可以用它去重
	Seq               int64             // 这条消息在时间线中的序号
	Target            DeliveryTarget    // 结构化投递目标（单聊或外部群）
	ContactID         string            // 冗余兼容：Target.ContactID
	ExternalTo        string            // 冗余兼容：Target.ExternalTo
	ReplyToExternalID string            // 可选：需要引用回复的外部消息编号
	Mentions          []ExternalMention // 可选：群聊回复时要 @ 的外部发言人列表
	ReplyContext      map[string]any    // 可选：来自入站消息的短期协议回复上下文（如企微 req_id、钉钉 sessionWebhook）
	Text              string            // 发给对方的文字
	Attachments       []Attachment      // 附带的媒体附件
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
// 写入收到的消息时，外部消息编号必须在渠道内全局唯一（通常要拼上会话标识），会话按它去重，见 conversation.ReceiveParams。
type Connector interface {
	// Capabilities 返回连接器的能力声明。
	Capabilities() Capabilities
	// Send 把消息 m 发到外部渠道，返回消息在渠道中的外部编号；delivered 为 true 表示发出即已送达。
	// 同一条消息可能被调用多次，连接器按 m.ItemID 去重。
	Send(ctx context.Context, m Outgoing) (externalID string, delivered bool, err error)
}

// Runner 是需要后台常驻连接（如企微智能机器人 WebSocket、Telegram 个人号 MTProto）的渠道管理器统一生命周期接口。
// 入口在 worker 中统一拉起所有 Runner 并在停止时平滑退出，无需为每种长连接协议单独硬编码启动逻辑。
type Runner interface {
	// Name 返回管理器名称，用于日志与排障识别。
	Name() string
	// Run 阻塞运行直到 ctx 取消或遇到不可恢复的系统错误。
	Run(ctx context.Context) error
	// Wake 通知管理器立即重新对齐数据库中的已启用渠道配置。
	Wake()
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

// KindsWithTransport 返回已登记的连接器中使用指定传输模式的渠道种类列表。
func (r Registry) KindsWithTransport(mode TransportMode) []Kind {
	var out []Kind
	for kind, conn := range r {
		if conn != nil && conn.Capabilities().Transport == mode {
			out = append(out, kind)
		}
	}
	return out
}

// TestConnector 是测试渠道的连接器：消息不发往任何地方，直接记为已送达。开发、演示和测试用。
type TestConnector struct{}

// Capabilities 返回测试渠道的能力：支持单聊、群内 @、引用与提及、评价与留资，没有已读回执。
func (TestConnector) Capabilities() Capabilities {
	return Capabilities{
		Transport:        TransportVirtual,
		MessageTypes:     []string{"text", "image", "video", "audio", "file"},
		SupportsDirect:   true,
		SupportsGroup:    true,
		SupportsMention:  true,
		SupportsReplyTo:  true,
		SupportsRating:   true,
		SupportsLeadForm: true,
	}
}

// Send 不发送，用时间线中的消息 ID 作为外部编号，并报告已送达。
func (TestConnector) Send(_ context.Context, m Outgoing) (string, bool, error) {
	return "test-" + m.ItemID, true, nil
}

// DefaultRegistry 返回不依赖其他资源的内置连接器：测试渠道。需要资源的连接器（网页聊天插件、Telegram、企微等）由入口加入。
func DefaultRegistry() Registry { return Registry{Test: TestConnector{}} }
