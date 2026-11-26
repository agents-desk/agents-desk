// Package conversation 是会话：会话、参与者、只追加的时间线，以及写入时间线的业务动作。
//
// 所有写操作走同一条路径：业务动作经 authorize 检查权限，在一个事务中修改会话、追加时间线（commit），
// 并为每个下游（Downstream）发布一个任务；事务提交后，任务由 worker 交给下游处理，例如把消息发到外部渠道，
// 同时把变化推送给能看到会话的成员（push）。
package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
)

var (
	// ErrNotFound 是会话不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "conversation.not_found")
	// ErrForbidden 是调用者没有权限执行这个会话动作。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "conversation.forbidden")
	// ErrNotClosed 是会话还没有关闭，客户不能评价。
	ErrNotClosed = errorx.Define(errorx.Conflict, "conversation.not_closed")
	// ErrRatingNotRequested 是这次关闭没有向客户发出评价邀请，不能评价。
	ErrRatingNotRequested = errorx.Define(errorx.Conflict, "conversation.rating_not_requested")
	// ErrAlreadyRated 是客户已经评价过这次关闭的会话；会话重新打开、再关闭后可以再评价。
	ErrAlreadyRated = errorx.Define(errorx.Conflict, "conversation.already_rated")
	// ErrWrongKind 是当前操作不适用于这个种类的会话（例如对工单执行关闭/回复，或对服务型会话执行工单操作）。
	ErrWrongKind = errorx.Define(errorx.Conflict, "conversation.wrong_kind")
	// ErrLinkAlreadyExists 是该联系人已经关联到目标工单。
	ErrLinkAlreadyExists = errorx.Define(errorx.Conflict, "ticket.link_exists")
	// ErrLinkNotFound 是工单与该联系人之间不存在关联关系。
	ErrLinkNotFound = errorx.Define(errorx.NotFound, "ticket.link_not_found")
	// ErrPrivateTicket 是仅成员可见的工单不能向客户发布公开进度。
	ErrPrivateTicket = errorx.Define(errorx.Conflict, "ticket.private")
	// ErrChannelUnavailable 是目标回复渠道不可用、已停用，或者该联系人未绑定该渠道身份。
	ErrChannelUnavailable = errorx.Define(errorx.Conflict, "conversation.channel_unavailable")
	// ErrActiveConversationExists 是该联系人当前已有一条进行中或稍后处理的服务型会话，不能再重开其他历史会话。
	ErrActiveConversationExists = errorx.Define(errorx.Conflict, "conversation.active_exists")
	// ErrChatGroupOnly 是该操作仅适用于群聊（例如修改群名称、增删群成员、退出群聊）。
	ErrChatGroupOnly = errorx.Define(errorx.Conflict, "chat.group_only")
	// ErrChatSelfDirect 是不能与自己发起单聊。
	ErrChatSelfDirect = errorx.Define(errorx.InvalidArgument, "chat.self_direct")
	// ErrChatUnsupportedParticipant 是不支持的聊天参与者类型或尚未启用的机器人参与者。
	ErrChatUnsupportedParticipant = errorx.Define(errorx.InvalidArgument, "chat.unsupported_participant")
	// ErrChatNotParticipant 是目标参与者不在这个群聊中。
	ErrChatNotParticipant = errorx.Define(errorx.NotFound, "chat.not_participant")
	// ErrChatNameRequired 是群聊名称不能为空。
	ErrChatNameRequired = errorx.Define(errorx.InvalidArgument, "chat.name_required")
	// ErrChatOwnerProtected 是不能移出商务渠道归属成员。
	ErrChatOwnerProtected = errorx.Define(errorx.Conflict, "chat.owner_protected")
	// ErrChatContactProtected 是不能移出外部联系人参与者。
	ErrChatContactProtected = errorx.Define(errorx.Conflict, "chat.contact_protected")
)

// Kind 是会话的种类。
type Kind string

const (
	Service Kind = "service" // 服务型会话：在收件箱中处理，有团队收件箱、负责人和状态
	Ticket  Kind = "ticket"  // 工单：围绕长期或跨团队事项协作，可多对多关联服务型会话
	Chat    Kind = "chat"    // 聊天型会话：长期存在的单聊或群聊（以后的阶段）
)

// Kinds 是会话的全部种类，显示文字是翻译文件中 conversation.kind.<种类> 的消息。
var Kinds = enum.New("conversation.kind", enum.Item(Service), enum.Item(Ticket), enum.Item(Chat))

// Schema 让 OpenAPI 中的 Kind 只接受上面的取值，并带有显示文字。
func (Kind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Kinds) }

// Status 是服务型会话的状态。
type Status string

const (
	Open    Status = "open"    // 需要处理
	Snoozed Status = "snoozed" // 稍后处理：到时间或对方回复时回到打开
	Closed  Status = "closed"  // 处理完毕：对方回复时重新打开
)

// Statuses 是会话的全部状态，显示文字是翻译文件中 conversation.status.<状态> 的消息。
var Statuses = enum.New("conversation.status", enum.Item(Open), enum.Item(Snoozed), enum.Item(Closed))

// Schema 让 OpenAPI 中的 Status 只接受上面的取值，并带有显示文字。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// TicketStatus 是工单的 4 个固定状态大类，未来自定义细状态都归入其中一个大类。
type TicketStatus string

const (
	TicketSubmitted  TicketStatus = "submitted"           // 已提交
	TicketInProgress TicketStatus = "in_progress"         // 处理中
	TicketWaiting    TicketStatus = "waiting_on_customer" // 等待客户
	TicketResolved   TicketStatus = "resolved"            // 已解决
)

// TicketStatuses 是工单的全部状态，显示文字是翻译文件中 ticket.status.<状态> 的消息。
var TicketStatuses = enum.New("ticket.status", enum.Item(TicketSubmitted), enum.Item(TicketInProgress), enum.Item(TicketWaiting), enum.Item(TicketResolved))

// Schema 让 OpenAPI 中的 TicketStatus 只接受上面的取值，并带有显示文字。
func (TicketStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, TicketStatuses)
}

// Priority 是会话的优先级。
type Priority string

const (
	Normal Priority = "normal" // 普通
	High   Priority = "high"   // 高
)

// Priorities 是会话的全部优先级，显示文字是翻译文件中 conversation.priority.<优先级> 的消息。
var Priorities = enum.New("conversation.priority", enum.Item(Normal), enum.Item(High))

// Schema 让 OpenAPI 中的 Priority 只接受上面的取值，并带有显示文字。
func (Priority) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Priorities) }

// ItemKind 是时间线中一项的类型。
type ItemKind string

const (
	Message ItemKind = "message" // 消息
	Note    ItemKind = "note"    // 内部备注，只有成员可见
	Event   ItemKind = "event"   // 系统事件，例如重新打开、指派
)

// ItemKinds 是时间线中各项的全部类型，显示文字是翻译文件中 conversation.item_kind.<类型> 的消息。
var ItemKinds = enum.New("conversation.item_kind", enum.Item(Message), enum.Item(Note), enum.Item(Event))

// Schema 让 OpenAPI 中的 ItemKind 只接受上面的取值，并带有显示文字。
func (ItemKind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, ItemKinds) }

// Visibility 是时间线中一项的可见范围。
type Visibility string

const (
	Everyone    Visibility = "all"     // 所有人，包括对方
	MembersOnly Visibility = "members" // 只有成员
)

// Visibilities 是全部可见范围，显示文字是翻译文件中 conversation.visibility.<范围> 的消息。
var Visibilities = enum.New("conversation.visibility", enum.Item(Everyone), enum.Item(MembersOnly))

// Schema 让 OpenAPI 中的 Visibility 只接受上面的取值，并带有显示文字。
func (Visibility) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Visibilities) }

// ActorType 是参与者和时间线中发起者的类型。
type ActorType string

const (
	ContactActor ActorType = "contact" // 联系人
	MemberActor  ActorType = "member"  // 成员
	BotActor     ActorType = "bot"     // 机器人 / AI Agent
	SystemActor  ActorType = "system"  // 系统
)

// ActorTypes 是参与者的全部类型，显示文字是翻译文件中 conversation.actor_type.<类型> 的消息。
var ActorTypes = enum.New("conversation.actor_type", enum.Item(ContactActor), enum.Item(MemberActor), enum.Item(BotActor), enum.Item(SystemActor))

// Schema 让 OpenAPI 中的 ActorType 只接受上面的取值，并带有显示文字。
func (ActorType) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, ActorTypes) }

// DeliveryStatus 是消息的送达状态。
type DeliveryStatus string

const (
	Sending   DeliveryStatus = "sending"   // 发送中
	Delivered DeliveryStatus = "delivered" // 已送达
	Read      DeliveryStatus = "read"      // 已读
	Failed    DeliveryStatus = "failed"    // 失败
)

// DeliveryStatuses 是全部送达状态，显示文字是翻译文件中 conversation.delivery.<状态> 的消息。
var DeliveryStatuses = enum.New("conversation.delivery", enum.Item(Sending), enum.Item(Delivered), enum.Item(Read), enum.Item(Failed))

// Schema 让 OpenAPI 中的 DeliveryStatus 只接受上面的取值，并带有显示文字。
func (DeliveryStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, DeliveryStatuses)
}

// Conversation 是一个会话，属于一个工作区。
type Conversation struct {
	bun.BaseModel `bun:"table:conversations"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID          string         `bun:"tenant_id,notnull"`
	Kind              Kind           `bun:"kind,notnull"`
	ChannelID         *string        `bun:"channel_id"`       // 来源渠道 ID（会话最初发起的渠道）
	ReplyChannelID    *string        `bun:"reply_channel_id"` // 当前默认回复渠道 ID（随对方最新消息或坐席显式切换更新）
	ContactID         *string        `bun:"contact_id"`
	TeamID            *string        `bun:"team_id"`                 // 团队收件箱；为空表示未指派到团队
	AssigneeType      ActorType      `bun:"assignee_type,notnull"`   // 负责人的类型；为空表示没有负责人
	AssigneeID        string         `bun:"assignee_id,notnull"`     // 负责人的编号
	Status            Status         `bun:"status,notnull"`          // 服务型会话的状态
	SnoozedUntil      *time.Time     `bun:"snoozed_until"`           // 稍后处理的到期时间
	Priority          Priority       `bun:"priority,notnull"`        // 优先级
	Language          string         `bun:"language,notnull"`        // 客户的语言，见 CustomerLanguage
	WaitingSince      *time.Time     `bun:"waiting_since"`           // 对方的消息开始等待回复的时间；已回复时为空
	OpenedAt          time.Time      `bun:"opened_at,notnull"`       // 当前这一轮（新建或重新打开）的开始时间
	FirstReplyAt      *time.Time     `bun:"first_reply_at"`          // 当前这一轮第一次回复的时间；重开后清空
	ClosedAt          *time.Time     `bun:"closed_at"`               // 最近一次关闭的时间
	LastMessageAt     time.Time      `bun:"last_message_at,notnull"` // 最近一条消息的时间
	LastSeq           int64          `bun:"last_seq,notnull"`        // 时间线中最新一项的序号
	Attributes        map[string]any `bun:"attributes,type:jsonb,notnull"`
	Rating            *int           `bun:"rating"`                 // 客户最近一次评价的星数，1 到 5；没有评价过时为空
	RatingComment     string         `bun:"rating_comment,notnull"` // 评价时附的一句意见
	RatedAt           *time.Time     `bun:"rated_at"`               // 最近一次评价的时间
	RatingRequestedAt *time.Time     `bun:"rating_requested_at"`    // 最近一次关闭后向客户发出评价邀请的时间
	MentionedAt       *time.Time     `bun:"mentioned_at,scanonly"`  // 只在“@我”的列表中读出：最近一次被 @ 的时间
}

// DeliveryChannelID 返回会话 c 当前默认的出站回复渠道 ID：优先使用 ReplyChannelID，为空时回退到来源渠道 ChannelID。
func (c *Conversation) DeliveryChannelID() *string {
	if c.ReplyChannelID != nil && *c.ReplyChannelID != "" {
		return c.ReplyChannelID
	}
	if c.ChannelID != nil && *c.ChannelID != "" {
		return c.ChannelID
	}
	return nil
}

// isFinished 报告会话 c 是否已处于结束状态（服务型会话已关闭或工单已解决）。
func (c *Conversation) isFinished() bool {
	return (c.Kind == Service && c.Status == Closed) || (c.Kind == Ticket && c.Status == Status(TicketResolved))
}

// Rating 是客户对会话的评价。
type Rating struct {
	Score   int       `json:"score" doc:"1 到 5 星"`
	Comment string    `json:"comment" doc:"附的一句意见"`
	RatedAt time.Time `json:"rated_at"`
}

// rating 返回会话 c 最近一次的评价，没有评价过时返回 nil。
func (c *Conversation) rating() *Rating {
	if c.Rating == nil {
		return nil
	}
	return &Rating{Score: *c.Rating, Comment: c.RatingComment, RatedAt: *c.RatedAt}
}

// ratedSinceClosed 报告客户在会话最近一次关闭之后有没有评价过：每关闭一次可以评价一次。
func (c *Conversation) ratedSinceClosed() bool {
	return c.RatedAt != nil && c.ClosedAt != nil && !c.RatedAt.Before(*c.ClosedAt)
}

// ratingRequestedSinceClosed 报告会话最近一次关闭之后有没有向客户发出过评价邀请。
func (c *Conversation) ratingRequestedSinceClosed() bool {
	return c.RatingRequestedAt != nil && c.ClosedAt != nil && !c.RatingRequestedAt.Before(*c.ClosedAt)
}

// Participant 是会话的一个参与者。
type Participant struct {
	bun.BaseModel `bun:"table:conversation_participants"`

	ConversationID string    `bun:"conversation_id,pk"`
	Type           ActorType `bun:"participant_type,pk"`
	ID             string    `bun:"participant_id,pk"`
	CreatedAt      time.Time `bun:"created_at,notnull"`
}

// Prompt 是系统发出的结构化提示或交互卡片（例如告知回复时间、请线索留联系方式）：
// 前端优先按它用客户或成员的语言渲染，不支持卡片的外部渠道使用 Content.Text。
type Prompt struct {
	Kind   string         `json:"kind" doc:"提示的种类：reply_time_open、reply_time_closed、collect_contact"`
	Params map[string]any `json:"params,omitempty" doc:"渲染提示用的参数"`
}

// Content 是时间线中一项的内容：消息和备注的文字，或者系统事件的名称和数据。
type Content struct {
	Text     string         `json:"text,omitempty" doc:"文字；翻译后发出的回复是发给对方的译文"`
	Original string         `json:"original,omitempty" doc:"成员写的原文；只有翻译后发出的回复有"`
	Language string         `json:"language,omitempty" doc:"text 的语言（BCP 47），只有翻译后发出的回复有"`
	Mentions []string       `json:"mentions,omitempty" doc:"内部备注中 @ 到的成员 ID"`
	Event    string         `json:"event,omitempty" doc:"系统事件的名称，例如 conversation.reopened"`
	Data     map[string]any `json:"data,omitempty" doc:"系统事件的数据"`
	Prompt   *Prompt        `json:"prompt,omitempty" doc:"系统发出的结构化提示或交互卡片"`
}

// Item 是时间线中的一项，写入后保持原样。
type Item struct {
	bun.BaseModel `bun:"table:timeline_items"`

	ID             string         `bun:"id,pk"`
	CreatedAt      time.Time      `bun:"created_at,notnull"`
	UpdatedAt      time.Time      `bun:"updated_at,notnull"`
	TenantID       string         `bun:"tenant_id,notnull"`
	ConversationID string         `bun:"conversation_id,notnull"`
	Seq            int64          `bun:"seq,notnull"`
	Kind           ItemKind       `bun:"kind,notnull"`
	ActorType      ActorType      `bun:"actor_type,notnull"`
	ActorID        string         `bun:"actor_id,notnull"`
	Visibility     Visibility     `bun:"visibility,notnull"`
	Content        Content        `bun:"content,type:jsonb,notnull"`
	Derived        map[string]any `bun:"derived,type:jsonb,notnull"` // 派生内容，例如按语言的译文，由下游补上
	ChannelID      *string        `bun:"channel_id"`                 // 来自或发往的渠道
	ExternalID     *string        `bun:"external_id"`                // 渠道中的消息编号，用来去重
}

// Delivery 是一条消息的送达状态。
type Delivery struct {
	bun.BaseModel `bun:"table:message_deliveries"`

	ItemID     string         `bun:"item_id,pk"`
	Status     DeliveryStatus `bun:"status,notnull"`
	ExternalID string         `bun:"external_id,notnull"` // 发出后在渠道中的编号，用来对应回执
	Error      string         `bun:"error,notnull"`
	UpdatedAt  time.Time      `bun:"updated_at,notnull"`
}

// Round 是服务型会话的一轮服务记录：从打开或重新打开开始，记录本轮首次回复（含首次回复人）、关闭归属快照和客户评价。
type Round struct {
	bun.BaseModel `bun:"table:conversation_rounds"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID         string     `bun:"tenant_id,notnull"`
	ConversationID   string     `bun:"conversation_id,notnull"`
	Round            int        `bun:"round,notnull"`
	TeamID           *string    `bun:"team_id"`
	AssigneeType     ActorType  `bun:"assignee_type,notnull"`
	AssigneeID       string     `bun:"assignee_id,notnull"`
	ClosedByType     ActorType  `bun:"closed_by_type,notnull"`
	ClosedByID       string     `bun:"closed_by_id,notnull"`
	OpenedAt         time.Time  `bun:"opened_at,notnull"`
	FirstReplyAt     *time.Time `bun:"first_reply_at"`
	FirstReplyByType ActorType  `bun:"first_reply_by_type,notnull"`
	FirstReplyByID   string     `bun:"first_reply_by_id,notnull"`
	ClosedAt         *time.Time `bun:"closed_at"`
	Rating           *int       `bun:"rating"`
	RatingComment    string     `bun:"rating_comment,notnull"`
	RatedAt          *time.Time `bun:"rated_at"`
}

// openRound 在事务 tx 中为服务型会话 c 创建新的一轮服务记录，轮次号从 1 递增。
func openRound(ctx context.Context, tx bun.IDB, c *Conversation, at time.Time) error {
	if c.Kind != Service {
		return nil
	}
	var next int
	if err := tx.NewRaw("SELECT coalesce(max(round), 0) + 1 FROM conversation_rounds WHERE conversation_id = ?", c.ID).Scan(ctx, &next); err != nil {
		return errors.Wrap(err, "next conversation round")
	}
	r := &Round{
		ID:             newID(),
		TenantID:       c.TenantID,
		ConversationID: c.ID,
		Round:          next,
		TeamID:         c.TeamID,
		AssigneeType:   c.AssigneeType,
		AssigneeID:     c.AssigneeID,
		OpenedAt:       at,
	}
	_, err := tx.NewInsert().Model(r).Exec(ctx)
	return errors.Wrap(err, "insert conversation round")
}

// recordFirstReply 在事务 tx 中把本轮首次回复的时间与发起者写入当前未关闭的服务轮次。
func recordFirstReply(ctx context.Context, tx bun.IDB, c *Conversation, actor ActorType, actorID string, at time.Time) error {
	if c.Kind != Service {
		return nil
	}
	_, err := tx.NewUpdate().Table("conversation_rounds").
		Set("first_reply_at = ?", at).
		Set("first_reply_by_type = ?", actor).
		Set("first_reply_by_id = ?", actorID).
		Set("team_id = ?", c.TeamID).
		Set("assignee_type = ?", c.AssigneeType).
		Set("assignee_id = ?", c.AssigneeID).
		Set("updated_at = ?", at).
		Where("tenant_id = ? AND conversation_id = ? AND closed_at IS NULL AND first_reply_at IS NULL", c.TenantID, c.ID).
		Exec(ctx)
	return errors.Wrap(err, "record round first reply")
}

// Appended 是“时间线有新内容”的任务内容：每个下游收到一份，按它读取新的一项。下游可能收到重复的任务，处理要能重复执行。
type Appended struct {
	TenantID       string `json:"tenant_id"`
	ConversationID string `json:"conversation_id"`
	ItemID         string `json:"item_id"`
	Seq            int64  `json:"seq"`
}

// AutoTranslateJob 描述一条需要在投递前由后台自动翻译的出站消息。
type AutoTranslateJob struct {
	TenantID       string
	ConversationID string
	MemberID       string
	Text           string
	Language       string // 可选目标语言（BCP 47）；为空时按会话对方语言自动推断
	Tone           string // 可选语气覆盖；为空时使用工作区默认语气
}

// AutoTranslator 在异步投递前将成员原文翻译成目标语言；needed 为 false 时按原文发出。
type AutoTranslator func(ctx context.Context, req AutoTranslateJob) (text, language string, needed bool, err error)

// Downstream 是会话有变化时要通知的下游，由入口装配。
type Downstream struct {
	Topics []queue.Topic[Appended] // 交给 worker 的下游，每个一个 topic，各自重试，例如把消息发到外部渠道（DeliverTopic），以后还有翻译
	Hub    *broadcast.Hub          // 提交后把变化推送到能看到会话的成员的个人频道；为 nil 时不推送
	Roles  *authz.Store            // 推送时按成员的角色判断可见范围，Hub 不为 nil 时必须设置
	Media  Media                   // 文件与缩略图服务：消息和内部备注确认、读取附件时使用
	// AutoTranslate 在后台投递前将折叠态发送的原文异步翻译成目标语言；为 nil 时跳过自动翻译直接发送原文。
	AutoTranslate AutoTranslator
	// ContactPush 在提交后把会话的变化推送给对方（联系人）打开着的聊天窗口，参数是会话的渠道和联系人；为 nil 时不推送。
	// 会话的状态变了，或者联系人自己发了消息（他的其他页面随之更新）时推送；成员的回复由连接器发出时推送。
	ContactPush func(ctx context.Context, channelID, contactID string, ev push.Event) error
}

// authorize 是会话动作统一的权限检查：成员按权限检查；系统主体（连接器、命令、种子数据）可以执行全部会话动作。
// perm 为空表示只有系统主体可以执行，例如写入渠道收到的消息。能看到哪些会话由 visible 决定；以后 AI 用途的配置也在这里检查。
func authorize(sub identity.Subject, perm auth.Permission) error {
	switch {
	case sub.Type == identity.System:
		return nil
	case perm != "" && sub.Type == member.Type && sub.Can(string(perm)):
		return nil
	}
	return ErrForbidden.New()
}

// find 读取工作区 tenant 中的会话 id，不存在或属于别的工作区时返回 ErrNotFound。lock 为 true 时锁住这一行，
// 同一个会话的写入因此依次进行，时间线的序号连续。
func find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*Conversation, error) {
	c := new(Conversation)
	q := db.NewSelect().Model(c).Where("id = ? AND tenant_id = ?", id, tenant)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return c, errors.Wrap(err, "load conversation")
}

// commit 在事务 tx 中把 items 依次追加到会话 c 的时间线，保存 c 的修改，并为每一项向每个下游发布任务；
// 提交后把变化推送给修改前后能看到 c 的成员。调用前 c 已经被锁住（find 的 lock）；items 的序号、时间和工作区在这里填写。
func commit(ctx context.Context, tx database.Tx, down Downstream, c *Conversation, items ...*Item) error {
	var audience []string // 提交后推送给这些成员
	var alerts map[string]string
	if down.Hub != nil {
		var err error
		if audience, alerts, err = audienceAndAlertsOf(ctx, tx, down.Roles, c, items); err != nil {
			return err
		}
	}
	contactChannels, err := contactPushChannels(ctx, tx, down, c, items)
	if err != nil {
		return err
	}
	ticketTargets, err := ticketContactPushTargets(ctx, tx, down, c, items)
	if err != nil {
		return err
	}
	at := now()
	tasks := make([]Appended, 0, len(items))
	syncIDs := make([]string, 0, len(items))
	for _, it := range items {
		c.LastSeq++
		itemAt := at
		if !it.CreatedAt.IsZero() {
			itemAt = it.CreatedAt.UTC().Truncate(time.Microsecond)
		}
		it.ID, it.Seq, it.CreatedAt, it.UpdatedAt, it.TenantID, it.ConversationID = newID(), c.LastSeq, itemAt, at, c.TenantID, c.ID
		if it.Derived == nil {
			it.Derived = map[string]any{}
		}
		if _, err := tx.NewInsert().Model(it).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert timeline item")
		}
		tasks = append(tasks, Appended{TenantID: c.TenantID, ConversationID: c.ID, ItemID: it.ID, Seq: it.Seq})
		if it.Searchable() {
			syncIDs = append(syncIDs, it.ID)
		}
	}
	if _, err := tx.NewUpdate().Model(c).WherePK().Exec(ctx); err != nil {
		return errors.Wrap(err, "save conversation")
	}
	for _, topic := range down.Topics {
		if err := topic.PublishAll(ctx, tx, tasks); err != nil {
			return err
		}
	}
	if len(syncIDs) > 0 {
		if err := SearchIndex.Sync(ctx, tx, syncIDs...); err != nil {
			return err
		}
	}
	ev := push.Event{Type: pushType(c.Kind), ID: c.ID, Seq: c.LastSeq}
	if down.Hub != nil {
		hub, tenant := down.Hub, c.TenantID
		tx.AfterCommit(func(ctx context.Context) error {
			return errors.Wrap(push.SendWithAlerts(ctx, hub, tenant, audience, ev, alerts), "push conversation changed")
		})
	}
	if len(contactChannels) > 0 && c.ContactID != nil {
		notify, contactID := down.ContactPush, *c.ContactID
		tx.AfterCommit(func(ctx context.Context) error {
			var errs []error
			for _, chID := range contactChannels {
				if err := notify(ctx, chID, contactID, ev); err != nil {
					errs = append(errs, errors.Wrap(err, "push conversation changed to contact"))
				}
			}
			return errors.Join(errs...)
		})
	}
	if len(ticketTargets) > 0 {
		notify := down.ContactPush
		tx.AfterCommit(func(ctx context.Context) error {
			var errs []error
			for _, target := range ticketTargets {
				if err := notify(ctx, target.ChannelID, target.ContactID, ev); err != nil {
					errs = append(errs, errors.Wrap(err, "push ticket changed to contact"))
				}
			}
			return errors.Join(errs...)
		})
	}
	return nil
}

// contactPushTarget 是工单变更推送到客户聊天插件频道的目标（渠道 ID + 联系人 ID）。
type contactPushTarget struct {
	ChannelID string `bun:"channel_id"`
	ContactID string `bun:"contact_id"`
}

// ticketContactPushTargets 返回工单 c 发生对客户有意义的变更（公开进度、状态变化、标题/描述/可见范围修改、
// 建单或关联关系变更）后，需要向网页聊天插件推送 push.Ticket 的目标频道列表。
func ticketContactPushTargets(ctx context.Context, db bun.IDB, down Downstream, c *Conversation, items []*Item) ([]contactPushTarget, error) {
	if down.ContactPush == nil || c.Kind != Ticket {
		return nil, nil
	}
	relevant := false
	for _, it := range items {
		if it.Kind == Message && it.Visibility == Everyone {
			relevant = true
			break
		}
		if it.Kind == Event {
			switch it.Content.Event {
			case "ticket.created", "ticket.updated", "ticket.status_changed", "ticket.contact_linked", "ticket.contact_unlinked":
				relevant = true
			}
		}
		if relevant {
			break
		}
	}
	if !relevant {
		return nil, nil
	}
	// 查出关联联系人可推送的启用网页聊天插件频道：
	// 已登录用户推送到其全部已启用 widget 渠道，访客线索推送到其服务型会话所在的启用 widget 渠道。
	var targets []contactPushTarget
	err := db.NewRaw(`
		SELECT DISTINCT ci.channel_id, tc.contact_id
		FROM ticket_contacts tc
		JOIN contacts ct ON ct.id = tc.contact_id AND ct.tenant_id = tc.tenant_id
		JOIN contact_identities ci ON ci.tenant_id = tc.tenant_id AND ci.contact_id = tc.contact_id
		JOIN channels ch ON ch.id = ci.channel_id
		WHERE tc.tenant_id = ? AND tc.ticket_id = ?
			AND ct.kind = ? AND ch.kind = ? AND ch.enabled = true
		UNION
		SELECT DISTINCT sc.channel_id, tc.contact_id
		FROM ticket_contacts tc
		JOIN contacts ct ON ct.id = tc.contact_id AND ct.tenant_id = tc.tenant_id
		JOIN conversations sc ON sc.tenant_id = tc.tenant_id AND sc.kind = ? AND sc.contact_id = tc.contact_id
		JOIN channels ch ON ch.id = sc.channel_id
		WHERE tc.tenant_id = ? AND tc.ticket_id = ?
			AND sc.channel_id IS NOT NULL
			AND ct.kind <> ? AND ch.kind = ? AND ch.enabled = true`,
		c.TenantID, c.ID, contact.User, channel.Widget,
		Service, c.TenantID, c.ID, contact.User, channel.Widget,
	).Scan(ctx, &targets)
	if err != nil {
		return nil, errors.Wrap(err, "load ticket contact push targets")
	}
	return targets, nil
}

// pushType 返回会话种类对应的实时推送事件类型：工单推 push.Ticket，内部聊天推 push.Chat，其余推 push.Conversation。
func pushType(kind Kind) string {
	switch kind {
	case Ticket:
		return push.Ticket
	case Chat:
		return push.Chat
	default:
		return push.Conversation
	}
}

// contactPushChannels 返回本次会话变更提交后需要向联系人实时推送的渠道 ID 列表。
// 当服务型会话状态改变、发出评价邀请、联系人发了消息、或成员/系统通过其他外部渠道发出所有人可见消息时：
//   - 已登录用户（user）推送到他在本工作区所有已启用的网页聊天插件渠道，实现跨渠道实时同步；
//   - 访客线索（lead）只推送到会话来源渠道。
//
// 要在保存 c 之前调用，以便读取变更前的状态。
func contactPushChannels(ctx context.Context, db bun.IDB, down Downstream, c *Conversation, items []*Item) ([]string, error) {
	if down.ContactPush == nil || c.ContactID == nil {
		return nil, nil
	}
	if c.Kind == Chat {
		if c.ChannelID == nil {
			return nil, nil
		}
		for _, it := range items {
			if it.Kind == Message && it.Visibility == Everyone && it.ActorType == ContactActor {
				return []string{*c.ChannelID}, nil
			}
		}
		return nil, nil
	}
	if c.Kind != Service {
		return nil, nil
	}
	needPush := false
	sentChannels := map[string]bool{}
	for _, it := range items {
		if it.Kind != Message || it.Visibility != Everyone {
			continue
		}
		if it.ActorType == ContactActor {
			needPush = true
			break
		}
		// 成员或系统消息发往某个外部渠道时，跨渠道已登录用户的网页插件也应同步刷新
		if it.ChannelID != nil {
			sentChannels[*it.ChannelID] = true
		}
	}
	if !needPush {
		var before struct {
			Status            Status     `bun:"status"`
			RatingRequestedAt *time.Time `bun:"rating_requested_at"`
		}
		if err := db.NewSelect().Table("conversations").Column("status", "rating_requested_at").Where("id = ?", c.ID).Scan(ctx, &before); err != nil {
			return nil, errors.Wrap(err, "load status before change")
		}
		needPush = before.Status != c.Status || !sameTime(before.RatingRequestedAt, c.RatingRequestedAt)
	}
	if !needPush && len(sentChannels) == 0 {
		return nil, nil
	}
	var kind contact.Kind
	if err := db.NewSelect().Table("contacts").Column("kind").Where("id = ? AND tenant_id = ?", *c.ContactID, c.TenantID).Scan(ctx, &kind); err != nil {
		return nil, errors.Wrap(err, "load contact kind for push")
	}
	if kind != contact.User {
		if !needPush || c.ChannelID == nil {
			return nil, nil
		}
		return []string{*c.ChannelID}, nil
	}
	// 已登录用户：推送到该用户在工作区内已绑定的全部已启用网页聊天插件频道
	var widgetIDs []string
	err := db.NewSelect().TableExpr("contact_identities AS ci").
		ColumnExpr("DISTINCT ci.channel_id").
		Join("JOIN channels AS ch ON ch.id = ci.channel_id").
		Where("ci.tenant_id = ? AND ci.contact_id = ? AND ch.kind = ? AND ch.enabled = true", c.TenantID, *c.ContactID, channel.Widget).
		Scan(ctx, &widgetIDs)
	if err != nil {
		return nil, errors.Wrap(err, "load contact widget channels for push")
	}
	if !needPush {
		// 仅由成员/系统消息触发：排除本次消息直接发往的 widget 渠道（由 widget.Connector.Send 负责推送）
		out := make([]string, 0, len(widgetIDs))
		for _, id := range widgetIDs {
			if !sentChannels[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
	return widgetIDs, nil
}

// sameTime 报告两个可选时间是否相等。
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// join 在事务 tx 中把参与者加入会话 conversationID，已经在其中时保持原样。
func join(ctx context.Context, tx bun.IDB, conversationID string, typ ActorType, id string) error {
	p := &Participant{ConversationID: conversationID, Type: typ, ID: id, CreatedAt: now()}
	_, err := tx.NewInsert().Model(p).On("CONFLICT DO NOTHING").Exec(ctx)
	return errors.Wrap(err, "add participant")
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// now 返回当前时间，精度与 PostgreSQL 的 timestamptz 一致。
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
