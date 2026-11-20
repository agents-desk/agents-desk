// Package conversation 是会话：会话、参与者、只追加的时间线，以及写入时间线的业务动作。
//
// 所有写操作走同一条路径：业务动作经 authorize 检查权限，在一个事务中修改会话、追加时间线（commit），
// 并为每个下游（Downstream）发布一个任务；事务提交后，任务由 worker 交给下游处理，例如把消息发到外部渠道。
package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

var (
	// ErrNotFound 是会话不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "conversation.not_found")
	// ErrForbidden 是调用者没有权限执行这个会话动作。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "conversation.forbidden")
)

// Kind 是会话的种类。
type Kind string

const (
	Service Kind = "service" // 服务型会话：在收件箱中处理，有团队收件箱、负责人和状态
	Chat    Kind = "chat"    // 聊天型会话：长期存在的单聊或群聊（以后的阶段）
)

// Kinds 是会话的全部种类，显示文字是翻译文件中 conversation.kind.<种类> 的消息。
var Kinds = enum.New("conversation.kind", Service, Chat)

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
var Statuses = enum.New("conversation.status", Open, Snoozed, Closed)

// Schema 让 OpenAPI 中的 Status 只接受上面的取值，并带有显示文字。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// Priority 是会话的优先级。
type Priority string

const (
	Normal Priority = "normal" // 普通
	High   Priority = "high"   // 高
)

// Priorities 是会话的全部优先级，显示文字是翻译文件中 conversation.priority.<优先级> 的消息。
var Priorities = enum.New("conversation.priority", Normal, High)

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
var ItemKinds = enum.New("conversation.item_kind", Message, Note, Event)

// Schema 让 OpenAPI 中的 ItemKind 只接受上面的取值，并带有显示文字。
func (ItemKind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, ItemKinds) }

// Visibility 是时间线中一项的可见范围。
type Visibility string

const (
	Everyone    Visibility = "all"     // 所有人，包括对方
	MembersOnly Visibility = "members" // 只有成员
)

// Visibilities 是全部可见范围，显示文字是翻译文件中 conversation.visibility.<范围> 的消息。
var Visibilities = enum.New("conversation.visibility", Everyone, MembersOnly)

// Schema 让 OpenAPI 中的 Visibility 只接受上面的取值，并带有显示文字。
func (Visibility) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Visibilities) }

// ActorType 是参与者和时间线中发起者的类型。参与者的类型会继续增加：外部群中的人、机器人、AI。
type ActorType string

const (
	ContactActor ActorType = "contact" // 联系人
	MemberActor  ActorType = "member"  // 成员
	SystemActor  ActorType = "system"  // 系统
)

// ActorTypes 是参与者的全部类型，显示文字是翻译文件中 conversation.actor_type.<类型> 的消息。
var ActorTypes = enum.New("conversation.actor_type", ContactActor, MemberActor, SystemActor)

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
var DeliveryStatuses = enum.New("conversation.delivery", Sending, Delivered, Read, Failed)

// Schema 让 OpenAPI 中的 DeliveryStatus 只接受上面的取值，并带有显示文字。
func (DeliveryStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, DeliveryStatuses)
}

// Conversation 是一个会话，属于一个工作区。
type Conversation struct {
	bun.BaseModel `bun:"table:conversations"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID      string         `bun:"tenant_id,notnull"`
	Kind          Kind           `bun:"kind,notnull"`
	ChannelID     *string        `bun:"channel_id"`
	ContactID     *string        `bun:"contact_id"`
	TeamID        *string        `bun:"team_id"`                 // 团队收件箱；为空表示未指派到团队
	AssigneeType  ActorType      `bun:"assignee_type,notnull"`   // 负责人的类型；为空表示没有负责人
	AssigneeID    string         `bun:"assignee_id,notnull"`     // 负责人的编号
	Status        Status         `bun:"status,notnull"`          // 服务型会话的状态
	SnoozedUntil  *time.Time     `bun:"snoozed_until"`           // 稍后处理的到期时间
	Priority      Priority       `bun:"priority,notnull"`        // 优先级
	Language      string         `bun:"language,notnull"`        // 对方最近一条消息识别出的语言
	WaitingSince  *time.Time     `bun:"waiting_since"`           // 对方的消息开始等待回复的时间；已回复时为空
	FirstReplyAt  *time.Time     `bun:"first_reply_at"`          // 第一次回复的时间
	ClosedAt      *time.Time     `bun:"closed_at"`               // 最近一次关闭的时间
	LastMessageAt time.Time      `bun:"last_message_at,notnull"` // 最近一条消息的时间
	LastSeq       int64          `bun:"last_seq,notnull"`        // 时间线中最新一项的序号
	Attributes    map[string]any `bun:"attributes,type:jsonb,notnull"`
	MentionedAt   *time.Time     `bun:"mentioned_at,scanonly"` // 只在“@我”的列表中读出：最近一次被 @ 的时间
}

// Participant 是会话的一个参与者。
type Participant struct {
	bun.BaseModel `bun:"table:conversation_participants"`

	ConversationID string    `bun:"conversation_id,pk"`
	Type           ActorType `bun:"participant_type,pk"`
	ID             string    `bun:"participant_id,pk"`
	CreatedAt      time.Time `bun:"created_at,notnull"`
}

// Content 是时间线中一项的内容：消息和备注的文字，或者系统事件的名称和数据。
type Content struct {
	Text     string         `json:"text,omitempty"`
	Mentions []string       `json:"mentions,omitempty" doc:"内部备注中 @ 到的成员 ID"`
	Event    string         `json:"event,omitempty" doc:"系统事件的名称，例如 conversation.reopened"`
	Data     map[string]any `json:"data,omitempty" doc:"系统事件的数据"`
}

// Item 是时间线中的一项，写入后保持原样。
type Item struct {
	bun.BaseModel `bun:"table:timeline_items"`

	ID             string         `bun:"id,pk"`
	CreatedAt      time.Time      `bun:"created_at,notnull"`
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

// Appended 是“时间线有新内容”的任务内容：每个下游收到一份，按它读取新的一项。下游可能收到重复的任务，处理要能重复执行。
type Appended struct {
	TenantID       string `json:"tenant_id"`
	ConversationID string `json:"conversation_id"`
	ItemID         string `json:"item_id"`
	Seq            int64  `json:"seq"`
}

// Downstream 是时间线有新内容时要通知的下游，每个下游一个 topic，各自重试，由入口装配。
// 例如把消息发到外部渠道（DeliverTopic），以后还有实时推送、翻译。
type Downstream []queue.Topic[Appended]

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

// commit 在事务 tx 中把 items 依次追加到会话 c 的时间线，保存 c 的修改，并为每一项向每个下游发布任务。
// 调用前 c 已经被锁住（find 的 lock）；items 的序号、时间和工作区在这里填写。
func commit(ctx context.Context, tx bun.IDB, down Downstream, c *Conversation, items ...*Item) error {
	at := now()
	tasks := make([]Appended, 0, len(items))
	for _, it := range items {
		c.LastSeq++
		it.ID, it.Seq, it.CreatedAt, it.TenantID, it.ConversationID = newID(), c.LastSeq, at, c.TenantID, c.ID
		if it.Derived == nil {
			it.Derived = map[string]any{}
		}
		if _, err := tx.NewInsert().Model(it).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert timeline item")
		}
		tasks = append(tasks, Appended{TenantID: c.TenantID, ConversationID: c.ID, ItemID: it.ID, Seq: it.Seq})
	}
	if _, err := tx.NewUpdate().Model(c).WherePK().Exec(ctx); err != nil {
		return errors.Wrap(err, "save conversation")
	}
	for _, topic := range down {
		if err := topic.PublishAll(ctx, tx, tasks); err != nil {
			return err
		}
	}
	return nil
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
