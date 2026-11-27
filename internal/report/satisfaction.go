package report

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
)

// SatisfactionSummary 是满意度报表的核心汇总指标。
type SatisfactionSummary struct {
	RatedCount     int64    `json:"rated_count" doc:"统计期内收到的客户评价数"`
	PrevRatedCount int64    `json:"prev_rated_count" doc:"上一对比期收到的评价数"`
	PositiveCount  int64    `json:"positive_count" doc:"好评数（4 星与 5 星）"`
	CSAT           *float64 `json:"csat,omitempty" doc:"满意度百分比（4–5 星占比，0–100）"`
	PrevCSAT       *float64 `json:"prev_csat,omitempty" doc:"上一对比期满意度百分比"`
	AvgScore       *float64 `json:"avg_score,omitempty" doc:"平均评分（1–5）"`
	PrevAvgScore   *float64 `json:"prev_avg_score,omitempty" doc:"上一对比期平均评分"`
	ClosedCount    int64    `json:"closed_count" doc:"统计期内关闭的可评价服务轮次数（外部客户单聊）"`
	ResponseRate   *float64 `json:"response_rate,omitempty" doc:"参评率百分比（评价数 / 可评价关闭轮次数 × 100）"`
}

// ScoreCount 是某一星级的评价数量。
type ScoreCount struct {
	Score int   `json:"score" doc:"星级：1 到 5"`
	Count int64 `json:"count"`
}

// SatisfactionPoint 是满意度趋势图中的一个时间桶。
type SatisfactionPoint struct {
	Date          string   `json:"date"`
	RatedCount    int64    `json:"rated_count"`
	PositiveCount int64    `json:"positive_count"`
	CSAT          *float64 `json:"csat,omitempty"`
	AvgScore      *float64 `json:"avg_score,omitempty"`
}

// TeamSatisfaction 是按团队收件箱拆解的满意度。
type TeamSatisfaction struct {
	ID            *string  `json:"id,omitempty"`
	Name          string   `json:"name"`
	Disabled      bool     `json:"disabled"`
	RatedCount    int64    `json:"rated_count"`
	PositiveCount int64    `json:"positive_count"`
	CSAT          *float64 `json:"csat,omitempty"`
	AvgScore      *float64 `json:"avg_score,omitempty"`
}

// ChannelSatisfaction 是按渠道拆解的满意度。
type ChannelSatisfaction struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Kind          channel.Kind `json:"kind"`
	Enabled       bool         `json:"enabled"`
	RatedCount    int64        `json:"rated_count"`
	PositiveCount int64        `json:"positive_count"`
	CSAT          *float64     `json:"csat,omitempty"`
	AvgScore      *float64     `json:"avg_score,omitempty"`
}

// SatisfactionReport 是满意度报表的完整聚合响应。
type SatisfactionReport struct {
	Period       PeriodMeta            `json:"period"`
	Summary      SatisfactionSummary   `json:"summary"`
	Distribution []ScoreCount          `json:"distribution" nullable:"false"`
	Series       []SatisfactionPoint   `json:"series" nullable:"false"`
	ByTeam       []TeamSatisfaction    `json:"by_team" nullable:"false"`
	ByChannel    []ChannelSatisfaction `json:"by_channel" nullable:"false"`
}

// SatisfactionOutput 是满意度报表的 HTTP 响应。
type SatisfactionOutput struct {
	Body SatisfactionReport
}

// Satisfaction 汇总客户满意度（CSAT）、平均分、星级分布、趋势及团队/渠道拆解。
type Satisfaction struct {
	db *bun.DB
}

// NewSatisfaction 返回满意度报表动作。
func NewSatisfaction(db *bun.DB) *Satisfaction { return &Satisfaction{db: db} }

// Handle 返回 sub 所在工作区按筛选条件 f 统计的满意度报表（默认仅统计对外客户服务，参评率分母排除外部群）。
func (a *Satisfaction) Handle(ctx context.Context, sub identity.Subject, f FilterParams) (*SatisfactionReport, error) {
	if f.Audience == "" {
		f.Audience = channel.AudienceCustomer
	}
	w, err := resolveWindow(ctx, a.db, sub, f)
	if err != nil {
		return nil, err
	}

	var sum SatisfactionSummary
	var prevPositive int64
	qSum := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("count(*) FILTER (WHERE r.rated_at >= ? AND r.rated_at < ? AND r.rating IS NOT NULL) AS rated", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE r.rated_at >= ? AND r.rated_at < ? AND r.rating >= 4) AS pos", w.Start, w.End).
		ColumnExpr("avg(r.rating) FILTER (WHERE r.rated_at >= ? AND r.rated_at < ? AND r.rating IS NOT NULL) AS avg_score", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE r.rated_at >= ? AND r.rated_at < ? AND r.rating IS NOT NULL) AS prev_rated", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE r.rated_at >= ? AND r.rated_at < ? AND r.rating >= 4) AS prev_pos", w.PrevStart, w.PrevEnd).
		ColumnExpr("avg(r.rating) FILTER (WHERE r.rated_at >= ? AND r.rated_at < ? AND r.rating IS NOT NULL) AS prev_avg_score", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE r.closed_at >= ? AND r.closed_at < ? AND c.external_group_id IS NULL) AS closed", w.Start, w.End).
		Where("r.tenant_id = ? AND c.kind = ?", sub.Tenant, conversation.Service).
		Where("(r.rated_at >= ? AND r.rated_at < ?) OR (r.closed_at >= ? AND r.closed_at < ?)", w.PrevStart, w.End, w.Start, w.End)
	applyRoundFilters(qSum, f)
	if err := qSum.Scan(ctx, &sum.RatedCount, &sum.PositiveCount, &sum.AvgScore, &sum.PrevRatedCount, &prevPositive, &sum.PrevAvgScore, &sum.ClosedCount); err != nil {
		return nil, errors.Wrap(err, "report satisfaction summary")
	}
	sum.AvgScore = roundPtr(sum.AvgScore)
	sum.PrevAvgScore = roundPtr(sum.PrevAvgScore)
	sum.CSAT = pct(sum.PositiveCount, sum.RatedCount)
	sum.PrevCSAT = pct(prevPositive, sum.PrevRatedCount)
	sum.ResponseRate = pct(sum.RatedCount, sum.ClosedCount)

	// 1–5 星分布（按 5 到 1 固定输出）
	type scoreRow struct {
		Score int   `bun:"rating"`
		Count int64 `bun:"cnt"`
	}
	var scoreRows []scoreRow
	qDist := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr("r.rating AS rating, count(*) AS cnt").
		Where("r.tenant_id = ? AND c.kind = ? AND r.rating IS NOT NULL AND r.rated_at >= ? AND r.rated_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("r.rating")
	applyRoundFilters(qDist, f)
	if err := qDist.Scan(ctx, &scoreRows); err != nil {
		return nil, errors.Wrap(err, "report satisfaction distribution")
	}
	byScore := map[int]int64{}
	for _, r := range scoreRows {
		byScore[r.Score] = r.Count
	}
	dist := make([]ScoreCount, 5)
	for i := 0; i < 5; i++ {
		s := 5 - i
		dist[i] = ScoreCount{Score: s, Count: byScore[s]}
	}

	// 趋势序列
	type seriesRow struct {
		Bucket   string   `bun:"bucket"`
		Rated    int64    `bun:"rated"`
		Positive int64    `bun:"pos"`
		Avg      *float64 `bun:"avg"`
	}
	var sRows []seriesRow
	qSeries := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		ColumnExpr(bucketExpr("r.rated_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS rated, count(*) FILTER (WHERE r.rating >= 4) AS pos, avg(r.rating) AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.rating IS NOT NULL AND r.rated_at >= ? AND r.rated_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		GroupExpr("1")
	applyRoundFilters(qSeries, f)
	if err := qSeries.Scan(ctx, &sRows); err != nil {
		return nil, errors.Wrap(err, "report satisfaction series")
	}
	sparse := make([]SatisfactionPoint, len(sRows))
	for i, r := range sRows {
		sparse[i] = SatisfactionPoint{
			Date:          r.Bucket,
			RatedCount:    r.Rated,
			PositiveCount: r.Positive,
			CSAT:          pct(r.Positive, r.Rated),
			AvgScore:      roundPtr(r.Avg),
		}
	}
	series := period.FillSeries(w, sparse,
		func(p SatisfactionPoint) string { return p.Date },
		func(b string) SatisfactionPoint { return SatisfactionPoint{Date: b} },
	)

	byTeam, err := a.byTeam(ctx, sub.Tenant, w, f)
	if err != nil {
		return nil, err
	}
	byChannel, err := a.byChannel(ctx, sub.Tenant, w, f)
	if err != nil {
		return nil, err
	}

	return &SatisfactionReport{
		Period:       PeriodMeta(w.Meta()),
		Summary:      sum,
		Distribution: dist,
		Series:       series,
		ByTeam:       byTeam,
		ByChannel:    byChannel,
	}, nil
}

// byTeam 按团队收件箱拆解满意度。
func (a *Satisfaction) byTeam(ctx context.Context, tenantID string, w period.Window, f FilterParams) ([]TeamSatisfaction, error) {
	type row struct {
		ID       *string  `bun:"id"`
		Name     string   `bun:"name"`
		Disabled bool     `bun:"disabled"`
		Rated    int64    `bun:"rated"`
		Positive int64    `bun:"pos"`
		Avg      *float64 `bun:"avg"`
	}
	var rows []row
	q := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		Join("LEFT JOIN teams t ON t.id = r.team_id").
		ColumnExpr("r.team_id::text AS id, COALESCE(t.name, '') AS name, (t.disabled_at IS NOT NULL) AS disabled").
		ColumnExpr("count(*) AS rated, count(*) FILTER (WHERE r.rating >= 4) AS pos, avg(r.rating) AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.rating IS NOT NULL AND r.rated_at >= ? AND r.rated_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("r.team_id, t.name, t.disabled_at").
		OrderExpr("rated DESC, name ASC")
	applyRoundFilters(q, f)
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, errors.Wrap(err, "report satisfaction by team")
	}
	out := make([]TeamSatisfaction, len(rows))
	for i, r := range rows {
		out[i] = TeamSatisfaction{
			ID:            r.ID,
			Name:          r.Name,
			Disabled:      r.Disabled,
			RatedCount:    r.Rated,
			PositiveCount: r.Positive,
			CSAT:          pct(r.Positive, r.Rated),
			AvgScore:      roundPtr(r.Avg),
		}
	}
	return out, nil
}

// byChannel 按渠道拆解满意度。
func (a *Satisfaction) byChannel(ctx context.Context, tenantID string, w period.Window, f FilterParams) ([]ChannelSatisfaction, error) {
	type row struct {
		ID       string       `bun:"id"`
		Name     string       `bun:"name"`
		Kind     channel.Kind `bun:"kind"`
		Enabled  bool         `bun:"enabled"`
		Rated    int64        `bun:"rated"`
		Positive int64        `bun:"pos"`
		Avg      *float64     `bun:"avg"`
	}
	var rows []row
	q := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		Join("JOIN channels ch ON ch.id = c.channel_id").
		ColumnExpr("ch.id::text AS id, ch.name AS name, ch.kind AS kind, ch.enabled AS enabled").
		ColumnExpr("count(*) AS rated, count(*) FILTER (WHERE r.rating >= 4) AS pos, avg(r.rating) AS avg").
		Where("r.tenant_id = ? AND c.kind = ? AND r.rating IS NOT NULL AND r.rated_at >= ? AND r.rated_at < ?", tenantID, conversation.Service, w.Start, w.End).
		GroupExpr("ch.id, ch.name, ch.kind, ch.enabled").
		OrderExpr("rated DESC, ch.name ASC")
	applyRoundFilters(q, f)
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, errors.Wrap(err, "report satisfaction by channel")
	}
	out := make([]ChannelSatisfaction, len(rows))
	for i, r := range rows {
		out[i] = ChannelSatisfaction{
			ID:            r.ID,
			Name:          r.Name,
			Kind:          r.Kind,
			Enabled:       r.Enabled,
			RatedCount:    r.Rated,
			PositiveCount: r.Positive,
			CSAT:          pct(r.Positive, r.Rated),
			AvgScore:      roundPtr(r.Avg),
		}
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/reports/satisfaction。
func (a *Satisfaction) HTTP(ctx context.Context, in *FilterParams) (*SatisfactionOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &SatisfactionOutput{Body: *res}, nil
}

// RatingsInput 是客户评价明细列表的查询参数（独立分页，避免翻页或筛差评时重跑聚合查询）。
type RatingsInput struct {
	FilterParams
	page.OffsetParams
	MinScore int `query:"min_score" minimum:"1" maximum:"5" doc:"最低星级（含）"`
	MaxScore int `query:"max_score" minimum:"1" maximum:"5" doc:"最高星级（含）"`
}

// RatingItem 是评价明细列表中的一条记录。
type RatingItem struct {
	ID             string    `json:"id" doc:"服务轮次 ID"`
	ConversationID string    `json:"conversation_id"`
	Round          int       `json:"round"`
	Score          int       `json:"score"`
	Comment        string    `json:"comment"`
	RatedAt        time.Time `json:"rated_at"`
	ContactID      *string   `json:"contact_id,omitempty"`
	ContactName    string    `json:"contact_name"`
	TeamID         *string   `json:"team_id,omitempty"`
	TeamName       string    `json:"team_name"`
	AssigneeID     string    `json:"assignee_id,omitempty"`
	AssigneeName   string    `json:"assignee_name"`
	ChannelID      *string   `json:"channel_id,omitempty"`
	ChannelName    string    `json:"channel_name"`
}

// RatingsOutput 是评价明细分页列表的 HTTP 响应。
type RatingsOutput struct {
	Body page.Offset[RatingItem]
}

// ListRatings 分页返回统计期内的客户评价明细，按评价时间倒序排列。
type ListRatings struct {
	db *bun.DB
}

// NewListRatings 返回评价明细分页列表动作。
func NewListRatings(db *bun.DB) *ListRatings { return &ListRatings{db: db} }

// Handle 返回 sub 所在工作区符合条件的评价明细分页结果。
func (a *ListRatings) Handle(ctx context.Context, sub identity.Subject, in RatingsInput) (page.Offset[RatingItem], error) {
	if in.Audience == "" {
		in.Audience = channel.AudienceCustomer
	}
	w, err := resolveWindow(ctx, a.db, sub, in.FilterParams)
	if err != nil {
		return page.Offset[RatingItem]{}, err
	}
	if in.Page < 1 {
		in.Page = 1
	}
	if in.PerPage < 1 {
		in.PerPage = 20
	}

	type row struct {
		ID             string    `bun:"id"`
		ConversationID string    `bun:"conversation_id"`
		Round          int       `bun:"round"`
		Score          int       `bun:"rating"`
		Comment        string    `bun:"rating_comment"`
		RatedAt        time.Time `bun:"rated_at"`
		ContactID      *string   `bun:"contact_id"`
		ContactName    string    `bun:"contact_name"`
		TeamID         *string   `bun:"team_id"`
		TeamName       string    `bun:"team_name"`
		AssigneeID     string    `bun:"assignee_id"`
		AssigneeName   string    `bun:"assignee_name"`
		ChannelID      *string   `bun:"channel_id"`
		ChannelName    string    `bun:"channel_name"`
	}
	var rows []row
	q := a.db.NewSelect().TableExpr("conversation_rounds r").
		Join("JOIN conversations c ON c.id = r.conversation_id").
		Join("LEFT JOIN contacts ct ON ct.id = c.contact_id").
		Join("LEFT JOIN teams t ON t.id = r.team_id").
		Join("LEFT JOIN channels ch ON ch.id = c.channel_id").
		Join("LEFT JOIN members m ON r.assignee_type = 'member' AND m.id::text = r.assignee_id").
		ColumnExpr("r.id::text AS id, r.conversation_id::text AS conversation_id, r.round, r.rating, r.rating_comment, r.rated_at").
		ColumnExpr("c.contact_id::text AS contact_id, COALESCE(ct.name, '') AS contact_name").
		ColumnExpr("r.team_id::text AS team_id, COALESCE(t.name, '') AS team_name").
		ColumnExpr("CASE WHEN r.assignee_type = 'member' THEN r.assignee_id ELSE '' END AS assignee_id, COALESCE(m.name, '') AS assignee_name").
		ColumnExpr("c.channel_id::text AS channel_id, COALESCE(ch.name, '') AS channel_name").
		Where("r.tenant_id = ? AND c.kind = ? AND r.rating IS NOT NULL AND r.rated_at >= ? AND r.rated_at < ?", sub.Tenant, conversation.Service, w.Start, w.End).
		OrderExpr("r.rated_at DESC, r.id DESC").
		Limit(in.Limit()).
		Offset(in.Offset())
	applyRoundFilters(q, in.FilterParams)
	if in.MinScore > 0 {
		q.Where("r.rating >= ?", in.MinScore)
	}
	if in.MaxScore > 0 {
		q.Where("r.rating <= ?", in.MaxScore)
	}
	total, err := q.ScanAndCount(ctx, &rows)
	if err != nil {
		return page.Offset[RatingItem]{}, errors.Wrap(err, "list report ratings")
	}
	items := make([]RatingItem, len(rows))
	for i, r := range rows {
		items[i] = RatingItem{
			ID:             r.ID,
			ConversationID: r.ConversationID,
			Round:          r.Round,
			Score:          r.Score,
			Comment:        r.Comment,
			RatedAt:        r.RatedAt,
			ContactID:      r.ContactID,
			ContactName:    r.ContactName,
			TeamID:         r.TeamID,
			TeamName:       r.TeamName,
			AssigneeID:     r.AssigneeID,
			AssigneeName:   r.AssigneeName,
			ChannelID:      r.ChannelID,
			ChannelName:    r.ChannelName,
		}
	}
	return page.NewOffset(items, in.OffsetParams, int64(total)), nil
}

// HTTP 处理 GET /t/{slug}/api/reports/satisfaction/ratings。
func (a *ListRatings) HTTP(ctx context.Context, in *RatingsInput) (*RatingsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &RatingsOutput{Body: res}, nil
}

// pct 返回 num / den × 100 保留一位小数；den <= 0 时返回 nil。
func pct(num, den int64) *float64 {
	if den <= 0 {
		return nil
	}
	v := round1(float64(num) * 100 / float64(den))
	return &v
}
