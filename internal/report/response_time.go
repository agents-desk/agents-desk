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

// DurationStats 是一类响应时长的统计摘要（单位：秒）：中位数、均值、P90、样本数与上一对比期中位数。
type DurationStats struct {
	Samples           int64    `json:"samples"`
	MedianSeconds     *float64 `json:"median_seconds,omitempty"`
	AvgSeconds        *float64 `json:"avg_seconds,omitempty"`
	P90Seconds        *float64 `json:"p90_seconds,omitempty"`
	PrevSamples       int64    `json:"prev_samples"`
	PrevMedianSeconds *float64 `json:"prev_median_seconds,omitempty"`
}

// ResponseTimePoint 是响应时间趋势图中的一个时间桶。
type ResponseTimePoint struct {
	Date              string   `json:"date"`
	FirstReplySamples int64    `json:"first_reply_samples"`
	FirstReplyMedian  *float64 `json:"first_reply_median,omitempty"`
	ResolutionSamples int64    `json:"resolution_samples"`
	ResolutionMedian  *float64 `json:"resolution_median,omitempty"`
}

// DurationBucket 是时长分布直方图中的一个区间桶。
type DurationBucket struct {
	Key   string `json:"key" doc:"区间标识：lt_5m、5m_15m、15m_1h、1h_4h、4h_24h、gt_24h"`
	Count int64  `json:"count"`
}

// TeamResponseTime 是按团队收件箱对比的响应时长。
type TeamResponseTime struct {
	ID                *string  `json:"id,omitempty"`
	Name              string   `json:"name"`
	Disabled          bool     `json:"disabled"`
	FirstReplySamples int64    `json:"first_reply_samples"`
	FirstReplyMedian  *float64 `json:"first_reply_median,omitempty"`
	FirstReplyAvg     *float64 `json:"first_reply_avg,omitempty"`
	ResolutionSamples int64    `json:"resolution_samples"`
	ResolutionMedian  *float64 `json:"resolution_median,omitempty"`
	ResolutionAvg     *float64 `json:"resolution_avg,omitempty"`
}

// ChannelResponseTime 是按渠道对比的响应时长。
type ChannelResponseTime struct {
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	Kind              channel.Kind `json:"kind"`
	Enabled           bool         `json:"enabled"`
	FirstReplySamples int64        `json:"first_reply_samples"`
	FirstReplyMedian  *float64     `json:"first_reply_median,omitempty"`
	FirstReplyAvg     *float64     `json:"first_reply_avg,omitempty"`
	ResolutionSamples int64        `json:"resolution_samples"`
	ResolutionMedian  *float64     `json:"resolution_median,omitempty"`
	ResolutionAvg     *float64     `json:"resolution_avg,omitempty"`
}

// ResponseTimeReport 是响应时间报表的完整响应。
type ResponseTimeReport struct {
	Period            PeriodMeta            `json:"period"`
	FirstReply        DurationStats         `json:"first_reply"`
	Resolution        DurationStats         `json:"resolution"`
	Series            []ResponseTimePoint   `json:"series" nullable:"false"`
	FirstReplyBuckets []DurationBucket      `json:"first_reply_buckets" nullable:"false"`
	ResolutionBuckets []DurationBucket      `json:"resolution_buckets" nullable:"false"`
	ByTeam            []TeamResponseTime    `json:"by_team" nullable:"false"`
	ByChannel         []ChannelResponseTime `json:"by_channel" nullable:"false"`
}

// ResponseTimeOutput 是响应时间报表的 HTTP 响应。
type ResponseTimeOutput struct {
	Body ResponseTimeReport
}

// ResponseTime 基于 conversation_rounds 统计首次回复时长（FRT）与解决时长（均按自然日历秒计算）。
type ResponseTime struct {
	db *bun.DB
}

// NewResponseTime 返回响应时间报表动作。
func NewResponseTime(db *bun.DB) *ResponseTime { return &ResponseTime{db: db} }

// durationBucketOrder 定义直方图固定展示顺序。
var durationBucketOrder = []string{"lt_5m", "5m_15m", "15m_1h", "1h_4h", "4h_24h", "gt_24h"}

// durationBucketCase 返回把秒数表达式 expr 映射到区间标识的 PostgreSQL CASE 表达式。
func durationBucketCase(expr string) string {
	return "CASE " +
		"WHEN " + expr + " < 300 THEN 'lt_5m' " +
		"WHEN " + expr + " < 900 THEN '5m_15m' " +
		"WHEN " + expr + " < 3600 THEN '15m_1h' " +
		"WHEN " + expr + " < 14400 THEN '1h_4h' " +
		"WHEN " + expr + " < 86400 THEN '4h_24h' " +
		"ELSE 'gt_24h' END"
}

// Handle 返回 sub 所在工作区按筛选条件 f 统计的响应时间报表。
func (a *ResponseTime) Handle(ctx context.Context, sub identity.Subject, f FilterParams) (*ResponseTimeReport, error) {
	w, err := resolveWindow(ctx, a.db, sub, f)
	if err != nil {
		return nil, err
	}

	frtExpr := "GREATEST(EXTRACT(EPOCH FROM (r.first_reply_at - r.opened_at)), 0)"
	resExpr := "GREATEST(EXTRACT(EPOCH FROM (r.closed_at - r.opened_at)), 0)"

	// 1. 首次回复时长（主周期 + 上一对比期）
	frtStats, err := a.durationStats(ctx, sub.Tenant, f, "r.first_reply_at", frtExpr, w)
	if err != nil {
		return nil, err
	}

	// 2. 解决时长（主周期 + 上一对比期）
	resStats, err := a.durationStats(ctx, sub.Tenant, f, "r.closed_at", resExpr, w)
	if err != nil {
		return nil, err
	}

	// 3. 趋势序列（中位数与样本量，按连续时间桶补零）
	byDate := map[string]ResponseTimePoint{}
	type seriesRow struct {
		Bucket  string   `bun:"bucket"`
		Samples int64    `bun:"samples"`
		Median  *float64 `bun:"median"`
	}
	var frtSeries []seriesRow
	qFRTSeries := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr(w.BucketExpr("r.first_reply_at")+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") AS median").
		Where("r.tenant_id = ? AND c.kind = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("1")
	applyRoundFilters(qFRTSeries, f)
	if err := qFRTSeries.Scan(ctx, &frtSeries); err != nil {
		return nil, errors.Wrap(err, "report frt series")
	}
	for _, r := range frtSeries {
		p := byDate[r.Bucket]
		p.Date = r.Bucket
		p.FirstReplySamples = r.Samples
		p.FirstReplyMedian = roundPtr(r.Median)
		byDate[r.Bucket] = p
	}

	var resSeries []seriesRow
	qResSeries := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr(w.BucketExpr("r.closed_at")+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+resExpr+") AS median").
		Where("r.tenant_id = ? AND c.kind = ? AND r.closed_at >= ? AND r.closed_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("1")
	applyRoundFilters(qResSeries, f)
	if err := qResSeries.Scan(ctx, &resSeries); err != nil {
		return nil, errors.Wrap(err, "report resolution series")
	}
	for _, r := range resSeries {
		p := byDate[r.Bucket]
		p.Date = r.Bucket
		p.ResolutionSamples = r.Samples
		p.ResolutionMedian = roundPtr(r.Median)
		byDate[r.Bucket] = p
	}

	series := period.FillSeriesMap(w, byDate, func(b string) ResponseTimePoint {
		return ResponseTimePoint{Date: b}
	})

	// 4. 时长分桶直方图
	frtBuckets, err := a.durationBuckets(ctx, sub.Tenant, f, "r.first_reply_at", frtExpr, w)
	if err != nil {
		return nil, err
	}
	resBuckets, err := a.durationBuckets(ctx, sub.Tenant, f, "r.closed_at", resExpr, w)
	if err != nil {
		return nil, err
	}

	// 5. 按团队与渠道拆解
	byTeam, err := a.byTeam(ctx, sub.Tenant, f, frtExpr, resExpr, w)
	if err != nil {
		return nil, err
	}
	byChannel, err := a.byChannel(ctx, sub.Tenant, f, frtExpr, resExpr, w)
	if err != nil {
		return nil, err
	}

	return &ResponseTimeReport{
		Period:            PeriodMeta(w.Meta()),
		FirstReply:        frtStats,
		Resolution:        resStats,
		Series:            series,
		FirstReplyBuckets: frtBuckets,
		ResolutionBuckets: resBuckets,
		ByTeam:            byTeam,
		ByChannel:         byChannel,
	}, nil
}

// durationStats 计算给定时间列 timeCol 和时长秒数表达式 durExpr 在主周期与上一对比期的分位数摘要。
func (a *ResponseTime) durationStats(ctx context.Context, tenantID string, f FilterParams, timeCol, durExpr string, w period.Window) (DurationStats, error) {
	var out DurationStats
	qCur := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+durExpr+") AS median").
		ColumnExpr("avg("+durExpr+") AS avg").
		ColumnExpr("percentile_cont(0.9) WITHIN GROUP (ORDER BY "+durExpr+") AS p90").
		Where("r.tenant_id = ? AND c.kind = ? AND "+timeCol+" >= ? AND "+timeCol+" < ?", tenantID, conversation.Service, w.Start, w.End)
	applyRoundFilters(qCur, f)
	if err := qCur.Scan(ctx, &out.Samples, &out.MedianSeconds, &out.AvgSeconds, &out.P90Seconds); err != nil {
		return DurationStats{}, errors.Wrap(err, "report duration stats")
	}

	qPrev := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+durExpr+") AS median").
		Where("r.tenant_id = ? AND c.kind = ? AND "+timeCol+" >= ? AND "+timeCol+" < ?", tenantID, conversation.Service, w.PrevStart, w.PrevEnd)
	applyRoundFilters(qPrev, f)
	if err := qPrev.Scan(ctx, &out.PrevSamples, &out.PrevMedianSeconds); err != nil {
		return DurationStats{}, errors.Wrap(err, "report prev duration stats")
	}
	out.MedianSeconds = roundPtr(out.MedianSeconds)
	out.AvgSeconds = roundPtr(out.AvgSeconds)
	out.P90Seconds = roundPtr(out.P90Seconds)
	out.PrevMedianSeconds = roundPtr(out.PrevMedianSeconds)
	return out, nil
}

// durationBuckets 按 6 个固定时长区间统计落入各区间的样本数。
func (a *ResponseTime) durationBuckets(ctx context.Context, tenantID string, f FilterParams, timeCol, durExpr string, w period.Window) ([]DurationBucket, error) {
	type row struct {
		Key   string `bun:"k"`
		Count int64  `bun:"cnt"`
	}
	var rows []row
	q := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr(durationBucketCase(durExpr)+" AS k").
		ColumnExpr("count(*) AS cnt").
		Where("r.tenant_id = ? AND c.kind = ? AND "+timeCol+" >= ? AND "+timeCol+" < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("1")
	applyRoundFilters(q, f)
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, errors.Wrap(err, "report duration buckets")
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.Key] = r.Count
	}
	out := make([]DurationBucket, len(durationBucketOrder))
	for i, k := range durationBucketOrder {
		out[i] = DurationBucket{Key: k, Count: counts[k]}
	}
	return out, nil
}

// byTeam 按团队收件箱拆解首次回复与解决时长。
func (a *ResponseTime) byTeam(ctx context.Context, tenantID string, f FilterParams, frtExpr, resExpr string, w period.Window) ([]TeamResponseTime, error) {
	type teamMeta struct {
		ID       string `bun:"id"`
		Name     string `bun:"name"`
		Disabled bool   `bun:"disabled"`
	}
	var teams []teamMeta
	qT := a.db.NewSelect().Table("teams").
		ColumnExpr("id::text AS id, name, (disabled_at IS NOT NULL) AS disabled").
		Where("tenant_id = ?", tenantID).
		OrderExpr("disabled_at IS NOT NULL ASC, name ASC")
	if f.TeamID != "" {
		qT.Where("id = ?", f.TeamID)
	}
	if err := qT.Scan(ctx, &teams); err != nil {
		return nil, errors.Wrap(err, "load teams for response time")
	}
	byID := make(map[string]*TeamResponseTime, len(teams))
	order := make([]string, 0, len(teams))
	for _, t := range teams {
		id := t.ID
		byID[id] = &TeamResponseTime{ID: &id, Name: t.Name, Disabled: t.Disabled}
		order = append(order, id)
	}
	unassigned := &TeamResponseTime{}

	type aggRow struct {
		ID      *string  `bun:"id"`
		Samples int64    `bun:"samples"`
		Median  *float64 `bun:"median"`
		Avg     *float64 `bun:"avg"`
	}
	var frtRows []aggRow
	qFRT := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("r.team_id::text AS id, count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") AS median").
		ColumnExpr("avg("+frtExpr+") AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.first_reply_at >= ? AND r.first_reply_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("r.team_id")
	applyRoundFilters(qFRT, f)
	if err := qFRT.Scan(ctx, &frtRows); err != nil {
		return nil, errors.Wrap(err, "report frt by team")
	}
	for _, r := range frtRows {
		target := unassigned
		if r.ID != nil {
			target = byID[*r.ID]
		}
		if target != nil {
			target.FirstReplySamples = r.Samples
			target.FirstReplyMedian = roundPtr(r.Median)
			target.FirstReplyAvg = roundPtr(r.Avg)
		}
	}

	var resRows []aggRow
	qRes := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("r.team_id::text AS id, count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+resExpr+") AS median").
		ColumnExpr("avg("+resExpr+") AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.closed_at >= ? AND r.closed_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("r.team_id")
	applyRoundFilters(qRes, f)
	if err := qRes.Scan(ctx, &resRows); err != nil {
		return nil, errors.Wrap(err, "report resolution by team")
	}
	for _, r := range resRows {
		target := unassigned
		if r.ID != nil {
			target = byID[*r.ID]
		}
		if target != nil {
			target.ResolutionSamples = r.Samples
			target.ResolutionMedian = roundPtr(r.Median)
			target.ResolutionAvg = roundPtr(r.Avg)
		}
	}

	out := make([]TeamResponseTime, 0, len(order)+1)
	for _, id := range order {
		v := byID[id]
		if v.FirstReplySamples > 0 || v.ResolutionSamples > 0 || !v.Disabled {
			out = append(out, *v)
		}
	}
	if f.TeamID == "" && (unassigned.FirstReplySamples > 0 || unassigned.ResolutionSamples > 0) {
		out = append(out, *unassigned)
	}
	return out, nil
}

// byChannel 按渠道拆解首次回复与解决时长。
func (a *ResponseTime) byChannel(ctx context.Context, tenantID string, f FilterParams, frtExpr, resExpr string, w period.Window) ([]ChannelResponseTime, error) {
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
		return nil, errors.Wrap(err, "load channels for response time")
	}
	byID := make(map[string]*ChannelResponseTime, len(channels))
	order := make([]string, 0, len(channels))
	for _, ch := range channels {
		byID[ch.ID] = &ChannelResponseTime{ID: ch.ID, Name: ch.Name, Kind: ch.Kind, Enabled: ch.Enabled}
		order = append(order, ch.ID)
	}

	type aggRow struct {
		ID      string   `bun:"id"`
		Samples int64    `bun:"samples"`
		Median  *float64 `bun:"median"`
		Avg     *float64 `bun:"avg"`
	}
	var frtRows []aggRow
	qFRT := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("c.channel_id::text AS id, count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+frtExpr+") AS median").
		ColumnExpr("avg("+frtExpr+") AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND c.channel_id IS NOT NULL AND r.first_reply_at >= ? AND r.first_reply_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("c.channel_id")
	applyRoundFilters(qFRT, f)
	if err := qFRT.Scan(ctx, &frtRows); err != nil {
		return nil, errors.Wrap(err, "report frt by channel")
	}
	for _, r := range frtRows {
		if v, ok := byID[r.ID]; ok {
			v.FirstReplySamples = r.Samples
			v.FirstReplyMedian = roundPtr(r.Median)
			v.FirstReplyAvg = roundPtr(r.Avg)
		}
	}

	var resRows []aggRow
	qRes := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("c.channel_id::text AS id, count(*) AS samples").
		ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY "+resExpr+") AS median").
		ColumnExpr("avg("+resExpr+") AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND c.channel_id IS NOT NULL AND r.closed_at >= ? AND r.closed_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("c.channel_id")
	applyRoundFilters(qRes, f)
	if err := qRes.Scan(ctx, &resRows); err != nil {
		return nil, errors.Wrap(err, "report resolution by channel")
	}
	for _, r := range resRows {
		if v, ok := byID[r.ID]; ok {
			v.ResolutionSamples = r.Samples
			v.ResolutionMedian = roundPtr(r.Median)
			v.ResolutionAvg = roundPtr(r.Avg)
		}
	}

	out := make([]ChannelResponseTime, 0, len(order))
	for _, id := range order {
		v := byID[id]
		if v.FirstReplySamples > 0 || v.ResolutionSamples > 0 || v.Enabled {
			out = append(out, *v)
		}
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/reports/response-time。
func (a *ResponseTime) HTTP(ctx context.Context, in *FilterParams) (*ResponseTimeOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &ResponseTimeOutput{Body: *res}, nil
}
