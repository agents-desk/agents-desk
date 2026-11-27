package report

import (
	"context"
	"slices"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/account"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// MembersSummary 是成员绩效报表的团队整体汇总。
type MembersSummary struct {
	ActiveMembers    int64    `json:"active_members" doc:"统计期内有客服、工单或商务产出的成员人数"`
	ClosedCount      int64    `json:"closed_count" doc:"成员关闭的服务轮次总数"`
	RepliesSent      int64    `json:"replies_sent" doc:"成员发出的客服回复总数"`
	TicketsResolved  int64    `json:"tickets_resolved" doc:"成员解决的工单总数"`
	BusinessMessages int64    `json:"business_messages" doc:"成员发出的商务沟通消息总数"`
	FirstReplyMedian *float64 `json:"first_reply_median,omitempty" doc:"成员首次回复时长中位数（秒）"`
	CSAT             *float64 `json:"csat,omitempty" doc:"成员负责会话的满意度百分比（4–5 星占比）"`
}

// MemberMetrics 是单名成员在统计期内的跨业务域绩效指标。
type MemberMetrics struct {
	MemberID          string   `json:"member_id"`
	Name              string   `json:"name"`
	AvatarURL         string   `json:"avatar_url"`
	OnlineStatus      string   `json:"online_status" doc:"当前在线状态：online、away；已移出工作区时为空"`
	Removed           bool     `json:"removed" doc:"成员是否已移出工作区（仍保留统计期内的历史数据）"`
	ClosedCount       int64    `json:"closed_count" doc:"统计期内负责并关闭的服务轮次数"`
	RepliesSent       int64    `json:"replies_sent" doc:"统计期内发出的客服回复数"`
	OpenAssigned      int64    `json:"open_assigned" doc:"当前负责且未关闭的服务型会话数（实时快照）"`
	FirstReplySamples int64    `json:"first_reply_samples" doc:"统计期内由该成员完成首次回复的轮次数"`
	FirstReplyMedian  *float64 `json:"first_reply_median,omitempty" doc:"首次回复时长中位数（秒，按实际首次回复人归因）"`
	FirstReplyAvg     *float64 `json:"first_reply_avg,omitempty" doc:"首次回复时长均值（秒）"`
	ResolutionSamples int64    `json:"resolution_samples" doc:"统计期内关闭轮次数"`
	ResolutionMedian  *float64 `json:"resolution_median,omitempty" doc:"解决时长中位数（秒）"`
	ResolutionAvg     *float64 `json:"resolution_avg,omitempty" doc:"解决时长均值（秒）"`
	RatedCount        int64    `json:"rated_count" doc:"收到的客户评价数"`
	PositiveCount     int64    `json:"positive_count" doc:"好评数（4–5 星）"`
	CSAT              *float64 `json:"csat,omitempty" doc:"满意度百分比（0–100）"`
	AvgScore          *float64 `json:"avg_score,omitempty" doc:"平均评分（1–5）"`
	TicketsResolved   int64    `json:"tickets_resolved" doc:"统计期内负责并解决的工单数"`
	TicketsOpen       int64    `json:"tickets_open" doc:"当前负责且未解决的工单数（实时快照）"`
	BusinessChats     int64    `json:"business_chats" doc:"统计期内活跃的商务沟通会话数"`
	BusinessMessages  int64    `json:"business_messages" doc:"统计期内在商务沟通中发出的消息数"`
}

// MembersReport 是成员绩效报表的完整响应。
type MembersReport struct {
	Period  PeriodMeta      `json:"period"`
	Summary MembersSummary  `json:"summary"`
	Items   []MemberMetrics `json:"items" nullable:"false"`
}

// MembersOutput 是成员绩效报表的 HTTP 响应。
type MembersOutput struct {
	Body MembersReport
}

// Members 汇总工作区各成员的服务量、回复量、首响时长、解决时长与满意度。
type Members struct {
	db    *bun.DB
	roles *authz.Store
	files *filesystem.Service
}

// NewMembers 返回成员绩效报表动作。
func NewMembers(db *bun.DB, roles *authz.Store, files *filesystem.Service) *Members {
	return &Members{db: db, roles: roles, files: files}
}

// Handle 返回 sub 所在工作区按筛选条件 f 统计的成员绩效报表。
func (a *Members) Handle(ctx context.Context, sub identity.Subject, f FilterParams) (*MembersReport, error) {
	w, err := resolveWindow(ctx, a.db, sub, f)
	if err != nil {
		return nil, err
	}

	frtExpr := "GREATEST(EXTRACT(EPOCH FROM (r.first_reply_at - r.opened_at)), 0)"
	resExpr := "GREATEST(EXTRACT(EPOCH FROM (r.closed_at - r.opened_at)), 0)"

	// 1. 当前工作区成员集合（用于区分在职与已移出成员；若指定 team_id，仅默认展示该团队成员及有历史记录的成员）
	currentSet := map[string]bool{}
	if a.roles != nil {
		assigned, err := a.roles.Members(ctx, sub.Tenant)
		if err != nil {
			return nil, errors.Wrap(err, "list tenant members for report")
		}
		for _, m := range assigned {
			if m.Subject.Type == member.Type {
				currentSet[m.Subject.ID] = true
			}
		}
	}

	baseIDs := map[string]bool{}
	if f.TeamID != "" {
		var ids []string
		if err := a.db.NewSelect().TableExpr("team_members tm").
			Join("JOIN teams t ON t.id = tm.team_id").
			ColumnExpr("tm.member_id::text").
			Where("t.tenant_id = ? AND tm.team_id = ?", sub.Tenant, f.TeamID).Scan(ctx, &ids); err != nil {
			return nil, errors.Wrap(err, "list team members for report")
		}
		for _, id := range ids {
			baseIDs[id] = true
		}
	} else {
		for id := range currentSet {
			baseIDs[id] = true
		}
	}

	byID := map[string]*MemberMetrics{}
	ensure := func(id string) *MemberMetrics {
		m, ok := byID[id]
		if !ok {
			m = &MemberMetrics{MemberID: id, Removed: !currentSet[id]}
			byID[id] = m
		}
		return m
	}
	for id := range baseIDs {
		ensure(id)
	}

	// 2. 按实际首次回复人（first_reply_by_id）统计 FRT
	type durRow struct {
		MemberID string   `bun:"member_id"`
		Samples  int64    `bun:"samples"`
		Median   *float64 `bun:"median"`
		Avg      *float64 `bun:"avg"`
	}
	var frtRows []durRow
	qFRT := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("r.first_reply_by_id AS member_id, count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") AS median").
		ColumnExpr("avg("+frtExpr+") AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.first_reply_by_type = ? AND r.first_reply_by_id <> '' AND r.first_reply_at >= ? AND r.first_reply_at < ?",
			sub.Tenant, conversation.Service, conversation.MemberActor, w.Start, w.End).
		GroupExpr("r.first_reply_by_id")
	applyRoundFilters(qFRT, f)
	if err := qFRT.Scan(ctx, &frtRows); err != nil {
		return nil, errors.Wrap(err, "report member frt")
	}
	for _, r := range frtRows {
		m := ensure(r.MemberID)
		m.FirstReplySamples = r.Samples
		m.FirstReplyMedian = roundPtr(r.Median)
		m.FirstReplyAvg = roundPtr(r.Avg)
	}

	// 3. 按关闭时负责人（assignee_id）统计关闭数与解决时长
	var resRows []durRow
	qRes := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("r.assignee_id AS member_id, count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+resExpr+") AS median").
		ColumnExpr("avg("+resExpr+") AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.assignee_type = ? AND r.assignee_id <> '' AND r.closed_at >= ? AND r.closed_at < ?",
			sub.Tenant, conversation.Service, conversation.MemberActor, w.Start, w.End).
		GroupExpr("r.assignee_id")
	applyRoundFilters(qRes, f)
	if err := qRes.Scan(ctx, &resRows); err != nil {
		return nil, errors.Wrap(err, "report member resolution")
	}
	for _, r := range resRows {
		m := ensure(r.MemberID)
		m.ClosedCount = r.Samples
		m.ResolutionSamples = r.Samples
		m.ResolutionMedian = roundPtr(r.Median)
		m.ResolutionAvg = roundPtr(r.Avg)
	}

	// 4. 按评价对应轮次的负责人统计满意度
	type rateRow struct {
		MemberID string   `bun:"member_id"`
		Rated    int64    `bun:"rated"`
		Positive int64    `bun:"pos"`
		Avg      *float64 `bun:"avg"`
	}
	var rateRows []rateRow
	qRate := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("r.assignee_id AS member_id, count(*) AS rated, count(*) FILTER (WHERE r.rating >= 4) AS pos, avg(r.rating) AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.assignee_type = ? AND r.assignee_id <> '' AND r.rating IS NOT NULL AND r.rated_at >= ? AND r.rated_at < ?",
			sub.Tenant, conversation.Service, conversation.MemberActor, w.Start, w.End).
		GroupExpr("r.assignee_id")
	applyRoundFilters(qRate, f)
	if err := qRate.Scan(ctx, &rateRows); err != nil {
		return nil, errors.Wrap(err, "report member satisfaction")
	}
	var totalRated, totalPositive int64
	for _, r := range rateRows {
		m := ensure(r.MemberID)
		m.RatedCount = r.Rated
		m.PositiveCount = r.Positive
		m.CSAT = pct(r.Positive, r.Rated)
		m.AvgScore = roundPtr(r.Avg)
		totalRated += r.Rated
		totalPositive += r.Positive
	}

	// 5. 成员发出的客户可见回复数
	type countRow struct {
		MemberID string `bun:"member_id"`
		Count    int64  `bun:"cnt"`
	}
	var replyRows []countRow
	qReplies := a.db.NewSelect().TableExpr("timeline_items it").
		Join("JOIN conversations c ON c.id = it.conversation_id").
		ColumnExpr("it.actor_id AS member_id, count(*) AS cnt").
		Where("it.tenant_id = ? AND c.kind = ? AND it.kind = ? AND it.actor_type = ? AND it.visibility = ? AND it.actor_id <> '' AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Service, conversation.Message, conversation.MemberActor, conversation.Everyone, w.Start, w.End).
		GroupExpr("it.actor_id")
	applyConvFilters(qReplies, f)
	if err := qReplies.Scan(ctx, &replyRows); err != nil {
		return nil, errors.Wrap(err, "report member replies")
	}
	for _, r := range replyRows {
		ensure(r.MemberID).RepliesSent = r.Count
	}

	// 6. 当前负责且未关闭会话数（实时快照）
	var openRows []countRow
	qOpen := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("c.assignee_id AS member_id, count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND c.status <> ? AND c.assignee_type = ? AND c.assignee_id <> ''",
			sub.Tenant, conversation.Service, conversation.Closed, conversation.MemberActor).
		GroupExpr("c.assignee_id")
	applyConvFilters(qOpen, f)
	if err := qOpen.Scan(ctx, &openRows); err != nil {
		return nil, errors.Wrap(err, "report member open assigned")
	}
	for _, r := range openRows {
		ensure(r.MemberID).OpenAssigned = r.Count
	}

	// 6b. 成员负责的工单解决数与当前未解决数（未指定渠道筛选时计入；若指定了 team_id 按工单团队筛选）
	if f.ChannelID == "" {
		type tkRow struct {
			MemberID string `bun:"member_id"`
			Resolved int64  `bun:"resolved"`
			OpenNow  int64  `bun:"open_now"`
		}
		var tkRows []tkRow
		qTk := a.db.NewSelect().TableExpr("conversations c").
			Join("JOIN tickets t ON t.conversation_id = c.id").
			ColumnExpr("c.assignee_id AS member_id").
			ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS resolved", w.Start, w.End).
			ColumnExpr("count(*) FILTER (WHERE c.status <> ?) AS open_now", conversation.Status(conversation.TicketResolved)).
			Where("c.tenant_id = ? AND c.kind = ? AND c.assignee_type = ? AND c.assignee_id <> ''",
				sub.Tenant, conversation.Ticket, conversation.MemberActor).
			Where("(t.resolved_at >= ? AND t.resolved_at < ?) OR c.status <> ?",
				w.Start, w.End, conversation.Status(conversation.TicketResolved)).
			GroupExpr("c.assignee_id")
		if f.TeamID != "" {
			qTk.Where("c.team_id = ?", f.TeamID)
		}
		if err := qTk.Scan(ctx, &tkRows); err != nil {
			return nil, errors.Wrap(err, "report member tickets")
		}
		for _, r := range tkRows {
			m := ensure(r.MemberID)
			m.TicketsResolved = r.Resolved
			m.TicketsOpen = r.OpenNow
		}

		// 6c. 成员发出的商务沟通消息数与活跃商务会话数（未指定 team_id 与 channel_id 时计入）
		if f.TeamID == "" {
			type bizRow struct {
				MemberID string `bun:"member_id"`
				Chats    int64  `bun:"chats"`
				Messages int64  `bun:"msgs"`
			}
			var bizRows []bizRow
			if err := a.db.NewSelect().TableExpr("timeline_items it").
				Join("JOIN conversations c ON c.id = it.conversation_id").
				Join("JOIN chats ch ON ch.conversation_id = c.id").
				ColumnExpr("it.actor_id AS member_id, count(DISTINCT it.conversation_id) AS chats, count(*) AS msgs").
				Where("it.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND it.kind = ? AND it.actor_type = ? AND it.actor_id <> '' AND it.created_at >= ? AND it.created_at < ?",
					sub.Tenant, conversation.Chat, conversation.ChatExternal, conversation.Message, conversation.MemberActor, w.Start, w.End).
				GroupExpr("it.actor_id").
				Scan(ctx, &bizRows); err != nil {
				return nil, errors.Wrap(err, "report member business")
			}
			for _, r := range bizRows {
				m := ensure(r.MemberID)
				m.BusinessChats = r.Chats
				m.BusinessMessages = r.Messages
			}
		}
	}

	// 7. 补全成员姓名、头像与在线状态
	allIDs := make([]string, 0, len(byID))
	for id := range byID {
		allIDs = append(allIDs, id)
	}
	if len(allIDs) > 0 {
		type profileRow struct {
			ID           string     `bun:"id"`
			Name         string     `bun:"name"`
			AvatarFileID *uuid.UUID `bun:"avatar_file_id"`
		}
		var profiles []profileRow
		if err := a.db.NewSelect().Table("members").
			ColumnExpr("id::text AS id, name, avatar_file_id").
			Where("id IN (?)", bun.List(allIDs)).
			Scan(ctx, &profiles); err != nil {
			return nil, errors.Wrap(err, "load member profiles for report")
		}
		for _, p := range profiles {
			m := byID[p.ID]
			if m == nil {
				continue
			}
			m.Name = p.Name
			if a.files != nil {
				url, err := account.AvatarURL(ctx, a.files, p.AvatarFileID)
				if err != nil {
					return nil, err
				}
				m.AvatarURL = url
			}
		}

		statusMap, err := member.StatusesOf(ctx, a.db, sub.Tenant, allIDs)
		if err != nil {
			return nil, err
		}
		for id, m := range byID {
			if !m.Removed {
				m.OnlineStatus = string(statusMap[id])
			}
		}
	}

	// 8. 整体首次回复中位数（限定成员首响）与汇总
	var overallFRT *float64
	qOverallFRT := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+")").
		Where("r.tenant_id = ? AND c.kind = ? AND r.first_reply_by_type = ? AND r.first_reply_by_id <> '' AND r.first_reply_at >= ? AND r.first_reply_at < ?",
			sub.Tenant, conversation.Service, conversation.MemberActor, w.Start, w.End)
	applyRoundFilters(qOverallFRT, f)
	if err := qOverallFRT.Scan(ctx, &overallFRT); err != nil {
		return nil, errors.Wrap(err, "report overall member frt")
	}

	items := make([]MemberMetrics, 0, len(byID))
	var sum MembersSummary
	sum.FirstReplyMedian = roundPtr(overallFRT)
	sum.CSAT = pct(totalPositive, totalRated)
	for _, m := range byID {
		if m.ClosedCount > 0 || m.RepliesSent > 0 || m.FirstReplySamples > 0 || m.TicketsResolved > 0 || m.BusinessMessages > 0 {
			sum.ActiveMembers++
		}
		sum.ClosedCount += m.ClosedCount
		sum.RepliesSent += m.RepliesSent
		sum.TicketsResolved += m.TicketsResolved
		sum.BusinessMessages += m.BusinessMessages
		items = append(items, *m)
	}
	slices.SortFunc(items, func(x, y MemberMetrics) int {
		if x.ClosedCount != y.ClosedCount {
			if x.ClosedCount > y.ClosedCount {
				return -1
			}
			return 1
		}
		if x.RepliesSent != y.RepliesSent {
			if x.RepliesSent > y.RepliesSent {
				return -1
			}
			return 1
		}
		if x.Name < y.Name {
			return -1
		}
		if x.Name > y.Name {
			return 1
		}
		return 0
	})

	return &MembersReport{
		Period:  PeriodMeta(w.Meta()),
		Summary: sum,
		Items:   items,
	}, nil
}

// HTTP 处理 GET /t/{slug}/api/reports/members。
func (a *Members) HTTP(ctx context.Context, in *FilterParams) (*MembersOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &MembersOutput{Body: *res}, nil
}
