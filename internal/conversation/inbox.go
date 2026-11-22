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

// InboxDTO 是收件箱左侧的全部列表和它们的数量，只算当前成员能看到的会话。
type InboxDTO struct {
	Mine       Summary       `json:"mine" doc:"我的"`
	Mentions   Summary       `json:"mentions" doc:"@我"`
	Unassigned Summary       `json:"unassigned" doc:"未指派"`
	All        Summary       `json:"all" doc:"全部"`
	Teams      []TeamSummary `json:"teams" nullable:"false" doc:"团队收件箱：我所在的；可见范围是全部会话的成员是全部团队收件箱，按名称排列"`
	Views      []ViewSummary `json:"views" nullable:"false" doc:"已保存视图：共享在前、个人在后，各带实时会话数量与积压情况"`
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

// Handle 返回 sub 在他所在工作区中的收件箱列表：我的、@我、未指派、全部、团队收件箱与已保存视图，各自的数量和积压情况。
// 数量只算 sub 能看到的服务型会话（visible）；内置列表与全部可见视图合并在单条聚合 SQL 中一次扫描算出。
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

	// 单次聚合扫描：在 sub 可见的服务型会话子集上，通过 FILTER (WHERE ...) 一次算出 4 个内置列表与全部可见视图的计数。
	type metric struct {
		countExpr string
		countArgs []any
		openExpr  string
		openArgs  []any
	}
	metrics := make([]metric, 0, 4+len(views))
	addOpen := func(cond string, args ...any) {
		expr := "conversation.status = ?"
		a := []any{Open}
		if cond != "" {
			expr = "(" + expr + " AND " + cond + ")"
			a = append(a, args...)
		}
		metrics = append(metrics, metric{countExpr: expr, countArgs: a, openExpr: expr, openArgs: a})
	}
	addOpen("conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, sub.ID)
	addOpen("conversation.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)", sub.Tenant, sub.ID)
	addOpen("conversation.team_id IS NULL AND conversation.assignee_type = ''")
	addOpen("")

	for i := range views {
		pred, args := buildFilterPredicate(sub, views[i].Config.Filter, attrDefs)
		openArgs := make([]any, 0, len(args)+1)
		openArgs = append(openArgs, args...)
		openArgs = append(openArgs, Open)
		metrics = append(metrics, metric{
			countExpr: pred,
			countArgs: args,
			openExpr:  "(" + pred + " AND conversation.status = ?)",
			openArgs:  openArgs,
		})
	}

	agg := a.db.NewSelect().TableExpr("conversations AS conversation").
		Where("conversation.tenant_id = ? AND conversation.kind = ?", sub.Tenant, Service)
	agg = visible(agg, sub)

	counts := make([]int, len(metrics))
	waitings := make([]int, len(metrics))
	oldests := make([]sql.NullTime, len(metrics))
	dest := make([]any, 0, len(metrics)*3)
	for i, m := range metrics {
		agg = agg.
			ColumnExpr("count(*) FILTER (WHERE "+m.countExpr+")", m.countArgs...).
			ColumnExpr("count(*) FILTER (WHERE "+m.openExpr+" AND conversation.waiting_since IS NOT NULL)", m.openArgs...).
			ColumnExpr("min(conversation.waiting_since) FILTER (WHERE "+m.openExpr+")", m.openArgs...)
		dest = append(dest, &counts[i], &waitings[i], &oldests[i])
	}
	if err := agg.Scan(ctx, dest...); err != nil {
		return nil, errors.Wrap(err, "count inbox")
	}

	summaryAt := func(i int) Summary {
		var oldest *time.Time
		if oldests[i].Valid {
			t := oldests[i].Time
			oldest = &t
		}
		return Summary{Count: counts[i], Waiting: waitings[i], OldestWaitingSince: oldest}
	}

	var out InboxDTO
	out.Mine = summaryAt(0)
	out.Mentions = summaryAt(1)
	out.Unassigned = summaryAt(2)
	out.All = summaryAt(3)

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
			Summary:  summaryAt(4 + i),
		}
	}

	// @我 的数量是还有未读的 @ 的会话，不论状态
	err = a.db.NewRaw(`SELECT count(DISTINCT m.conversation_id) FROM conversation_mentions m
		LEFT JOIN conversation_reads r ON r.conversation_id = m.conversation_id AND r.member_id = m.member_id
		WHERE m.tenant_id = ? AND m.member_id = ? AND m.seq > coalesce(r.last_read_seq, 0)`, sub.Tenant, sub.ID).Scan(ctx, &out.Mentions.Count)
	if err != nil {
		return nil, errors.Wrap(err, "count unread mentions")
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
		Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.status = ? AND conversation.team_id IS NOT NULL", sub.Tenant, Service, Open).
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
