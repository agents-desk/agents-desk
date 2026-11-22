package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
)

// Inbox 是收件箱中的一个列表。团队收件箱的列表是“全部”加上团队收件箱的筛选。
type Inbox string

const (
	InboxMine       Inbox = "mine"       // 我的：负责人是我
	InboxMentions   Inbox = "mentions"   // @我：我在内部备注中被 @ 过，最近被 @ 的在前，不按状态筛选
	InboxUnassigned Inbox = "unassigned" // 未指派：没有团队收件箱，也没有负责人
	InboxAll        Inbox = "all"        // 全部：我能看到的全部会话
)

// Inboxes 是收件箱的全部列表，显示文字是翻译文件中 conversation.inbox.<列表> 的消息。
var Inboxes = enum.New("conversation.inbox", enum.Item(InboxMine), enum.Item(InboxMentions), enum.Item(InboxUnassigned), enum.Item(InboxAll))

// Schema 让 OpenAPI 中的 Inbox 只接受上面的取值，并带有显示文字。
func (Inbox) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Inboxes) }

// Sort 是会话列表的排序。
type Sort string

const (
	ByWaiting Sort = "waiting" // 按等待时长：等待回复的在前，等得最久的在最上面；已回复的按最近活动排在后面
	ByRecent  Sort = "recent"  // 按最近活动：最近有消息的在前
)

// Sorts 是会话列表的全部排序，显示文字是翻译文件中 conversation.sort.<排序> 的消息。
var Sorts = enum.New("conversation.sort", enum.Item(ByWaiting), enum.Item(ByRecent))

// Schema 让 OpenAPI 中的 Sort 只接受上面的取值，并带有显示文字。
func (Sort) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Sorts) }

// ListInput 是会话列表的查询条件：哪个列表、视图、筛选和排序。
type ListInput struct {
	page.CursorParams
	Inbox      Inbox    `query:"inbox" default:"all" doc:"收件箱中的哪个列表"`
	ViewID     string   `query:"view_id" format:"uuid" doc:"可选：按已保存视图的筛选条件与默认排序列出"`
	Filter     string   `query:"filter" maxLength:"4096" doc:"可选：JSON 编码的统一筛选条件（Filter），用于高级筛选或在视图内临时微调预览"`
	TeamID     string   `query:"team_id" format:"uuid" doc:"只列出这个团队收件箱的会话；团队收件箱的列表是 inbox=all 加上它"`
	Status     Status   `query:"status" default:"open" doc:"只列出这个状态的会话；@我 的列表和视图不按它筛选"`
	AssigneeID string   `query:"assignee_id" format:"uuid" doc:"只列出负责人是这位成员的会话"`
	ChannelID  string   `query:"channel_id" format:"uuid" doc:"只列出来自这个渠道的会话"`
	ContactID  string   `query:"contact_id" format:"uuid" doc:"只列出这个联系人的会话，不按状态筛选，例如详情中的“最近的会话”"`
	Priority   Priority `query:"priority" doc:"只列出这个优先级的会话"`
	TagID      string   `query:"tag_id" format:"uuid" doc:"只列出带有这个标签的会话"`
	Sort       Sort     `query:"sort" doc:"排序：默认按视图排序或 waiting；@我 的列表总是最近被 @ 的在前"`
}

// ListOutput 是一页会话。
type ListOutput struct {
	Body page.Cursor[ConversationDTO]
}

// List 列出收件箱中的会话。
type List struct {
	db *bun.DB
}

// NewList 返回会话列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// waitingKey 是按等待时长排序时的位置，三个值都从小到大：
// Group 0 是等待回复的、1 是已回复的；Key 在等待回复时是开始等待的时间，已回复时是最近活动时间的相反数（微秒）。
type waitingKey struct {
	Group int    `json:"g"`
	Key   int64  `json:"k"`
	ID    string `json:"id"`
}

// timeKey 是按时间从新到旧排序时的位置：最近活动的时间，或者最近被 @ 的时间。
type timeKey struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

// 按等待时长排序用的表达式，与 waitingKey 对应。
const (
	waitingGroup = "(CASE WHEN conversation.waiting_since IS NULL THEN 1 ELSE 0 END)"
	waitingValue = "(CASE WHEN conversation.waiting_since IS NULL THEN -(extract(epoch FROM conversation.last_message_at) * 1000000)::bigint " +
		"ELSE (extract(epoch FROM conversation.waiting_since) * 1000000)::bigint END)"
)

// Handle 返回 sub 所在工作区中收件箱列表或视图的一页服务型会话，只包括 sub 能看到的（visible），按 in 筛选和排序。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Cursor[ConversationDTO], error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}

	sortMode := in.Sort
	var activeFilter *Filter
	if in.ViewID != "" {
		v, err := NewViews(a.db).Get(ctx, sub, in.ViewID)
		if err != nil {
			return nil, err
		}
		f := v.Config.Filter
		activeFilter = &f
		if sortMode == "" && v.Config.Sort != "" {
			sortMode = v.Config.Sort
		}
	}
	if in.Filter != "" {
		f, err := parseFilterParam(ctx, a.db, sub.Tenant, in.Filter)
		if err != nil {
			return nil, err
		}
		activeFilter = &f
	}
	if sortMode == "" {
		sortMode = ByWaiting
	}

	var rows []Conversation
	q := a.db.NewSelect().Model(&rows).Where("conversation.tenant_id = ? AND conversation.kind = ?", sub.Tenant, Service).Limit(in.Limit())
	q = visible(q, sub)

	if activeFilter != nil {
		var attrDefs map[string]attribute.Definition
		if len(activeFilter.Attributes) > 0 {
			var err error
			attrDefs, err = loadConversationAttrDefs(ctx, a.db, sub.Tenant)
			if err != nil {
				return nil, err
			}
		}
		pred, args := buildFilterPredicate(sub, *activeFilter, attrDefs)
		q = q.Where(pred, args...)
		if in.ContactID != "" {
			q = q.Where("conversation.contact_id = ?", in.ContactID)
		}
		if in.TagID != "" {
			q = q.Where("EXISTS (SELECT 1 FROM conversation_tags AS ct WHERE ct.tenant_id = ? AND ct.tag_id = ? AND ct.conversation_id = conversation.id)", sub.Tenant, in.TagID)
		}
	} else {
		status := in.Status
		if status == "" {
			status = Open
		}
		switch in.Inbox {
		case InboxMine:
			q = q.Where("conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, sub.ID)
		case InboxUnassigned:
			q = q.Where("conversation.team_id IS NULL AND conversation.assignee_type = ''")
		}
		if in.Inbox != InboxMentions && in.ContactID == "" {
			q = q.Where("conversation.status = ?", status)
		}
		if in.ContactID != "" {
			q = q.Where("conversation.contact_id = ?", in.ContactID)
		}
		if in.TeamID != "" {
			q = q.Where("conversation.team_id = ?", in.TeamID)
		}
		if in.AssigneeID != "" {
			q = q.Where("conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, in.AssigneeID)
		}
		if in.ChannelID != "" {
			q = q.Where("conversation.channel_id = ?", in.ChannelID)
		}
		if in.Priority != "" {
			q = q.Where("conversation.priority = ?", in.Priority)
		}
		if in.TagID != "" {
			q = q.Where("EXISTS (SELECT 1 FROM conversation_tags AS ct WHERE ct.tenant_id = ? AND ct.tag_id = ? AND ct.conversation_id = conversation.id)", sub.Tenant, in.TagID)
		}
	}

	// 排序和游标：@我 按最近被 @ 的时间，其他列表与视图按 sortMode
	var key func(ConversationDTO) any
	switch {
	case activeFilter == nil && in.Inbox == InboxMentions:
		q = q.Join("JOIN (SELECT conversation_id, max(created_at) AS mentioned_at FROM conversation_mentions WHERE tenant_id = ? AND member_id = ? GROUP BY conversation_id) AS m ON m.conversation_id = conversation.id", sub.Tenant, sub.ID).
			ColumnExpr("conversation.*").ColumnExpr("m.mentioned_at").OrderExpr("m.mentioned_at DESC, conversation.id DESC")
		var after timeKey
		if ok, err := in.After(&after); err != nil {
			return nil, err
		} else if ok {
			q = q.Where("(m.mentioned_at, conversation.id) < (?, ?)", after.At, after.ID)
		}
		key = func(d ConversationDTO) any { return timeKey{At: *d.MentionedAt, ID: d.ID} }
	case sortMode == ByRecent:
		q = q.OrderExpr("conversation.last_message_at DESC, conversation.id DESC")
		var after timeKey
		if ok, err := in.After(&after); err != nil {
			return nil, err
		} else if ok {
			q = q.Where("(conversation.last_message_at, conversation.id) < (?, ?)", after.At, after.ID)
		}
		key = func(d ConversationDTO) any { return timeKey{At: d.LastMessageAt, ID: d.ID} }
	default:
		q = q.OrderExpr(waitingGroup + ", " + waitingValue + ", conversation.id")
		var after waitingKey
		if ok, err := in.After(&after); err != nil {
			return nil, err
		} else if ok {
			q = q.Where("("+waitingGroup+", "+waitingValue+", conversation.id) > (?, ?, ?)", after.Group, after.Key, after.ID)
		}
		key = func(d ConversationDTO) any { return toWaitingKey(d) }
	}
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list conversations")
	}
	items, err := toDTOs(ctx, a.db, sub, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewCursor(items, in.CursorParams, key)
	return &p, nil
}

// toWaitingKey 返回会话 d 按等待时长排序时的位置，与 waitingGroup、waitingValue 的计算一致。
func toWaitingKey(d ConversationDTO) waitingKey {
	if d.WaitingSince != nil {
		return waitingKey{Group: 0, Key: d.WaitingSince.UnixMicro(), ID: d.ID}
	}
	return waitingKey{Group: 1, Key: -d.LastMessageAt.UnixMicro(), ID: d.ID}
}

// HTTP 处理 GET /t/{slug}/api/conversations。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
