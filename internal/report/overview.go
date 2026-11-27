package report

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
)

// OverviewLive 是工作区此刻的实时运营快照（不受时间窗口筛选影响）。
type OverviewLive struct {
	ServiceOpen        int64 `json:"service_open" doc:"当前打开的对外客服会话数"`
	ServiceWaiting     int64 `json:"service_waiting" doc:"当前等待回复的对外客服会话数"`
	ServiceUnassigned  int64 `json:"service_unassigned" doc:"当前未关闭且无负责人的对外客服会话数"`
	ServiceSnoozed     int64 `json:"service_snoozed" doc:"当前稍后处理的对外客服会话数"`
	PartnerOpen        int64 `json:"partner_open" doc:"当前打开的商务服务会话数"`
	PartnerWaiting     int64 `json:"partner_waiting" doc:"当前等待回复的商务服务会话数"`
	PartnerUnassigned  int64 `json:"partner_unassigned" doc:"当前未关闭且无负责人的商务服务会话数"`
	InternalOpen       int64 `json:"internal_open" doc:"当前打开的内部服务会话数"`
	InternalWaiting    int64 `json:"internal_waiting" doc:"当前等待回复的内部服务会话数"`
	InternalUnassigned int64 `json:"internal_unassigned" doc:"当前未关闭且无负责人的内部服务会话数"`
	TicketOpen         int64 `json:"ticket_open" doc:"当前未解决的工单总数"`
	TicketSubmitted    int64 `json:"ticket_submitted" doc:"当前已提交状态的工单数"`
	TicketInProgress   int64 `json:"ticket_in_progress" doc:"当前处理中状态的工单数"`
	TicketWaiting      int64 `json:"ticket_waiting" doc:"当前等待客户状态的工单数"`
	TicketUnassigned   int64 `json:"ticket_unassigned" doc:"当前未解决且无负责人的工单数"`
	BusinessActive24h  int64 `json:"business_active_24h" doc:"近 24 小时有消息往来的商务沟通会话数"`
	BusinessWaiting    int64 `json:"business_waiting" doc:"最新一条消息来自外部联系人的商务沟通会话数"`
	OnlineMembers      int64 `json:"online_members" doc:"当前在线的工作区成员数"`
	TotalMembers       int64 `json:"total_members" doc:"工作区在职成员总数"`
}

// OverviewService 是统计期内对外客服业务线的核心汇总。
type OverviewService struct {
	Created             int64    `json:"created"`
	PrevCreated         int64    `json:"prev_created"`
	Closed              int64    `json:"closed"`
	PrevClosed          int64    `json:"prev_closed"`
	FirstReplyMedianSec *float64 `json:"first_reply_median_seconds,omitempty"`
	PrevFirstReplySec   *float64 `json:"prev_first_reply_median_seconds,omitempty"`
	CSAT                *float64 `json:"csat,omitempty"`
	PrevCSAT            *float64 `json:"prev_csat,omitempty"`
	RatedCount          int64    `json:"rated_count"`
}

// OverviewPartner 是统计期内商务服务（商务群与商务机器人）业务线的核心汇总。
type OverviewPartner struct {
	Created             int64    `json:"created"`
	PrevCreated         int64    `json:"prev_created"`
	Closed              int64    `json:"closed"`
	PrevClosed          int64    `json:"prev_closed"`
	FirstReplyMedianSec *float64 `json:"first_reply_median_seconds,omitempty"`
	PrevFirstReplySec   *float64 `json:"prev_first_reply_median_seconds,omitempty"`
	GroupCreated        int64    `json:"group_created"`
}

// OverviewInternal 是统计期内内部员工服务业务线的核心汇总。
type OverviewInternal struct {
	Created             int64    `json:"created"`
	PrevCreated         int64    `json:"prev_created"`
	Closed              int64    `json:"closed"`
	PrevClosed          int64    `json:"prev_closed"`
	FirstReplyMedianSec *float64 `json:"first_reply_median_seconds,omitempty"`
	PrevFirstReplySec   *float64 `json:"prev_first_reply_median_seconds,omitempty"`
	GroupCreated        int64    `json:"group_created"`
}

// OverviewTickets 是统计期内工单业务线的核心汇总。
type OverviewTickets struct {
	Created             int64    `json:"created"`
	PrevCreated         int64    `json:"prev_created"`
	Resolved            int64    `json:"resolved"`
	PrevResolved        int64    `json:"prev_resolved"`
	ResolutionMedianSec *float64 `json:"resolution_median_seconds,omitempty"`
	PrevResolutionSec   *float64 `json:"prev_resolution_median_seconds,omitempty"`
}

// OverviewBusiness 是统计期内商务沟通业务线的核心汇总。
type OverviewBusiness struct {
	ActiveChats     int64 `json:"active_chats"`
	PrevActiveChats int64 `json:"prev_active_chats"`
	NewChats        int64 `json:"new_chats"`
	PrevNewChats    int64 `json:"prev_new_chats"`
	MessagesTotal   int64 `json:"messages_total"`
	PrevMessages    int64 `json:"prev_messages_total"`
}

// OverviewAI 是统计期内 AI 与翻译调用的核心汇总。
type OverviewAI struct {
	Calls            int64    `json:"calls"`
	PrevCalls        int64    `json:"prev_calls"`
	FailedCalls      int64    `json:"failed_calls"`
	SuccessRate      *float64 `json:"success_rate,omitempty"`
	TotalCostUSD     float64  `json:"total_cost_usd"`
	PrevTotalCostUSD float64  `json:"prev_total_cost_usd"`
}

// OverviewPoint 是全局趋势图中的一个时间桶，合并展示对外客服、商务服务、内部服务、工单与商务沟通的业务量走势。
type OverviewPoint struct {
	Date             string `json:"date"`
	ServiceCreated   int64  `json:"service_created"`
	PartnerCreated   int64  `json:"partner_created"`
	InternalCreated  int64  `json:"internal_created"`
	TicketCreated    int64  `json:"ticket_created"`
	BusinessMessages int64  `json:"business_messages"`
}

// OverviewReport 是工作区报表概览的完整响应。
type OverviewReport struct {
	Period   PeriodMeta       `json:"period"`
	Live     OverviewLive     `json:"live"`
	Service  OverviewService  `json:"service"`
	Partner  OverviewPartner  `json:"partner"`
	Internal OverviewInternal `json:"internal"`
	Tickets  OverviewTickets  `json:"tickets"`
	Business OverviewBusiness `json:"business"`
	AI       OverviewAI       `json:"ai"`
	Series   []OverviewPoint  `json:"series" nullable:"false"`
}

// OverviewOutput 是报表概览的 HTTP 响应。
type OverviewOutput struct {
	Body OverviewReport
}

// Overview 汇总工作区全局实时状态与各业务线的周期核心指标。
type Overview struct {
	db    *bun.DB
	roles *authz.Store
}

// NewOverview 返回报表概览动作。
func NewOverview(db *bun.DB, roles *authz.Store) *Overview {
	return &Overview{db: db, roles: roles}
}

// Handle 返回 sub 所在工作区按时间窗口 p 统计的全局概览报表。
func (a *Overview) Handle(ctx context.Context, sub identity.Subject, p period.Params) (*OverviewReport, error) {
	w, err := resolvePeriodOnly(ctx, a.db, sub.Tenant, p)
	if err != nil {
		return nil, err
	}

	var live OverviewLive

	// 1. 实时快照：对外客服、商务服务、内部服务与工单积压
	if err := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status = ?) AS svc_open", conversation.Service, channel.AudienceCustomer, conversation.Open).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status <> ? AND c.waiting_since IS NOT NULL) AS svc_waiting", conversation.Service, channel.AudienceCustomer, conversation.Closed).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status <> ? AND c.assignee_id = '') AS svc_unassigned", conversation.Service, channel.AudienceCustomer, conversation.Closed).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status = ?) AS svc_snoozed", conversation.Service, channel.AudienceCustomer, conversation.Snoozed).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status = ?) AS ptr_open", conversation.Service, channel.AudiencePartner, conversation.Open).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status <> ? AND c.waiting_since IS NOT NULL) AS ptr_waiting", conversation.Service, channel.AudiencePartner, conversation.Closed).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status <> ? AND c.assignee_id = '') AS ptr_unassigned", conversation.Service, channel.AudiencePartner, conversation.Closed).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status = ?) AS int_open", conversation.Service, channel.AudienceInternal, conversation.Open).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status <> ? AND c.waiting_since IS NOT NULL) AS int_waiting", conversation.Service, channel.AudienceInternal, conversation.Closed).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ? AND c.status <> ? AND c.assignee_id = '') AS int_unassigned", conversation.Service, channel.AudienceInternal, conversation.Closed).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.status <> ?) AS tk_open", conversation.Ticket, conversation.Status(conversation.TicketResolved)).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.status = ?) AS tk_sub", conversation.Ticket, conversation.Status(conversation.TicketSubmitted)).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.status = ?) AS tk_prog", conversation.Ticket, conversation.Status(conversation.TicketInProgress)).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.status = ?) AS tk_wait", conversation.Ticket, conversation.Status(conversation.TicketWaiting)).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.status <> ? AND c.assignee_id = '') AS tk_unassigned", conversation.Ticket, conversation.Status(conversation.TicketResolved)).
		Where("c.tenant_id = ?", sub.Tenant).
		Where("((c.kind = ? AND c.status IN (?)) OR (c.kind = ? AND c.status IN (?)))",
			conversation.Service, bun.In([]conversation.Status{conversation.Open, conversation.Snoozed}),
			conversation.Ticket, bun.In([]conversation.Status{
				conversation.Status(conversation.TicketSubmitted),
				conversation.Status(conversation.TicketInProgress),
				conversation.Status(conversation.TicketWaiting),
			})).
		Scan(ctx,
			&live.ServiceOpen, &live.ServiceWaiting, &live.ServiceUnassigned, &live.ServiceSnoozed,
			&live.PartnerOpen, &live.PartnerWaiting, &live.PartnerUnassigned,
			&live.InternalOpen, &live.InternalWaiting, &live.InternalUnassigned,
			&live.TicketOpen, &live.TicketSubmitted, &live.TicketInProgress, &live.TicketWaiting, &live.TicketUnassigned,
		); err != nil {
		return nil, errors.Wrap(err, "report overview live backlog")
	}

	// 2. 实时快照：商务沟通近 24 小时活跃数与待跟进数（最新消息来自外部联系人或外部群发言人）
	since24h := now().Add(-24 * time.Hour)
	if err := a.db.NewRaw(`
		SELECT
			count(*) FILTER (WHERE c.last_message_at >= ?) AS active_24h,
			count(*) FILTER (WHERE last_msg.actor_type IN (?, ?)) AS waiting
		FROM conversations c
		JOIN chats ch ON ch.conversation_id = c.id
		LEFT JOIN LATERAL (
			SELECT it.actor_type
			FROM timeline_items it
			WHERE it.conversation_id = c.id AND it.tenant_id = c.tenant_id AND it.kind = ?
			ORDER BY it.seq DESC
			LIMIT 1
		) last_msg ON true
		WHERE c.tenant_id = ? AND c.kind = ? AND ch.scope = ?`,
		since24h, conversation.ContactActor, conversation.ExternalUserActor, conversation.Message, sub.Tenant, conversation.Chat, conversation.ChatExternal,
	).Scan(ctx, &live.BusinessActive24h, &live.BusinessWaiting); err != nil {
		return nil, errors.Wrap(err, "report overview live business")
	}

	// 3. 实时快照：在线成员数与在职总人数
	if a.roles != nil {
		assigned, err := a.roles.Members(ctx, sub.Tenant)
		if err != nil {
			return nil, errors.Wrap(err, "report overview list members")
		}
		memberIDs := make([]string, 0, len(assigned))
		for _, m := range assigned {
			if m.Subject.Type == member.Type {
				memberIDs = append(memberIDs, m.Subject.ID)
			}
		}
		live.TotalMembers = int64(len(memberIDs))
		if len(memberIDs) > 0 {
			statuses, err := member.StatusesOf(ctx, a.db, sub.Tenant, memberIDs)
			if err != nil {
				return nil, err
			}
			for _, st := range statuses {
				if st == member.Online {
					live.OnlineMembers++
				}
			}
		}
	}

	// 4. 对外客服、商务服务与内部服务周期汇总
	var svc OverviewService
	var partnerSvc OverviewPartner
	var internalSvc OverviewInternal
	if err := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.created_at >= ? AND c.created_at < ?) AS svc_created", channel.AudienceCustomer, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.created_at >= ? AND c.created_at < ?) AS svc_prev_created", channel.AudienceCustomer, w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.created_at >= ? AND c.created_at < ?) AS ptr_created", channel.AudiencePartner, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.created_at >= ? AND c.created_at < ?) AS ptr_prev_created", channel.AudiencePartner, w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.external_group_id IS NOT NULL AND c.created_at >= ? AND c.created_at < ?) AS ptr_group_created", channel.AudiencePartner, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.created_at >= ? AND c.created_at < ?) AS int_created", channel.AudienceInternal, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.created_at >= ? AND c.created_at < ?) AS int_prev_created", channel.AudienceInternal, w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND c.external_group_id IS NOT NULL AND c.created_at >= ? AND c.created_at < ?) AS int_group_created", channel.AudienceInternal, w.Start, w.End).
		Where("c.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", sub.Tenant, conversation.Service, w.PrevStart, w.End).
		Scan(ctx,
			&svc.Created, &svc.PrevCreated,
			&partnerSvc.Created, &partnerSvc.PrevCreated, &partnerSvc.GroupCreated,
			&internalSvc.Created, &internalSvc.PrevCreated, &internalSvc.GroupCreated,
		); err != nil {
		return nil, errors.Wrap(err, "report overview service created")
	}
	frtExpr := "GREATEST(EXTRACT(EPOCH FROM (r.first_reply_at - r.opened_at)), 0)"
	var svcPos, svcPrevRated, svcPrevPos int64
	if err := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.closed_at >= ? AND r.closed_at < ?) AS closed", channel.AudienceCustomer, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.closed_at >= ? AND r.closed_at < ?) AS prev_closed", channel.AudienceCustomer, w.PrevStart, w.PrevEnd).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") FILTER (WHERE c.audience = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?) AS frt_med", channel.AudienceCustomer, w.Start, w.End).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") FILTER (WHERE c.audience = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?) AS prev_frt_med", channel.AudienceCustomer, w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.rated_at >= ? AND r.rated_at < ? AND r.rating IS NOT NULL) AS rated", channel.AudienceCustomer, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.rated_at >= ? AND r.rated_at < ? AND r.rating >= 4) AS pos", channel.AudienceCustomer, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.rated_at >= ? AND r.rated_at < ? AND r.rating IS NOT NULL) AS prev_rated", channel.AudienceCustomer, w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.rated_at >= ? AND r.rated_at < ? AND r.rating >= 4) AS prev_pos", channel.AudienceCustomer, w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.closed_at >= ? AND r.closed_at < ?) AS ptr_closed", channel.AudiencePartner, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.closed_at >= ? AND r.closed_at < ?) AS ptr_prev_closed", channel.AudiencePartner, w.PrevStart, w.PrevEnd).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") FILTER (WHERE c.audience = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?) AS ptr_frt_med", channel.AudiencePartner, w.Start, w.End).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") FILTER (WHERE c.audience = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?) AS ptr_prev_frt_med", channel.AudiencePartner, w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.closed_at >= ? AND r.closed_at < ?) AS int_closed", channel.AudienceInternal, w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.audience = ? AND r.closed_at >= ? AND r.closed_at < ?) AS int_prev_closed", channel.AudienceInternal, w.PrevStart, w.PrevEnd).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") FILTER (WHERE c.audience = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?) AS int_frt_med", channel.AudienceInternal, w.Start, w.End).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") FILTER (WHERE c.audience = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?) AS int_prev_frt_med", channel.AudienceInternal, w.PrevStart, w.PrevEnd).
		Where("r.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Service).
		Where("(r.closed_at >= ? AND r.closed_at < ?) OR (r.first_reply_at >= ? AND r.first_reply_at < ?) OR (r.rated_at >= ? AND r.rated_at < ?)",
			w.PrevStart, w.End, w.PrevStart, w.End, w.PrevStart, w.End).
		Scan(ctx,
			&svc.Closed, &svc.PrevClosed, &svc.FirstReplyMedianSec, &svc.PrevFirstReplySec, &svc.RatedCount, &svcPos, &svcPrevRated, &svcPrevPos,
			&partnerSvc.Closed, &partnerSvc.PrevClosed, &partnerSvc.FirstReplyMedianSec, &partnerSvc.PrevFirstReplySec,
			&internalSvc.Closed, &internalSvc.PrevClosed, &internalSvc.FirstReplyMedianSec, &internalSvc.PrevFirstReplySec,
		); err != nil {
		return nil, errors.Wrap(err, "report overview service rounds")
	}
	svc.FirstReplyMedianSec = roundPtr(svc.FirstReplyMedianSec)
	svc.PrevFirstReplySec = roundPtr(svc.PrevFirstReplySec)
	svc.CSAT = pct(svcPos, svc.RatedCount)
	svc.PrevCSAT = pct(svcPrevPos, svcPrevRated)
	partnerSvc.FirstReplyMedianSec = roundPtr(partnerSvc.FirstReplyMedianSec)
	partnerSvc.PrevFirstReplySec = roundPtr(partnerSvc.PrevFirstReplySec)
	internalSvc.FirstReplyMedianSec = roundPtr(internalSvc.FirstReplyMedianSec)
	internalSvc.PrevFirstReplySec = roundPtr(internalSvc.PrevFirstReplySec)

	// 5. 工单周期汇总
	var tk OverviewTickets
	tkResExpr := "GREATEST(EXTRACT(EPOCH FROM (t.resolved_at - c.created_at)), 0)"
	if err := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS prev_created", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS resolved", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS prev_resolved", w.PrevStart, w.PrevEnd).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+tkResExpr+") FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS res_med", w.Start, w.End).
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+tkResExpr+") FILTER (WHERE t.resolved_at >= ? AND t.resolved_at < ?) AS prev_res_med", w.PrevStart, w.PrevEnd).
		Where("c.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Ticket).
		Where("(c.created_at >= ? AND c.created_at < ?) OR (t.resolved_at >= ? AND t.resolved_at < ?)", w.PrevStart, w.End, w.PrevStart, w.End).
		Scan(ctx, &tk.Created, &tk.PrevCreated, &tk.Resolved, &tk.PrevResolved, &tk.ResolutionMedianSec, &tk.PrevResolutionSec); err != nil {
		return nil, errors.Wrap(err, "report overview tickets")
	}
	tk.ResolutionMedianSec = roundPtr(tk.ResolutionMedianSec)
	tk.PrevResolutionSec = roundPtr(tk.PrevResolutionSec)

	// 6. 商务沟通周期汇总
	var biz OverviewBusiness
	if err := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS new_chats", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS prev_new_chats", w.PrevStart, w.PrevEnd).
		Where("c.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND c.created_at >= ? AND c.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, w.PrevStart, w.End).
		Scan(ctx, &biz.NewChats, &biz.PrevNewChats); err != nil {
		return nil, errors.Wrap(err, "report overview business chats")
	}
	if err := a.db.NewSelect().TableExpr("timeline_items it").
		Join("JOIN conversations c ON c.id = it.conversation_id").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		ColumnExpr("count(DISTINCT it.conversation_id) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS active_chats", w.Start, w.End).
		ColumnExpr("count(DISTINCT it.conversation_id) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS prev_active_chats", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS msgs", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS prev_msgs", w.PrevStart, w.PrevEnd).
		Where("it.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND it.kind = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, conversation.Message, w.PrevStart, w.End).
		Scan(ctx, &biz.ActiveChats, &biz.PrevActiveChats, &biz.MessagesTotal, &biz.PrevMessages); err != nil {
		return nil, errors.Wrap(err, "report overview business messages")
	}

	// 7. AI 与翻译周期汇总
	var ai OverviewAI
	var aiSuccess int64
	if err := a.db.NewSelect().Table("llm_calls").
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ?) AS calls", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ?) AS prev_calls", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ? AND error = '') AS ok", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ? AND error <> '') AS failed", w.Start, w.End).
		ColumnExpr("COALESCE(sum(cost) FILTER (WHERE created_at >= ? AND created_at < ?), 0)::float8 AS cost", w.Start, w.End).
		ColumnExpr("COALESCE(sum(cost) FILTER (WHERE created_at >= ? AND created_at < ?), 0)::float8 AS prev_cost", w.PrevStart, w.PrevEnd).
		Where("tenant_id = ? AND call_type = ? AND created_at >= ? AND created_at < ?", sub.Tenant, llm.Translation, w.PrevStart, w.End).
		Scan(ctx, &ai.Calls, &ai.PrevCalls, &aiSuccess, &ai.FailedCalls, &ai.TotalCostUSD, &ai.PrevTotalCostUSD); err != nil {
		return nil, errors.Wrap(err, "report overview ai")
	}
	ai.SuccessRate = pct(aiSuccess, ai.Calls)
	ai.TotalCostUSD = round6(ai.TotalCostUSD)
	ai.PrevTotalCostUSD = round6(ai.PrevTotalCostUSD)

	// 8. 合并全局趋势序列（对外客服新建、商务服务新建、内部服务新建、工单新建、商务消息量）
	pointMap := map[string]*OverviewPoint{}
	ensurePt := func(b string) *OverviewPoint {
		pt, ok := pointMap[b]
		if !ok {
			pt = &OverviewPoint{Date: b}
			pointMap[b] = pt
		}
		return pt
	}
	type convSeriesRow struct {
		Bucket   string `bun:"bucket"`
		Service  int64  `bun:"svc"`
		Partner  int64  `bun:"partner"`
		Internal int64  `bun:"internal"`
		Ticket   int64  `bun:"tk"`
	}
	var convRows []convSeriesRow
	if err := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr(bucketExpr("c.created_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ?) AS svc", conversation.Service, channel.AudienceCustomer).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ?) AS partner", conversation.Service, channel.AudiencePartner).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ? AND c.audience = ?) AS internal", conversation.Service, channel.AudienceInternal).
		ColumnExpr("count(*) FILTER (WHERE c.kind = ?) AS tk", conversation.Ticket).
		Where("c.tenant_id = ? AND c.kind IN (?, ?) AND c.created_at >= ? AND c.created_at < ?",
			sub.Tenant, conversation.Service, conversation.Ticket, w.Start, w.End).
		GroupExpr("1").
		Scan(ctx, &convRows); err != nil {
		return nil, errors.Wrap(err, "report overview conv series")
	}
	for _, r := range convRows {
		pt := ensurePt(r.Bucket)
		pt.ServiceCreated = r.Service
		pt.PartnerCreated = r.Partner
		pt.InternalCreated = r.Internal
		pt.TicketCreated = r.Ticket
	}

	type bizSeriesRow struct {
		Bucket string `bun:"bucket"`
		Count  int64  `bun:"cnt"`
	}
	var bizRows []bizSeriesRow
	if err := a.db.NewSelect().TableExpr("timeline_items it").
		Join("JOIN conversations c ON c.id = it.conversation_id").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		ColumnExpr(bucketExpr("it.created_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS cnt").
		Where("it.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND it.kind = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, conversation.Message, w.Start, w.End).
		GroupExpr("1").
		Scan(ctx, &bizRows); err != nil {
		return nil, errors.Wrap(err, "report overview business series")
	}
	for _, r := range bizRows {
		ensurePt(r.Bucket).BusinessMessages = r.Count
	}

	sparse := make([]OverviewPoint, 0, len(pointMap))
	for _, pt := range pointMap {
		sparse = append(sparse, *pt)
	}
	series := period.FillSeries(w, sparse,
		func(pt OverviewPoint) string { return pt.Date },
		func(b string) OverviewPoint { return OverviewPoint{Date: b} },
	)

	return &OverviewReport{
		Period:   PeriodMeta(w.Meta()),
		Live:     live,
		Service:  svc,
		Partner:  partnerSvc,
		Internal: internalSvc,
		Tickets:  tk,
		Business: biz,
		AI:       ai,
		Series:   series,
	}, nil
}

// HTTP 处理 GET /t/{slug}/api/reports/overview。
func (a *Overview) HTTP(ctx context.Context, in *period.Params) (*OverviewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &OverviewOutput{Body: *res}, nil
}
