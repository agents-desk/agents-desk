package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Summary 是收件箱中一个列表的数量和积压情况。
type Summary struct {
	Count              int        `json:"count" doc:"左侧显示的数量：打开的会话数；@我 是还有未读的 @ 的会话数"`
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

// Handle 返回 sub 在他所在工作区中的收件箱列表：我的、@我、未指派、全部和团队收件箱，各自的数量和积压情况。
// 数量只算 sub 能看到的服务型会话（visible）。
func (a *GetInbox) Handle(ctx context.Context, sub identity.Subject) (*InboxDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	// open 返回 sub 能看到的、打开的服务型会话的查询，cond 是列表自己的条件
	open := func(cond string, args ...any) *bun.SelectQuery {
		q := a.db.NewSelect().TableExpr("conversations AS conversation").
			ColumnExpr("count(*) AS count").
			ColumnExpr("count(*) FILTER (WHERE conversation.waiting_since IS NOT NULL) AS waiting").
			ColumnExpr("min(conversation.waiting_since) AS oldest").
			Where("conversation.tenant_id = ? AND conversation.kind = ? AND conversation.status = ?", sub.Tenant, Service, Open)
		if cond != "" {
			q = q.Where(cond, args...)
		}
		return visible(q, sub)
	}
	mentioned := "conversation.id IN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ?)"
	var out InboxDTO
	for _, l := range []struct {
		to   *Summary
		cond string
		args []any
	}{
		{&out.Mine, "conversation.assignee_type = ? AND conversation.assignee_id = ?", []any{MemberActor, sub.ID}},
		{&out.Mentions, mentioned, []any{sub.Tenant, sub.ID}},
		{&out.Unassigned, "conversation.team_id IS NULL AND conversation.assignee_type = ''", nil},
		{&out.All, "", nil},
	} {
		var r summaryRow
		if err := open(l.cond, l.args...).Scan(ctx, &r); err != nil {
			return nil, errors.Wrap(err, "count inbox")
		}
		*l.to = r.summary()
	}

	// @我 的数量是还有未读的 @ 的会话，不论状态
	err := a.db.NewRaw(`SELECT count(DISTINCT m.conversation_id) FROM conversation_mentions m
		LEFT JOIN conversation_reads r ON r.conversation_id = m.conversation_id AND r.member_id = m.member_id
		WHERE m.tenant_id = ? AND m.member_id = ? AND m.seq > coalesce(r.last_read_seq, 0)`, sub.Tenant, sub.ID).Scan(ctx, &out.Mentions.Count)
	if err != nil {
		return nil, errors.Wrap(err, "count unread mentions")
	}

	// 团队收件箱：我所在的；可见范围是全部会话时是全部
	teams := a.db.NewSelect().Table("teams").Column("id", "name").Where("tenant_id = ?", sub.Tenant).OrderExpr("name, id")
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
	err = open("conversation.team_id IS NOT NULL").ColumnExpr("conversation.team_id").GroupExpr("conversation.team_id").Scan(ctx, &byTeam)
	if err != nil {
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
