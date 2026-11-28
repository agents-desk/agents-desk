package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
)

// Summary 是收件箱中一个列表的数量和积压情况。
type Summary struct {
	Count              int        `json:"count" doc:"左侧显示的数量：打开的会话数；@我 是还有未读的 @ 的会话数；视图是满足视图全部条件的会话数"`
	Waiting            int        `json:"waiting" doc:"打开的会话中等待回复的数量"`
	OldestWaitingSince *time.Time `json:"oldest_waiting_since,omitempty" doc:"打开的会话中等得最久的那个开始等待的时间；没有等待回复的会话时为空"`
}

// TeamSummary 是一个团队收件箱的列表：团队收件箱和它的数量。
type TeamSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Summary
}

// TicketInboxSummary 是收件箱侧栏中工单分组的未解决数量统计。
type TicketInboxSummary struct {
	Mine       int `json:"mine" doc:"指派给我的未解决工单数"`
	Unassigned int `json:"unassigned" doc:"未指派团队且未指派负责人的未解决工单数"`
	All        int `json:"all" doc:"我能看到的全部未解决工单数"`
}

// InboxDTO 是收件箱左侧的全部列表和它们的数量，只算当前成员能看到的会话和工单。
type InboxDTO struct {
	Mine            Summary            `json:"mine" doc:"我的"`
	Mentions        Summary            `json:"mentions" doc:"@我"`
	Unassigned      Summary            `json:"unassigned" doc:"未指派"`
	All             Summary            `json:"all" doc:"全部"`
	Teams           []TeamSummary      `json:"teams" nullable:"false" doc:"团队收件箱：我所在的；可见范围是全部会话的成员是全部团队收件箱，按名称排列"`
	Views           []ViewSummary      `json:"views" nullable:"false" doc:"已保存视图：共享在前、个人在后，各带实时会话数量与积压情况"`
	Tickets         TicketInboxSummary `json:"tickets" doc:"工单分组的未解决数量"`
	Spam            int                `json:"spam" doc:"垃圾会话数量（排除在常规统计之外）"`
	WaitingApproval int                `json:"waiting_approval" doc:"当前等待人工审批 AI 工具调用的打开会话数"`
}

// InboxOutput 是收件箱列表的响应。
type InboxOutput struct {
	Body InboxDTO
}

// GetInbox 读取收件箱左侧的列表和数量。
type GetInbox struct {
	db *bun.DB
}

// NewGetInbox 返回读取收件箱列表的动作。
func NewGetInbox(db *bun.DB) *GetInbox { return &GetInbox{db: db} }

// summaryRow 是一组会话的统计，列名与 summaryColumns 一致。
type summaryRow struct {
	Count   int        `bun:"count"`
	Waiting int        `bun:"waiting"`
	Oldest  *time.Time `bun:"oldest"`
}

// summary 返回 r 对应的 Summary。
func (r summaryRow) summary() Summary {
	return Summary{Count: r.Count, Waiting: r.Waiting, OldestWaitingSince: r.Oldest}
}

// viewOnlyOpen 报告已保存视图是否仅筛选处于 open 状态的会话。
func viewOnlyOpen(f Filter) bool {
	if len(f.Statuses) == 0 {
		return false
	}
	for _, s := range f.Statuses {
		if s != Open {
			return false
		}
	}
	return true
}

// Handle 返回 sub 在他所在工作区中的收件箱列表：我的、@我、未指派、全部、团队收件箱与已保存视图，各自的数量和积压情况。
// 数量只算 sub 能看到的服务型会话（visible）；内置列表与仅看 open 的视图合并在主聚合中走 open 索引，
// 包含非 open 状态的视图按所需状态并集在扩展聚合中统计，避免默认扫描全量已关闭历史会话。
func (a *GetInbox) Handle(ctx context.Context, sub identity.Subject) (*InboxDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}

	viewGroup := NewViews(a.db)
	views, err := viewGroup.List(ctx, sub, "")
	if err != nil {
		return nil, err
	}
	var attrDefs map[string]attribute.Definition
	for i := range views {
		if len(views[i].Config.Attributes) > 0 {
			attrDefs, err = loadConversationAttrDefs(ctx, a.db, sub.Tenant)
			if err != nil {
				return nil, err
			}
			break
		}
	}

	type metric struct {
		countExpr string
		countArgs []any
		openExpr  string
		openArgs  []any
		targetIdx int // 0..3 为内置列表，4+ 为对应 views[targetIdx-4]
	}

	openMetrics := make([]metric, 0, 4+len(views))
	addOpenBuiltin := func(targetIdx int, cond string, args ...any) {
		expr := "1=1"
		if cond != "" {
			expr = cond
		}
		openMetrics = append(openMetrics, metric{countExpr: expr, countArgs: args, openExpr: expr, openArgs: args, targetIdx: targetIdx})
	}
	addOpenBuiltin(0, "conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, sub.ID)
	addOpenBuiltin(1, "conversation.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)", sub.Tenant, sub.ID)
	addOpenBuiltin(2, "conversation.team_id IS NULL AND conversation.assignee_type = ''")
	addOpenBuiltin(3, "")

	extMetrics := make([]metric, 0, len(views))
	extUnbounded := false
	statusSeen := map[Status]bool{}
	for i := range views {
		pred, args := buildFilterPredicate(sub, views[i].Config.Filter, attrDefs)
		if viewOnlyOpen(views[i].Config.Filter) {
			openMetrics = append(openMetrics, metric{
				countExpr: pred,
				countArgs: args,
				openExpr:  pred,
				openArgs:  args,
				targetIdx: 4 + i,
			})
			continue
		}
		openArgs := make([]any, 0, len(args)+1)
		openArgs = append(openArgs, args...)
		openArgs = append(openArgs, Open)
		extMetrics = append(extMetrics, metric{
			countExpr: pred,
			countArgs: args,
			openExpr:  "(" + pred + " AND conversation.status = ?)",
			openArgs:  openArgs,
			targetIdx: 4 + i,
		})
		if len(views[i].Config.Filter.Statuses) == 0 {
			extUnbounded = true
		} else {
			for _, st := range views[i].Config.Filter.Statuses {
				statusSeen[st] = true
			}
		}
	}

	summaries := make([]Summary, 4+len(views))
	runAgg := func(items []metric, statuses []Status) error {
		if len(items) == 0 {
			return nil
		}
		agg := a.db.NewSelect().TableExpr("conversations AS conversation").
			Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.spam_at IS NULL", sub.Tenant, Service)
		if len(statuses) == 1 {
			agg = agg.Where("conversation.status = ?", statuses[0])
		} else if len(statuses) > 1 {
			agg = agg.Where("conversation.status IN (?)", bun.In(statuses))
		}
		agg = visible(agg, sub)

		counts := make([]int, len(items))
		waitings := make([]int, len(items))
		oldests := make([]sql.NullTime, len(items))
		dest := make([]any, 0, len(items)*3)
		for idx, m := range items {
			agg = agg.
				ColumnExpr("count(*) FILTER (WHERE "+m.countExpr+")", m.countArgs...).
				ColumnExpr("count(*) FILTER (WHERE "+m.openExpr+" AND conversation.waiting_since IS NOT NULL)", m.openArgs...).
				ColumnExpr("min(conversation.waiting_since) FILTER (WHERE "+m.openExpr+")", m.openArgs...)
			dest = append(dest, &counts[idx], &waitings[idx], &oldests[idx])
		}
		if err := agg.Scan(ctx, dest...); err != nil {
			return errors.Wrap(err, "count inbox")
		}
		for idx, m := range items {
			var oldest *time.Time
			if oldests[idx].Valid {
				t := oldests[idx].Time
				oldest = &t
			}
			summaries[m.targetIdx] = Summary{Count: counts[idx], Waiting: waitings[idx], OldestWaitingSince: oldest}
		}
		return nil
	}

	// 1. 主聚合固定限定 status = open，仅扫描打开状态的服务型会话
	if err := runAgg(openMetrics, []Status{Open}); err != nil {
		return nil, err
	}
	// 2. 扩展聚合仅在存在不限于 open 的视图时运行，并尽量按状态并集缩小扫描范围
	if len(extMetrics) > 0 {
		var statusUnion []Status
		if !extUnbounded {
			for st := range statusSeen {
				statusUnion = append(statusUnion, st)
			}
		}
		if err := runAgg(extMetrics, statusUnion); err != nil {
			return nil, err
		}
	}

	var out InboxDTO
	out.Mine = summaries[0]
	out.Mentions = summaries[1]
	out.Unassigned = summaries[2]
	out.All = summaries[3]

	out.Views = make([]ViewSummary, len(views))
	for i, v := range views {
		dto := viewToDTO(viewGroup, sub, v)
		out.Views[i] = ViewSummary{
			ID:       dto.ID,
			Scope:    dto.Scope,
			Name:     dto.Name,
			Config:   dto.Config,
			Position: dto.Position,
			Editable: dto.Editable,
			Summary:  summaries[4+i],
		}
	}

	// @我 的数量是还有未读的 @ 的服务型会话，不论状态
	err = a.db.NewRaw(`SELECT count(DISTINCT m.conversation_id) FROM conversation_mentions m
		JOIN conversations c ON c.id = m.conversation_id AND c.kind = ? AND c.spam_at IS NULL
		LEFT JOIN conversation_reads r ON r.conversation_id = m.conversation_id AND r.member_id = m.member_id
		WHERE m.tenant_id = ? AND m.member_id = ? AND m.seq > coalesce(r.last_read_seq, 0)`, Service, sub.Tenant, sub.ID).Scan(ctx, &out.Mentions.Count)
	if err != nil {
		return nil, errors.Wrap(err, "count unread mentions")
	}

	// 工单分组：单次聚合统计当前成员可见的未解决工单数
	ticketAgg := a.db.NewSelect().TableExpr("conversations AS conversation").
		ColumnExpr("count(*) FILTER (WHERE conversation.assignee_type = ? AND conversation.assignee_id = ?)", MemberActor, sub.ID).
		ColumnExpr("count(*) FILTER (WHERE conversation.team_id IS NULL AND conversation.assignee_type = '')").
		ColumnExpr("count(*)").
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.status <> ?", sub.Tenant, Ticket, TicketResolved)
	ticketAgg = visible(ticketAgg, sub)
	if err := ticketAgg.Scan(ctx, &out.Tickets.Mine, &out.Tickets.Unassigned, &out.Tickets.All); err != nil {
		return nil, errors.Wrap(err, "count ticket inbox")
	}

	// 团队收件箱：只列未停用的；我所在的；可见范围是全部会话时是全部
	teams := a.db.NewSelect().Table("teams").Column("id", "name").Where("tenant_id = ? AND disabled_at IS NULL", sub.Tenant).OrderExpr("name, id")
	if auth.ScopeOf(sub) != auth.ScopeAll {
		teams = teams.Where("id IN (SELECT team_id FROM team_members WHERE member_id = ?)", sub.ID)
	}
	if err := teams.Scan(ctx, &out.Teams); err != nil {
		return nil, errors.Wrap(err, "list inbox teams")
	}
	var byTeam []struct {
		TeamID string `bun:"team_id"`
		summaryRow
	}
	teamStatsQ := a.db.NewSelect().TableExpr("conversations AS conversation").
		ColumnExpr("conversation.team_id").
		ColumnExpr("count(*) AS count").
		ColumnExpr("count(*) FILTER (WHERE conversation.waiting_since IS NOT NULL) AS waiting").
		ColumnExpr("min(conversation.waiting_since) AS oldest").
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.status = ? AND conversation.spam_at IS NULL AND conversation.team_id IS NOT NULL", sub.Tenant, Service, Open).
		GroupExpr("conversation.team_id")
	teamStatsQ = visible(teamStatsQ, sub)
	if err := teamStatsQ.Scan(ctx, &byTeam); err != nil {
		return nil, errors.Wrap(err, "count team inboxes")
	}
	stats := make(map[string]Summary, len(byTeam))
	for _, r := range byTeam {
		stats[r.TeamID] = r.summary()
	}
	if out.Teams == nil {
		out.Teams = []TeamSummary{}
	}
	for i := range out.Teams {
		out.Teams[i].Summary = stats[out.Teams[i].ID]
	}

	spamQ := a.db.NewSelect().TableExpr("conversations AS conversation").
		ColumnExpr("count(*)").
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.spam_at IS NOT NULL", sub.Tenant, Service)
	spamQ = visible(spamQ, sub)
	if err := spamQ.Scan(ctx, &out.Spam); err != nil {
		return nil, errors.Wrap(err, "count spam inbox")
	}

	approvalQ := a.db.NewSelect().TableExpr("conversations AS conversation").
		ColumnExpr("count(*)").
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.status = ? AND conversation.spam_at IS NULL AND conversation.handling_mode = ?",
			sub.Tenant, Service, Open, HandlingWaitingApproval)
	approvalQ = visible(approvalQ, sub)
	if err := approvalQ.Scan(ctx, &out.WaitingApproval); err != nil {
		return nil, errors.Wrap(err, "count waiting approval inbox")
	}
	return &out, nil
}

// HTTP 处理 GET /t/{slug}/api/inbox。
func (a *GetInbox) HTTP(ctx context.Context, _ *struct{}) (*InboxOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &InboxOutput{Body: *dto}, nil
}
