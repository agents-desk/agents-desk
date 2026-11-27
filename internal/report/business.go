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
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/account"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// BusinessFilterParams 是商务沟通报表的查询参数。
type BusinessFilterParams struct {
	period.Params
	ChannelKind string `query:"channel_kind" enum:"business_link,telegram_account" doc:"按商务渠道类型筛选"`
	OwnerID     string `query:"owner_id" format:"uuid" doc:"按渠道归属成员筛选"`
}

// BusinessSummary 是商务沟通报表的核心活跃度与消息量汇总。
type BusinessSummary struct {
	ActiveChats       int64 `json:"active_chats" doc:"统计期内有消息往来的商务会话数"`
	PrevActiveChats   int64 `json:"prev_active_chats" doc:"上一对比期活跃的商务会话数"`
	NewChats          int64 `json:"new_chats" doc:"统计期内新建的商务会话数"`
	PrevNewChats      int64 `json:"prev_new_chats" doc:"上一对比期新建的商务会话数"`
	MessagesTotal     int64 `json:"messages_total" doc:"统计期内商务沟通消息总数"`
	PrevMessagesTotal int64 `json:"prev_messages_total" doc:"上一对比期商务沟通消息总数"`
	MessagesInbound   int64 `json:"messages_inbound" doc:"外部联系人发来的消息数"`
	MessagesOutbound  int64 `json:"messages_outbound" doc:"工作区成员发出的消息数"`
	DirectChats       int64 `json:"direct_chats" doc:"统计期内活跃的单聊商务会话数"`
	GroupChats        int64 `json:"group_chats" doc:"统计期内活跃的群聊商务会话数"`
	TotalChatsNow     int64 `json:"total_chats_now" doc:"累计商务沟通会话总数（实时快照）"`
	ActiveChannelsNow int64 `json:"active_channels_now" doc:"当前启用中的个人商务渠道数（实时快照）"`
	WaitingNow        int64 `json:"waiting_now" doc:"最新消息来自外部联系人的待跟进会话数（实时快照）"`
}

// BusinessPoint 是商务沟通趋势图中的一个时间桶。
type BusinessPoint struct {
	Date     string `json:"date"`
	Inbound  int64  `json:"inbound"`
	Outbound int64  `json:"outbound"`
	NewChats int64  `json:"new_chats"`
}

// BusinessChannelKindVolume 是按商务渠道种类拆分的活跃度与消息量。
type BusinessChannelKindVolume struct {
	Kind             channel.Kind `json:"kind"`
	ActiveChats      int64        `json:"active_chats"`
	NewChats         int64        `json:"new_chats"`
	MessagesTotal    int64        `json:"messages_total"`
	MessagesInbound  int64        `json:"messages_inbound"`
	MessagesOutbound int64        `json:"messages_outbound"`
}

// BusinessOwnerMetrics 是按渠道归属成员拆分的商务沟通活跃度。
type BusinessOwnerMetrics struct {
	MemberID         string `json:"member_id"`
	Name             string `json:"name"`
	AvatarURL        string `json:"avatar_url"`
	OnlineStatus     string `json:"online_status"`
	Removed          bool   `json:"removed"`
	ChannelsCount    int64  `json:"channels_count"`
	ActiveChats      int64  `json:"active_chats"`
	NewChats         int64  `json:"new_chats"`
	MessagesInbound  int64  `json:"messages_inbound"`
	MessagesOutbound int64  `json:"messages_outbound"`
}

// BusinessReport 是商务沟通报表的完整响应。
type BusinessReport struct {
	Period        PeriodMeta                  `json:"period"`
	Summary       BusinessSummary             `json:"summary"`
	Series        []BusinessPoint             `json:"series" nullable:"false"`
	ByChannelKind []BusinessChannelKindVolume `json:"by_channel_kind" nullable:"false"`
	ByOwner       []BusinessOwnerMetrics      `json:"by_owner" nullable:"false"`
	ByLanguage    []LanguageCount             `json:"by_language" nullable:"false"`
}

// BusinessOutput 是商务沟通报表的 HTTP 响应。
type BusinessOutput struct {
	Body BusinessReport
}

// Business 汇总工作区对外商务沟通（个人网页链接与 Telegram 个人号）的活跃度、消息量与成员拆解。
type Business struct {
	db    *bun.DB
	roles *authz.Store
	files *filesystem.Service
}

// NewBusiness 返回商务沟通报表动作。
func NewBusiness(db *bun.DB, roles *authz.Store, files *filesystem.Service) *Business {
	return &Business{db: db, roles: roles, files: files}
}

// applyBusinessFilters 在已关联 channels chan_t 的查询上叠加渠道种类与归属成员筛选。
func applyBusinessFilters(q *bun.SelectQuery, f BusinessFilterParams) {
	if f.ChannelKind != "" {
		q.Where("chan_t.kind = ?", f.ChannelKind)
	}
	if f.OwnerID != "" {
		q.Where("chan_t.owner_member_id = ?", f.OwnerID)
	}
}

// Handle 返回 sub 所在工作区按筛选条件 f 统计的商务沟通报表。
func (a *Business) Handle(ctx context.Context, sub identity.Subject, f BusinessFilterParams) (*BusinessReport, error) {
	w, err := resolvePeriodOnly(ctx, a.db, sub.Tenant, f.Params)
	if err != nil {
		return nil, err
	}

	var sum BusinessSummary

	// 1. 商务会话新建数与累计实时总数
	qChats := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("LEFT JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS new_chats", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS prev_new_chats", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) AS total_now").
		Where("c.tenant_id = ? AND c.kind = ? AND ch.scope = ?", sub.Tenant, conversation.Chat, conversation.ChatExternal)
	applyBusinessFilters(qChats, f)
	if err := qChats.Scan(ctx, &sum.NewChats, &sum.PrevNewChats, &sum.TotalChatsNow); err != nil {
		return nil, errors.Wrap(err, "report business chats summary")
	}

	// 2. 启用中的个人商务渠道数（实时快照）
	qChans := a.db.NewSelect().TableExpr("channels chan_t").
		ColumnExpr("count(*)").
		Where("chan_t.tenant_id = ? AND chan_t.owner_member_id IS NOT NULL AND chan_t.enabled = TRUE", sub.Tenant)
	applyBusinessFilters(qChans, f)
	if err := qChans.Scan(ctx, &sum.ActiveChannelsNow); err != nil {
		return nil, errors.Wrap(err, "report business active channels")
	}

	// 3. 待跟进商务会话数（最新消息来自外部联系人）
	qWait := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("LEFT JOIN channels chan_t ON chan_t.id = ch.channel_id").
		Join(`LEFT JOIN LATERAL (
			SELECT it.actor_type
			FROM timeline_items it
			WHERE it.conversation_id = c.id AND it.tenant_id = c.tenant_id AND it.kind = ?
			ORDER BY it.seq DESC
			LIMIT 1
		) last_msg ON true`, conversation.Message).
		ColumnExpr("count(*) FILTER (WHERE last_msg.actor_type IN (?, ?))", conversation.ContactActor, conversation.ExternalUserActor).
		Where("c.tenant_id = ? AND c.kind = ? AND ch.scope = ?", sub.Tenant, conversation.Chat, conversation.ChatExternal)
	applyBusinessFilters(qWait, f)
	if err := qWait.Scan(ctx, &sum.WaitingNow); err != nil {
		return nil, errors.Wrap(err, "report business waiting now")
	}

	// 4. 统计期内活跃会话、单聊/群聊分布及收发消息量
	qMsgs := a.db.NewSelect().TableExpr("timeline_items it").
		Join("JOIN conversations c ON c.id = it.conversation_id").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("LEFT JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr("count(DISTINCT it.conversation_id) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS active_chats", w.Start, w.End).
		ColumnExpr("count(DISTINCT it.conversation_id) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS prev_active_chats", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(DISTINCT it.conversation_id) FILTER (WHERE it.created_at >= ? AND it.created_at < ? AND ch.mode = ?) AS direct_chats", w.Start, w.End, conversation.ChatDirect).
		ColumnExpr("count(DISTINCT it.conversation_id) FILTER (WHERE it.created_at >= ? AND it.created_at < ? AND ch.mode = ?) AS group_chats", w.Start, w.End, conversation.ChatGroup).
		ColumnExpr("count(*) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS msgs_total", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE it.created_at >= ? AND it.created_at < ?) AS prev_msgs_total", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE it.created_at >= ? AND it.created_at < ? AND it.actor_type IN (?, ?)) AS msgs_in", w.Start, w.End, conversation.ContactActor, conversation.ExternalUserActor).
		ColumnExpr("count(*) FILTER (WHERE it.created_at >= ? AND it.created_at < ? AND it.actor_type = ?) AS msgs_out", w.Start, w.End, conversation.MemberActor).
		Where("it.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND it.kind = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, conversation.Message, w.PrevStart, w.End)
	applyBusinessFilters(qMsgs, f)
	if err := qMsgs.Scan(ctx,
		&sum.ActiveChats, &sum.PrevActiveChats, &sum.DirectChats, &sum.GroupChats,
		&sum.MessagesTotal, &sum.PrevMessagesTotal, &sum.MessagesInbound, &sum.MessagesOutbound,
	); err != nil {
		return nil, errors.Wrap(err, "report business messages summary")
	}

	// 5. 趋势序列（外部来信、成员发出、新建会话）
	pointMap := map[string]*BusinessPoint{}
	ensurePt := func(b string) *BusinessPoint {
		pt, ok := pointMap[b]
		if !ok {
			pt = &BusinessPoint{Date: b}
			pointMap[b] = pt
		}
		return pt
	}
	type msgSeriesRow struct {
		Bucket   string `bun:"bucket"`
		Inbound  int64  `bun:"inbound"`
		Outbound int64  `bun:"outbound"`
	}
	var mRows []msgSeriesRow
	qMsgSeries := a.db.NewSelect().TableExpr("timeline_items it").
		Join("JOIN conversations c ON c.id = it.conversation_id").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("LEFT JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr(bucketExpr("it.created_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) FILTER (WHERE it.actor_type IN (?, ?)) AS inbound", conversation.ContactActor, conversation.ExternalUserActor).
		ColumnExpr("count(*) FILTER (WHERE it.actor_type = ?) AS outbound", conversation.MemberActor).
		Where("it.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND it.kind = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, conversation.Message, w.Start, w.End).
		GroupExpr("1")
	applyBusinessFilters(qMsgSeries, f)
	if err := qMsgSeries.Scan(ctx, &mRows); err != nil {
		return nil, errors.Wrap(err, "report business message series")
	}
	for _, r := range mRows {
		pt := ensurePt(r.Bucket)
		pt.Inbound = r.Inbound
		pt.Outbound = r.Outbound
	}

	type newSeriesRow struct {
		Bucket string `bun:"bucket"`
		Count  int64  `bun:"cnt"`
	}
	var nRows []newSeriesRow
	qNewSeries := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("LEFT JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr(bucketExpr("c.created_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND c.created_at >= ? AND c.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, w.Start, w.End).
		GroupExpr("1")
	applyBusinessFilters(qNewSeries, f)
	if err := qNewSeries.Scan(ctx, &nRows); err != nil {
		return nil, errors.Wrap(err, "report business new chat series")
	}
	for _, r := range nRows {
		ensurePt(r.Bucket).NewChats = r.Count
	}

	sparse := make([]BusinessPoint, 0, len(pointMap))
	for _, pt := range pointMap {
		sparse = append(sparse, *pt)
	}
	series := period.FillSeries(w, sparse,
		func(pt BusinessPoint) string { return pt.Date },
		func(b string) BusinessPoint { return BusinessPoint{Date: b} },
	)

	// 6. 按商务渠道种类拆解
	kindMap := map[channel.Kind]*BusinessChannelKindVolume{}
	ensureKind := func(k channel.Kind) *BusinessChannelKindVolume {
		v, ok := kindMap[k]
		if !ok {
			v = &BusinessChannelKindVolume{Kind: k}
			kindMap[k] = v
		}
		return v
	}
	type kindMsgRow struct {
		Kind        channel.Kind `bun:"kind"`
		ActiveChats int64        `bun:"active_chats"`
		Total       int64        `bun:"total"`
		Inbound     int64        `bun:"inbound"`
		Outbound    int64        `bun:"outbound"`
	}
	var kmRows []kindMsgRow
	qKindMsg := a.db.NewSelect().TableExpr("timeline_items it").
		Join("JOIN conversations c ON c.id = it.conversation_id").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr("chan_t.kind AS kind").
		ColumnExpr("count(DISTINCT it.conversation_id) AS active_chats").
		ColumnExpr("count(*) AS total").
		ColumnExpr("count(*) FILTER (WHERE it.actor_type IN (?, ?)) AS inbound", conversation.ContactActor, conversation.ExternalUserActor).
		ColumnExpr("count(*) FILTER (WHERE it.actor_type = ?) AS outbound", conversation.MemberActor).
		Where("it.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND it.kind = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, conversation.Message, w.Start, w.End).
		GroupExpr("chan_t.kind")
	applyBusinessFilters(qKindMsg, f)
	if err := qKindMsg.Scan(ctx, &kmRows); err != nil {
		return nil, errors.Wrap(err, "report business by channel kind messages")
	}
	for _, r := range kmRows {
		v := ensureKind(r.Kind)
		v.ActiveChats = r.ActiveChats
		v.MessagesTotal = r.Total
		v.MessagesInbound = r.Inbound
		v.MessagesOutbound = r.Outbound
	}

	type kindNewRow struct {
		Kind  channel.Kind `bun:"kind"`
		Count int64        `bun:"cnt"`
	}
	var knRows []kindNewRow
	qKindNew := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr("chan_t.kind AS kind, count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND c.created_at >= ? AND c.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, w.Start, w.End).
		GroupExpr("chan_t.kind")
	applyBusinessFilters(qKindNew, f)
	if err := qKindNew.Scan(ctx, &knRows); err != nil {
		return nil, errors.Wrap(err, "report business by channel kind new chats")
	}
	for _, r := range knRows {
		ensureKind(r.Kind).NewChats = r.Count
	}

	byChannelKind := make([]BusinessChannelKindVolume, 0, len(kindMap))
	for _, v := range kindMap {
		byChannelKind = append(byChannelKind, *v)
	}
	slices.SortFunc(byChannelKind, func(x, y BusinessChannelKindVolume) int {
		if x.MessagesTotal != y.MessagesTotal {
			if x.MessagesTotal > y.MessagesTotal {
				return -1
			}
			return 1
		}
		if x.Kind < y.Kind {
			return -1
		}
		if x.Kind > y.Kind {
			return 1
		}
		return 0
	})

	// 7. 按商务渠道归属成员拆解
	currentSet := map[string]bool{}
	if a.roles != nil {
		assigned, err := a.roles.Members(ctx, sub.Tenant)
		if err != nil {
			return nil, errors.Wrap(err, "list tenant members for business report")
		}
		for _, m := range assigned {
			if m.Subject.Type == member.Type {
				currentSet[m.Subject.ID] = true
			}
		}
	}

	ownerMap := map[string]*BusinessOwnerMetrics{}
	ensureOwner := func(id string) *BusinessOwnerMetrics {
		m, ok := ownerMap[id]
		if !ok {
			m = &BusinessOwnerMetrics{MemberID: id, Removed: !currentSet[id]}
			ownerMap[id] = m
		}
		return m
	}

	type ownerChanRow struct {
		OwnerID string `bun:"owner_id"`
		Count   int64  `bun:"cnt"`
	}
	var ocRows []ownerChanRow
	qOwnerChans := a.db.NewSelect().TableExpr("channels chan_t").
		ColumnExpr("chan_t.owner_member_id::text AS owner_id, count(*) AS cnt").
		Where("chan_t.tenant_id = ? AND chan_t.owner_member_id IS NOT NULL AND chan_t.enabled = TRUE", sub.Tenant).
		GroupExpr("chan_t.owner_member_id")
	applyBusinessFilters(qOwnerChans, f)
	if err := qOwnerChans.Scan(ctx, &ocRows); err != nil {
		return nil, errors.Wrap(err, "report business owner channels")
	}
	for _, r := range ocRows {
		ensureOwner(r.OwnerID).ChannelsCount = r.Count
	}

	type ownerMsgRow struct {
		OwnerID     string `bun:"owner_id"`
		ActiveChats int64  `bun:"active_chats"`
		Inbound     int64  `bun:"inbound"`
		Outbound    int64  `bun:"outbound"`
	}
	var omRows []ownerMsgRow
	qOwnerMsgs := a.db.NewSelect().TableExpr("timeline_items it").
		Join("JOIN conversations c ON c.id = it.conversation_id").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr("chan_t.owner_member_id::text AS owner_id").
		ColumnExpr("count(DISTINCT it.conversation_id) AS active_chats").
		ColumnExpr("count(*) FILTER (WHERE it.actor_type IN (?, ?)) AS inbound", conversation.ContactActor, conversation.ExternalUserActor).
		ColumnExpr("count(*) FILTER (WHERE it.actor_type = ?) AS outbound", conversation.MemberActor).
		Where("it.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND chan_t.owner_member_id IS NOT NULL AND it.kind = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, conversation.Message, w.Start, w.End).
		GroupExpr("chan_t.owner_member_id")
	applyBusinessFilters(qOwnerMsgs, f)
	if err := qOwnerMsgs.Scan(ctx, &omRows); err != nil {
		return nil, errors.Wrap(err, "report business owner messages")
	}
	for _, r := range omRows {
		o := ensureOwner(r.OwnerID)
		o.ActiveChats = r.ActiveChats
		o.MessagesInbound = r.Inbound
		o.MessagesOutbound = r.Outbound
	}

	var onRows []ownerChanRow
	qOwnerNew := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr("chan_t.owner_member_id::text AS owner_id, count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND ch.scope = ? AND chan_t.owner_member_id IS NOT NULL AND c.created_at >= ? AND c.created_at < ?",
			sub.Tenant, conversation.Chat, conversation.ChatExternal, w.Start, w.End).
		GroupExpr("chan_t.owner_member_id")
	applyBusinessFilters(qOwnerNew, f)
	if err := qOwnerNew.Scan(ctx, &onRows); err != nil {
		return nil, errors.Wrap(err, "report business owner new chats")
	}
	for _, r := range onRows {
		ensureOwner(r.OwnerID).NewChats = r.Count
	}

	ownerIDs := make([]string, 0, len(ownerMap))
	for id := range ownerMap {
		ownerIDs = append(ownerIDs, id)
	}
	byOwner := make([]BusinessOwnerMetrics, 0, len(ownerMap))
	if len(ownerIDs) > 0 {
		type profileRow struct {
			ID           string     `bun:"id"`
			Name         string     `bun:"name"`
			AvatarFileID *uuid.UUID `bun:"avatar_file_id"`
		}
		var profiles []profileRow
		if err := a.db.NewSelect().Table("members").
			ColumnExpr("id::text AS id, name, avatar_file_id").
			Where("id IN (?)", bun.List(ownerIDs)).
			Scan(ctx, &profiles); err != nil {
			return nil, errors.Wrap(err, "load owner profiles for business report")
		}
		for _, p := range profiles {
			o := ownerMap[p.ID]
			if o == nil {
				continue
			}
			o.Name = p.Name
			if a.files != nil {
				url, err := account.AvatarURL(ctx, a.files, p.AvatarFileID)
				if err != nil {
					return nil, err
				}
				o.AvatarURL = url
			}
		}
		statusMap, err := member.StatusesOf(ctx, a.db, sub.Tenant, ownerIDs)
		if err != nil {
			return nil, err
		}
		for id, o := range ownerMap {
			if !o.Removed {
				o.OnlineStatus = string(statusMap[id])
			}
			byOwner = append(byOwner, *o)
		}
		slices.SortFunc(byOwner, func(x, y BusinessOwnerMetrics) int {
			xMsgs := x.MessagesInbound + x.MessagesOutbound
			yMsgs := y.MessagesInbound + y.MessagesOutbound
			if xMsgs != yMsgs {
				if xMsgs > yMsgs {
					return -1
				}
				return 1
			}
			if x.ActiveChats != y.ActiveChats {
				if x.ActiveChats > y.ActiveChats {
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

	// 8. 商务沟通对方语言分布（按统计期内有消息活动的会话聚合，避免期末之后的新消息导致历史区间漏算）
	byLanguage := []LanguageCount{}
	qByLang := a.db.NewSelect().TableExpr("conversations c").
		Join("JOIN chats ch ON ch.conversation_id = c.id").
		Join("LEFT JOIN channels chan_t ON chan_t.id = ch.channel_id").
		ColumnExpr("COALESCE(NULLIF(c.language, ''), 'und') AS language, count(*) AS count").
		Where("c.tenant_id = ? AND c.kind = ? AND ch.scope = ?", sub.Tenant, conversation.Chat, conversation.ChatExternal).
		Where("EXISTS (SELECT 1 FROM timeline_items it WHERE it.conversation_id = c.id AND it.tenant_id = c.tenant_id AND it.kind = ? AND it.created_at >= ? AND it.created_at < ?)",
			conversation.Message, w.Start, w.End).
		GroupExpr("1").
		OrderExpr("count DESC, 1 ASC").
		Limit(12)
	applyBusinessFilters(qByLang, f)
	if err := qByLang.Scan(ctx, &byLanguage); err != nil {
		return nil, errors.Wrap(err, "report business by language")
	}
	if byLanguage == nil {
		byLanguage = []LanguageCount{}
	}

	return &BusinessReport{
		Period:        PeriodMeta(w.Meta()),
		Summary:       sum,
		Series:        series,
		ByChannelKind: byChannelKind,
		ByOwner:       byOwner,
		ByLanguage:    byLanguage,
	}, nil
}

// HTTP 处理 GET /t/{slug}/api/reports/business。
func (a *Business) HTTP(ctx context.Context, in *BusinessFilterParams) (*BusinessOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &BusinessOutput{Body: *res}, nil
}
