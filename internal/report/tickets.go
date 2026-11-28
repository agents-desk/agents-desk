package report

import (
	"context"
	"slices"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/fileapi"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tickettype"
)

// TicketFilterParams 是工单报表的查询参数。
type TicketFilterParams struct {
	period.Params
	TeamID     string `query:"team_id" format:"uuid" doc:"按团队收件箱筛选（含已停用团队）"`
	TypeID     string `query:"type_id" format:"uuid" doc:"按工单类型筛选（含已停用类型）"`
	Visibility string `query:"visibility" enum:"all,members" doc:"按可见范围筛选：all（客户可见）或 members（仅成员可见）"`
}

// TicketsSummary 是工单报表的核心汇总指标与当前积压快照。
type TicketsSummary struct {
	Created             int64    `json:"created" doc:"统计期内新建的工单数"`
	PrevCreated         int64    `json:"prev_created" doc:"上一对比期新建的工单数"`
	Resolved            int64    `json:"resolved" doc:"统计期内已解决的工单数"`
	PrevResolved        int64    `json:"prev_resolved" doc:"上一对比期已解决的工单数"`
	ResolutionMedianSec *float64 `json:"resolution_median_seconds,omitempty" doc:"解决时长中位数（秒）"`
	PrevResolutionSec   *float64 `json:"prev_resolution_median_seconds,omitempty" doc:"上一对比期解决时长中位数（秒）"`
	ResolutionAvgSec    *float64 `json:"resolution_avg_seconds,omitempty" doc:"解决时长均值（秒）"`
	LinkedContacts      int64    `json:"linked_contacts" doc:"统计期内新建工单关联的受影响对象总数"`
	SubmittedNow        int64    `json:"submitted_now" doc:"当前已提交状态的工单数（实时快照）"`
	InProgressNow       int64    `json:"in_progress_now" doc:"当前处理中状态的工单数（实时快照）"`
	WaitingNow          int64    `json:"waiting_now" doc:"当前等待客户状态的工单数（实时快照）"`
	UnassignedNow       int64    `json:"unassigned_now" doc:"当前未解决且无负责人的工单数（实时快照）"`
}

// TicketsPoint 是工单趋势图中的一个时间桶。
type TicketsPoint struct {
	Date     string `json:"date"`
	Created  int64  `json:"created"`
	Resolved int64  `json:"resolved"`
}

// TicketTypeVolume 是按工单类型拆分的工单量；ID 为空表示未选类型的通用工单。
type TicketTypeVolume struct {
	ID       *string `json:"id,omitempty"`
	Name     string  `json:"name"`
	Disabled bool    `json:"disabled"`
	Created  int64   `json:"created"`
	Resolved int64   `json:"resolved"`
	OpenNow  int64   `json:"open_now"`
}

// TicketPriorityVolume 是按优先级拆分的工单量。
type TicketPriorityVolume struct {
	Priority conversation.Priority `json:"priority"`
	Created  int64                 `json:"created"`
	OpenNow  int64                 `json:"open_now"`
}

// TicketVisibilityVolume 是按可见范围拆分的工单量。
type TicketVisibilityVolume struct {
	Visibility tickettype.Visibility `json:"visibility"`
	Created    int64                 `json:"created"`
	OpenNow    int64                 `json:"open_now"`
}

// TicketAssigneeMetrics 是按负责人拆分的工单处理绩效。
type TicketAssigneeMetrics struct {
	MemberID            string   `json:"member_id"`
	Name                string   `json:"name"`
	AvatarURL           string   `json:"avatar_url"`
	Removed             bool     `json:"removed"`
	Resolved            int64    `json:"resolved"`
	OpenNow             int64    `json:"open_now"`
	ResolutionMedianSec *float64 `json:"resolution_median_seconds,omitempty"`
}

// TicketsReport 是工单报表的完整响应。
type TicketsReport struct {
	Period       PeriodMeta               `json:"period"`
	Summary      TicketsSummary           `json:"summary"`
	Series       []TicketsPoint           `json:"series" nullable:"false"`
	ByType       []TicketTypeVolume       `json:"by_type" nullable:"false"`
	ByTeam       []TeamVolume             `json:"by_team" nullable:"false"`
	ByPriority   []TicketPriorityVolume   `json:"by_priority" nullable:"false"`
	ByVisibility []TicketVisibilityVolume `json:"by_visibility" nullable:"false"`
	ByAssignee   []TicketAssigneeMetrics  `json:"by_assignee" nullable:"false"`
}

// TicketsOutput 是工单报表的 HTTP 响应。
type TicketsOutput struct {
	Body TicketsReport
}

// Tickets 汇总工作区的工单量、积压、解决效率及多维分布。
type Tickets struct {
	db    *bun.DB
	roles *authz.Store
	files *filesystem.Service
}

// NewTickets 返回工单报表动作。
func NewTickets(db *bun.DB, roles *authz.Store, files *filesystem.Service) *Tickets {
	return &Tickets{db: db, roles: roles, files: files}
}

// applyTicketFilters 在包含 conversations c 与 tickets t 的查询上叠加未标垃圾条件及团队、类型与可见范围筛选。
func applyTicketFilters(q *bun.SelectQuery, f TicketFilterParams) {
	q.Where("c.spam_at IS NULL")
	if f.TeamID != "" {
		q.Where("c.team_id = ?", f.TeamID)
	}
	if f.TypeID != "" {
		q.Where("t.type_id = ?", f.TypeID)
	}
	if f.Visibility != "" {
		q.Where("t.visibility = ?", f.Visibility)
	}
}

// Handle 返回 sub 所在工作区按筛选条件 f 统计的工单报表。
func (a *Tickets) Handle(ctx context.Context, sub identity.Subject, f TicketFilterParams) (*TicketsReport, error) {
	if f.TeamID != "" {
		ok, err := a.db.NewSelect().Table("teams").Where("id = ? AND tenant_id = ?", f.TeamID, sub.Tenant).Exists(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "check ticket report team")
		}
		if !ok {
			return nil, team.ErrNotFound.New()
		}
	}
	if f.TypeID != "" {
		ok, err := a.db.NewSelect().Table("ticket_types").Where("id = ? AND tenant_id = ?", f.TypeID, sub.Tenant).Exists(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "check ticket report type")
		}
		if !ok {
			return nil, tickettype.ErrNotFound.New()
		}
	}
	w, err := resolvePeriodOnly(ctx, a.db, sub.Tenant, f.Params)
	if err != nil {
		return nil, err
	}

	var sum TicketsSummary
	resExpr := "GREATEST(EXTRACT(EPOCH FROM (t.resolved_at - c.created_at)), 0)"

	// 1. 周期汇总（新建、已解决、解决时长）与实时积压快照
	qSum := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS prev_created", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS resolved", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS prev_resolved", w.PrevStart, w.PrevEnd).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+resExpr+") FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS res_med", w.Start, w.End).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+resExpr+") FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS prev_res_med", w.PrevStart, w.PrevEnd).
		ColumnExpr("avg("+resExpr+") FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS res_avg", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.status = ?) AS sub_now", conversation.Status(conversation.TicketSubmitted)).
		ColumnExpr("count(*) FILTER (WHERE c.status = ?) AS prog_now", conversation.Status(conversation.TicketInProgress)).
		ColumnExpr("count(*) FILTER (WHERE c.status = ?) AS wait_now", conversation.Status(conversation.TicketWaiting)).
		ColumnExpr("count(*) FILTER (WHERE c.status <> ? AND c.assignee_id = '') AS unassigned_now", conversation.Status(conversation.TicketResolved)).
		Where("c.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Ticket)
	applyTicketFilters(qSum, f)
	if err := qSum.Scan(ctx,
		&sum.Created, &sum.PrevCreated, &sum.Resolved, &sum.PrevResolved,
		&sum.ResolutionMedianSec, &sum.PrevResolutionSec, &sum.ResolutionAvgSec,
		&sum.SubmittedNow, &sum.InProgressNow, &sum.WaitingNow, &sum.UnassignedNow,
	); err != nil {
		return nil, errors.Wrap(err, "report tickets summary")
	}
	sum.ResolutionMedianSec = roundPtr(sum.ResolutionMedianSec)
	sum.PrevResolutionSec = roundPtr(sum.PrevResolutionSec)
	sum.ResolutionAvgSec = roundPtr(sum.ResolutionAvgSec)

	// 2. 统计期内新建工单关联的受影响对象总数
	qLinked := a.db.NewSelect().TableExpr("ticket_targets tc").
		Join("JOIN conversations c ON c.id = tc.ticket_id").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr("count(*)").
		Where("tc.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", sub.Tenant, conversation.Ticket, w.Start, w.End)
	applyTicketFilters(qLinked, f)
	if err := qLinked.Scan(ctx, &sum.LinkedContacts); err != nil {
		return nil, errors.Wrap(err, "report tickets linked contacts")
	}

	// 3. 趋势序列（新建与已解决）
	pointMap := map[string]TicketsPoint{}
	type bucketRow struct {
		Bucket string `bun:"bucket"`
		Count  int64  `bun:"cnt"`
	}
	var cRows []bucketRow
	qCreatedSeries := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr(w.BucketExpr("c.created_at")+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", sub.Tenant, conversation.Ticket, w.Start, w.End).
		GroupExpr("1")
	applyTicketFilters(qCreatedSeries, f)
	if err := qCreatedSeries.Scan(ctx, &cRows); err != nil {
		return nil, errors.Wrap(err, "report tickets created series")
	}
	for _, r := range cRows {
		pt := pointMap[r.Bucket]
		pt.Date = r.Bucket
		pt.Created = r.Count
		pointMap[r.Bucket] = pt
	}

	var rRows []bucketRow
	qResolvedSeries := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr(w.BucketExpr("t.resolved_at")+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND t.resolved_at >= ? AND t.resolved_at < ?", sub.Tenant, conversation.Ticket, w.Start, w.End).
		GroupExpr("1")
	applyTicketFilters(qResolvedSeries, f)
	if err := qResolvedSeries.Scan(ctx, &rRows); err != nil {
		return nil, errors.Wrap(err, "report tickets resolved series")
	}
	for _, r := range rRows {
		pt := pointMap[r.Bucket]
		pt.Date = r.Bucket
		pt.Resolved = r.Count
		pointMap[r.Bucket] = pt
	}

	series := period.FillSeriesMap(w, pointMap, func(b string) TicketsPoint {
		return TicketsPoint{Date: b}
	})

	// 4. 按工单类型拆解
	type typeRow struct {
		ID       *string `bun:"id"`
		Name     string  `bun:"name"`
		Disabled bool    `bun:"disabled"`
		Created  int64   `bun:"created"`
		Resolved int64   `bun:"resolved"`
		OpenNow  int64   `bun:"open_now"`
	}
	var typeRows []typeRow
	qByType := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		Join("LEFT JOIN ticket_types tt ON tt.id = t.type_id").
		ColumnExpr("t.type_id::text AS id, COALESCE(tt.name, '') AS name, (tt.disabled_at IS NOT NULL) AS disabled").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS resolved", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.status <> ?) AS open_now", conversation.Status(conversation.TicketResolved)).
		Where("c.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Ticket).
		Where("(c.created_at >= ? AND c.created_at < ?) OR (t.resolved_at >= ? AND t.resolved_at < ?) OR c.status <> ?",
			w.Start, w.End, w.Start, w.End, conversation.Status(conversation.TicketResolved)).
		GroupExpr("t.type_id, tt.name, tt.disabled_at").
		OrderExpr("created DESC, open_now DESC, name ASC")
	applyTicketFilters(qByType, f)
	if err := qByType.Scan(ctx, &typeRows); err != nil {
		return nil, errors.Wrap(err, "report tickets by type")
	}
	byType := make([]TicketTypeVolume, len(typeRows))
	for i, r := range typeRows {
		byType[i] = TicketTypeVolume{
			ID:       r.ID,
			Name:     r.Name,
			Disabled: r.Disabled,
			Created:  r.Created,
			Resolved: r.Resolved,
			OpenNow:  r.OpenNow,
		}
	}

	// 5. 按团队收件箱拆解
	type teamRow struct {
		ID       *string `bun:"id"`
		Name     string  `bun:"name"`
		Disabled bool    `bun:"disabled"`
		Created  int64   `bun:"created"`
		Closed   int64   `bun:"closed"`
	}
	var teamRows []teamRow
	qByTeam := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		Join("LEFT JOIN teams tm ON tm.id = c.team_id").
		ColumnExpr("c.team_id::text AS id, COALESCE(tm.name, '') AS name, (tm.disabled_at IS NOT NULL) AS disabled").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS closed", w.Start, w.End).
		Where("c.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Ticket).
		Where("(c.created_at >= ? AND c.created_at < ?) OR (t.resolved_at >= ? AND t.resolved_at < ?)", w.Start, w.End, w.Start, w.End).
		GroupExpr("c.team_id, tm.name, tm.disabled_at").
		OrderExpr("created DESC, closed DESC, name ASC")
	applyTicketFilters(qByTeam, f)
	if err := qByTeam.Scan(ctx, &teamRows); err != nil {
		return nil, errors.Wrap(err, "report tickets by team")
	}
	byTeam := make([]TeamVolume, len(teamRows))
	for i, r := range teamRows {
		byTeam[i] = TeamVolume{ID: r.ID, Name: r.Name, Disabled: r.Disabled, Created: r.Created, Closed: r.Closed}
	}

	// 6. 按优先级拆解
	byPriority := []TicketPriorityVolume{}
	qByPri := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr("c.priority AS priority").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.status <> ?) AS open_now", conversation.Status(conversation.TicketResolved)).
		Where("c.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Ticket).
		Where("(c.created_at >= ? AND c.created_at < ?) OR c.status <> ?", w.Start, w.End, conversation.Status(conversation.TicketResolved)).
		GroupExpr("c.priority").
		OrderExpr("created DESC, open_now DESC")
	applyTicketFilters(qByPri, f)
	if err := qByPri.Scan(ctx, &byPriority); err != nil {
		return nil, errors.Wrap(err, "report tickets by priority")
	}
	if byPriority == nil {
		byPriority = []TicketPriorityVolume{}
	}

	// 7. 按可见范围拆解
	byVis := []TicketVisibilityVolume{}
	qByVis := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr("t.visibility AS visibility").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.status <> ?) AS open_now", conversation.Status(conversation.TicketResolved)).
		Where("c.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Ticket).
		Where("(c.created_at >= ? AND c.created_at < ?) OR c.status <> ?", w.Start, w.End, conversation.Status(conversation.TicketResolved)).
		GroupExpr("t.visibility").
		OrderExpr("created DESC, open_now DESC")
	applyTicketFilters(qByVis, f)
	if err := qByVis.Scan(ctx, &byVis); err != nil {
		return nil, errors.Wrap(err, "report tickets by visibility")
	}
	if byVis == nil {
		byVis = []TicketVisibilityVolume{}
	}

	// 8. 按负责人拆解（解决数、当前未解决、解决中位时长）
	type assigneeRow struct {
		MemberID string   `bun:"member_id"`
		Resolved int64    `bun:"resolved"`
		OpenNow  int64    `bun:"open_now"`
		Median   *float64 `bun:"median"`
	}
	var assigneeRows []assigneeRow
	qByAssignee := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr("c.assignee_id AS member_id").
		ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS resolved", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.status <> ?) AS open_now", conversation.Status(conversation.TicketResolved)).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+resExpr+") FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS median", w.Start, w.End).
		Where("c.tenant_id = ? AND c.kind = ? AND c.assignee_type = ? AND c.assignee_id <> ''", sub.Tenant, conversation.Ticket, conversation.MemberActor).
		Where("(t.resolved_at >= ? AND t.resolved_at < ?) OR c.status <> ?", w.Start, w.End, conversation.Status(conversation.TicketResolved)).
		GroupExpr("c.assignee_id")
	applyTicketFilters(qByAssignee, f)
	if err := qByAssignee.Scan(ctx, &assigneeRows); err != nil {
		return nil, errors.Wrap(err, "report tickets by assignee")
	}

	currentSet := map[string]bool{}
	if a.roles != nil {
		assigned, err := a.roles.Members(ctx, sub.Tenant)
		if err != nil {
			return nil, errors.Wrap(err, "list tenant members for ticket report")
		}
		for _, m := range assigned {
			if m.Subject.Type == member.Type {
				currentSet[m.Subject.ID] = true
			}
		}
	}

	byAssignee := make([]TicketAssigneeMetrics, 0, len(assigneeRows))
	assigneeIDs := make([]string, 0, len(assigneeRows))
	assigneeMap := map[string]*TicketAssigneeMetrics{}
	for _, r := range assigneeRows {
		m := &TicketAssigneeMetrics{
			MemberID:            r.MemberID,
			Removed:             !currentSet[r.MemberID],
			Resolved:            r.Resolved,
			OpenNow:             r.OpenNow,
			ResolutionMedianSec: roundPtr(r.Median),
		}
		assigneeMap[r.MemberID] = m
		assigneeIDs = append(assigneeIDs, r.MemberID)
	}
	if len(assigneeIDs) > 0 {
		type profileRow struct {
			ID           string     `bun:"id"`
			Name         string     `bun:"name"`
			AvatarFileID *uuid.UUID `bun:"avatar_file_id"`
		}
		var profiles []profileRow
		if err := a.db.NewSelect().Table("members").
			ColumnExpr("id::text AS id, name, avatar_file_id").
			Where("id IN (?)", bun.List(assigneeIDs)).
			Scan(ctx, &profiles); err != nil {
			return nil, errors.Wrap(err, "load assignee profiles for ticket report")
		}
		for _, p := range profiles {
			m := assigneeMap[p.ID]
			if m == nil {
				continue
			}
			m.Name = p.Name
			if a.files != nil {
				url, err := fileapi.URL(ctx, a.files, p.AvatarFileID)
				if err != nil {
					return nil, err
				}
				m.AvatarURL = url
			}
		}
		for _, m := range assigneeMap {
			byAssignee = append(byAssignee, *m)
		}
		slices.SortFunc(byAssignee, func(x, y TicketAssigneeMetrics) int {
			if x.Resolved != y.Resolved {
				if x.Resolved > y.Resolved {
					return -1
				}
				return 1
			}
			if x.OpenNow != y.OpenNow {
				if x.OpenNow > y.OpenNow {
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
	}

	return &TicketsReport{
		Period:       PeriodMeta(w.Meta()),
		Summary:      sum,
		Series:       series,
		ByType:       byType,
		ByTeam:       byTeam,
		ByPriority:   byPriority,
		ByVisibility: byVis,
		ByAssignee:   byAssignee,
	}, nil
}

// HTTP 处理 GET /t/{slug}/api/reports/tickets。
func (a *Tickets) HTTP(ctx context.Context, in *TicketFilterParams) (*TicketsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &TicketsOutput{Body: *res}, nil
}
