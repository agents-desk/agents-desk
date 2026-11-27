package report

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
)

// ConversationsSummary 是会话报表的核心汇总指标与当前积压快照。
type ConversationsSummary struct {
	Created          int64 `json:"created" doc:"统计期内新建的服务型会话数"`
	PrevCreated      int64 `json:"prev_created" doc:"上一对比期新建的服务型会话数"`
	DirectCreated    int64 `json:"direct_created" doc:"统计期内新建的单聊会话数"`
	GroupCreated     int64 `json:"group_created" doc:"统计期内新建的外部群会话数"`
	RoundsOpened     int64 `json:"rounds_opened" doc:"统计期内开启的服务轮次数（新建 + 重开）"`
	PrevRoundsOpened int64 `json:"prev_rounds_opened" doc:"上一对比期开启的服务轮次数"`
	Reopened         int64 `json:"reopened" doc:"统计期内重新打开的轮次数（round > 1）"`
	PrevReopened     int64 `json:"prev_reopened" doc:"上一对比期重新打开的轮次数"`
	Closed           int64 `json:"closed" doc:"统计期内关闭的服务轮次数"`
	PrevClosed       int64 `json:"prev_closed" doc:"上一对比期关闭的服务轮次数"`
	OpenNow          int64 `json:"open_now" doc:"当前处于打开状态的会话数（实时快照）"`
	SnoozedNow       int64 `json:"snoozed_now" doc:"当前处于稍后处理状态的会话数（实时快照）"`
	UnassignedNow    int64 `json:"unassigned_now" doc:"当前未关闭且无负责人的会话数（实时快照）"`
	WaitingNow       int64 `json:"waiting_now" doc:"当前未关闭且正在等待客服回复的会话数（实时快照）"`
}

// ConversationsPoint 是会话趋势图中的一个时间桶。
type ConversationsPoint struct {
	Date     string `json:"date"`
	Created  int64  `json:"created"`
	Reopened int64  `json:"reopened"`
	Closed   int64  `json:"closed"`
}

// ChannelVolume 是按渠道拆分的会话量。
type ChannelVolume struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Kind    channel.Kind `json:"kind"`
	Enabled bool         `json:"enabled"`
	Created int64        `json:"created"`
	Closed  int64        `json:"closed"`
}

// TeamVolume 是按团队收件箱拆分的会话量；ID 为空表示未归属任何团队收件箱。
type TeamVolume struct {
	ID       *string `json:"id,omitempty"`
	Name     string  `json:"name"`
	Disabled bool    `json:"disabled"`
	Created  int64   `json:"created"`
	Closed   int64   `json:"closed"`
}

// TagVolume 是按标签拆分的会话量。
type TagVolume struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Count int64  `json:"count"`
}

// ConversationsReport 是会话报表的完整响应。
type ConversationsReport struct {
	Period     PeriodMeta           `json:"period"`
	Summary    ConversationsSummary `json:"summary"`
	Series     []ConversationsPoint `json:"series" nullable:"false"`
	ByChannel  []ChannelVolume      `json:"by_channel" nullable:"false"`
	ByTeam     []TeamVolume         `json:"by_team" nullable:"false"`
	ByTag      []TagVolume          `json:"by_tag" nullable:"false"`
	ByLanguage []LanguageCount      `json:"by_language" nullable:"false"`
}

// ConversationsOutput 是会话报表的 HTTP 响应。
type ConversationsOutput struct {
	Body ConversationsReport
}

// Conversations 汇总工作区的会话量、趋势、积压快照和维度分布。
type Conversations struct {
	db *bun.DB
}

// NewConversations 返回会话报表动作。
func NewConversations(db *bun.DB) *Conversations { return &Conversations{db: db} }

// Handle 返回 sub 所在工作区按筛选条件 f 统计的会话报表。
func (a *Conversations) Handle(ctx context.Context, sub identity.Subject, f FilterParams) (*ConversationsReport, error) {
	w, err := resolveWindow(ctx, a.db, sub, f)
	if err != nil {
		return nil, err
	}

	var sum ConversationsSummary

	// 1. 新建会话数（主周期 + 上一对比期 + 单聊/外部群拆解）
	qCreated := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ?) AS prev_created", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ? AND c.external_group_id IS NULL) AS direct_created", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE c.created_at >= ? AND c.created_at < ? AND c.external_group_id IS NOT NULL) AS group_created", w.Start, w.End).
		Where("c.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", sub.Tenant, conversation.Service, w.PrevStart, w.End)
	applyConvFilters(qCreated, f)
	if err := qCreated.Scan(ctx, &sum.Created, &sum.PrevCreated, &sum.DirectCreated, &sum.GroupCreated); err != nil {
		return nil, errors.Wrap(err, "report conversations created summary")
	}

	// 2. 开启/重开/关闭轮次数（主周期 + 上一对比期）
	qRounds := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("count(*) FILTER (WHERE r.opened_at >= ? AND r.opened_at < ?) AS rounds_opened", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE r.opened_at >= ? AND r.opened_at < ?) AS prev_rounds_opened", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE r.opened_at >= ? AND r.opened_at < ? AND r.round > 1) AS reopened", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE r.opened_at >= ? AND r.opened_at < ? AND r.round > 1) AS prev_reopened", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE r.closed_at >= ? AND r.closed_at < ?) AS closed", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE r.closed_at >= ? AND r.closed_at < ?) AS prev_closed", w.PrevStart, w.PrevEnd).
		Where("r.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Service).
		Where("(r.opened_at >= ? AND r.opened_at < ?) OR (r.closed_at >= ? AND r.closed_at < ?)", w.PrevStart, w.End, w.PrevStart, w.End)
	applyRoundFilters(qRounds, f)
	if err := qRounds.Scan(ctx, &sum.RoundsOpened, &sum.PrevRoundsOpened, &sum.Reopened, &sum.PrevReopened, &sum.Closed, &sum.PrevClosed); err != nil {
		return nil, errors.Wrap(err, "report conversations rounds summary")
	}

	// 3. 当前积压实时快照
	qBacklog := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("count(*) FILTER (WHERE c.status = ?) AS open_now", conversation.Open).
		ColumnExpr("count(*) FILTER (WHERE c.status = ?) AS snoozed_now", conversation.Snoozed).
		ColumnExpr("count(*) FILTER (WHERE c.status <> ? AND c.assignee_id = '') AS unassigned_now", conversation.Closed).
		ColumnExpr("count(*) FILTER (WHERE c.status <> ? AND c.waiting_since IS NOT NULL) AS waiting_now", conversation.Closed).
		Where("c.tenant_id = ? AND c.kind = ? AND c.status <> ?", sub.Tenant, conversation.Service, conversation.Closed)
	applyConvFilters(qBacklog, f)
	if err := qBacklog.Scan(ctx, &sum.OpenNow, &sum.SnoozedNow, &sum.UnassignedNow, &sum.WaitingNow); err != nil {
		return nil, errors.Wrap(err, "report conversations backlog")
	}

	// 4. 按时间桶汇总趋势：新建、重开与关闭合并后按完整时间桶补零
	type bucketVal struct {
		Bucket string `bun:"bucket"`
		Count  int64  `bun:"cnt"`
	}
	byDate := map[string]*ConversationsPoint{}
	touch := func(b string) *ConversationsPoint {
		p, ok := byDate[b]
		if !ok {
			p = &ConversationsPoint{Date: b}
			byDate[b] = p
		}
		return p
	}

	var createdRows []bucketVal
	qCreatedSeries := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr(bucketExpr("c.created_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("1")
	applyConvFilters(qCreatedSeries, f)
	if err := qCreatedSeries.Scan(ctx, &createdRows); err != nil {
		return nil, errors.Wrap(err, "report conversations created series")
	}
	for _, r := range createdRows {
		touch(r.Bucket).Created = r.Count
	}

	var reopenedRows []bucketVal
	qReopenedSeries := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr(bucketExpr("r.opened_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS cnt").
		Where("r.tenant_id = ? AND c.kind = ? AND r.round > 1 AND r.opened_at >= ? AND r.opened_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("1")
	applyRoundFilters(qReopenedSeries, f)
	if err := qReopenedSeries.Scan(ctx, &reopenedRows); err != nil {
		return nil, errors.Wrap(err, "report conversations reopened series")
	}
	for _, r := range reopenedRows {
		touch(r.Bucket).Reopened = r.Count
	}

	var closedRows []bucketVal
	qClosedSeries := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr(bucketExpr("r.closed_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS cnt").
		Where("r.tenant_id = ? AND c.kind = ? AND r.closed_at >= ? AND r.closed_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("1")
	applyRoundFilters(qClosedSeries, f)
	if err := qClosedSeries.Scan(ctx, &closedRows); err != nil {
		return nil, errors.Wrap(err, "report conversations closed series")
	}
	for _, r := range closedRows {
		touch(r.Bucket).Closed = r.Count
	}

	sparse := make([]ConversationsPoint, 0, len(byDate))
	for _, p := range byDate {
		sparse = append(sparse, *p)
	}
	series := period.FillSeries(w, sparse,
		func(p ConversationsPoint) string { return p.Date },
		func(b string) ConversationsPoint { return ConversationsPoint{Date: b} },
	)

	// 5. 按渠道分布
	byChannel, err := a.channelVolumes(ctx, sub.Tenant, w, f)
	if err != nil {
		return nil, err
	}

	// 6. 按团队收件箱分布（含停用团队名称与未归属团队）
	byTeam, err := a.teamVolumes(ctx, sub.Tenant, w, f)
	if err != nil {
		return nil, err
	}

	// 7. 按标签分布（统计期内新建会话上的标签 Top 10）
	byTag := []TagVolume{}
	qTags := a.db.NewSelect().TableExpr("conversation_tags ct").
		Join("JOIN conversations c ON c.id = ct.conversation_id").
		Join("JOIN tags tg ON tg.id = ct.tag_id").
		ColumnExpr("tg.id::text AS id, tg.name AS name, tg.color AS color, count(*) AS count").
		Where("ct.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("tg.id, tg.name, tg.color").
		OrderExpr("count DESC, tg.name ASC").
		Limit(10)
	applyConvFilters(qTags, f)
	if err := qTags.Scan(ctx, &byTag); err != nil {
		return nil, errors.Wrap(err, "report conversations by tag")
	}
	if byTag == nil {
		byTag = []TagVolume{}
	}

	// 8. 按客户语言分布（统计期内新建会话）
	byLang := []LanguageCount{}
	qLang := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("COALESCE(NULLIF(c.language, ''), 'und') AS language, count(*) AS count").
		Where("c.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("1").
		OrderExpr("count DESC, 1 ASC").
		Limit(12)
	applyConvFilters(qLang, f)
	if err := qLang.Scan(ctx, &byLang); err != nil {
		return nil, errors.Wrap(err, "report conversations by language")
	}
	if byLang == nil {
		byLang = []LanguageCount{}
	}

	return &ConversationsReport{
		Period:     PeriodMeta(w.Meta()),
		Summary:    sum,
		Series:     series,
		ByChannel:  byChannel,
		ByTeam:     byTeam,
		ByTag:      byTag,
		ByLanguage: byLang,
	}, nil
}

// channelVolumes 按渠道统计新建会话与关闭轮次数。
func (a *Conversations) channelVolumes(ctx context.Context, tenantID string, w period.Window, f FilterParams) ([]ChannelVolume, error) {
	var channels []channel.Channel
	qCh := a.db.NewSelect().Model(&channels).
		Column("id", "name", "kind", "enabled").
		Where("tenant_id = ? AND owner_member_id IS NULL", tenantID).
		OrderExpr("created_at ASC, id ASC")
	if f.Audience != "" {
		qCh.Where("audience = ?", f.Audience)
	}
	if f.ChannelID != "" {
		qCh.Where("id = ?", f.ChannelID)
	}
	if err := qCh.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load channels for report")
	}
	byID := make(map[string]*ChannelVolume, len(channels))
	order := make([]string, 0, len(channels))
	for _, ch := range channels {
		byID[ch.ID] = &ChannelVolume{ID: ch.ID, Name: ch.Name, Kind: ch.Kind, Enabled: ch.Enabled}
		order = append(order, ch.ID)
	}

	type idCount struct {
		ID    string `bun:"id"`
		Count int64  `bun:"cnt"`
	}
	var created []idCount
	qC := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("c.channel_id::text AS id, count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND c.channel_id IS NOT NULL AND c.created_at >= ? AND c.created_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("c.channel_id")
	applyConvFilters(qC, f)
	if err := qC.Scan(ctx, &created); err != nil {
		return nil, errors.Wrap(err, "report channel created")
	}
	for _, r := range created {
		if v, ok := byID[r.ID]; ok {
			v.Created = r.Count
		}
	}

	var closed []idCount
	qCl := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("c.channel_id::text AS id, count(*) AS cnt").
		Where("r.tenant_id = ? AND c.kind = ? AND c.channel_id IS NOT NULL AND r.closed_at >= ? AND r.closed_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("c.channel_id")
	applyRoundFilters(qCl, f)
	if err := qCl.Scan(ctx, &closed); err != nil {
		return nil, errors.Wrap(err, "report channel closed")
	}
	for _, r := range closed {
		if v, ok := byID[r.ID]; ok {
			v.Closed = r.Count
		}
	}

	out := make([]ChannelVolume, 0, len(order))
	for _, id := range order {
		v := byID[id]
		if v.Created > 0 || v.Closed > 0 || v.Enabled {
			out = append(out, *v)
		}
	}
	return out, nil
}

// teamVolumes 按团队收件箱统计新建会话与关闭轮次数，保留已停用团队历史并包含未归属团队的项。
func (a *Conversations) teamVolumes(ctx context.Context, tenantID string, w period.Window, f FilterParams) ([]TeamVolume, error) {
	type teamRow struct {
		ID       string `bun:"id"`
		Name     string `bun:"name"`
		Disabled bool   `bun:"disabled"`
	}
	var teams []teamRow
	qT := a.db.NewSelect().Table("teams").
		ColumnExpr("id::text AS id, name, (disabled_at IS NOT NULL) AS disabled").
		Where("tenant_id = ?", tenantID).
		OrderExpr("disabled_at IS NOT NULL ASC, name ASC")
	if f.TeamID != "" {
		qT.Where("id = ?", f.TeamID)
	}
	if err := qT.Scan(ctx, &teams); err != nil {
		return nil, errors.Wrap(err, "load teams for report")
	}
	byID := make(map[string]*TeamVolume, len(teams)+1)
	order := make([]string, 0, len(teams)+1)
	for _, t := range teams {
		id := t.ID
		byID[id] = &TeamVolume{ID: &id, Name: t.Name, Disabled: t.Disabled}
		order = append(order, id)
	}
	unassigned := &TeamVolume{}

	type keyCount struct {
		ID    *string `bun:"id"`
		Count int64   `bun:"cnt"`
	}
	var created []keyCount
	qC := a.db.NewSelect().TableExpr("conversations c").
		ColumnExpr("c.team_id::text AS id, count(*) AS cnt").
		Where("c.tenant_id = ? AND c.kind = ? AND c.created_at >= ? AND c.created_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("c.team_id")
	applyConvFilters(qC, f)
	if err := qC.Scan(ctx, &created); err != nil {
		return nil, errors.Wrap(err, "report team created")
	}
	for _, r := range created {
		if r.ID == nil {
			unassigned.Created = r.Count
		} else if v, ok := byID[*r.ID]; ok {
			v.Created = r.Count
		}
	}

	var closed []keyCount
	qCl := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("r.team_id::text AS id, count(*) AS cnt").
		Where("r.tenant_id = ? AND c.kind = ? AND r.closed_at >= ? AND r.closed_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("r.team_id")
	applyRoundFilters(qCl, f)
	if err := qCl.Scan(ctx, &closed); err != nil {
		return nil, errors.Wrap(err, "report team closed")
	}
	for _, r := range closed {
		if r.ID == nil {
			unassigned.Closed = r.Count
		} else if v, ok := byID[*r.ID]; ok {
			v.Closed = r.Count
		}
	}

	out := make([]TeamVolume, 0, len(order)+1)
	for _, id := range order {
		v := byID[id]
		if v.Created > 0 || v.Closed > 0 || !v.Disabled {
			out = append(out, *v)
		}
	}
	if f.TeamID == "" && (unassigned.Created > 0 || unassigned.Closed > 0) {
		out = append(out, *unassigned)
	}
	return out, nil
}

// applyConvFilters 向以 conversations c 为主表的查询附加未标垃圾条件以及服务对象、团队与渠道条件。
func applyConvFilters(q *bun.SelectQuery, f FilterParams) {
	q.Where("c.spam_at IS NULL")
	if f.Audience != "" {
		q.Where("c.audience = ?", f.Audience)
	}
	if f.TeamID != "" {
		q.Where("c.team_id = ?", f.TeamID)
	}
	if f.ChannelID != "" {
		q.Where("c.channel_id = ?", f.ChannelID)
	}
}

// applyRoundFilters 向以 conversation_rounds r JOIN conversations c 为主表的查询附加未标垃圾条件以及服务对象、团队与渠道条件。
func applyRoundFilters(q *bun.SelectQuery, f FilterParams) {
	q.Where("c.spam_at IS NULL")
	if f.Audience != "" {
		q.Where("c.audience = ?", f.Audience)
	}
	if f.TeamID != "" {
		q.Where("r.team_id = ?", f.TeamID)
	}
	if f.ChannelID != "" {
		q.Where("c.channel_id = ?", f.ChannelID)
	}
}

// HTTP 处理 GET /t/{slug}/api/reports/conversations。
func (a *Conversations) HTTP(ctx context.Context, in *FilterParams) (*ConversationsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &ConversationsOutput{Body: *res}, nil
}
