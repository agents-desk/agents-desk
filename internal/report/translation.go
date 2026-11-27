package report

import (
	"context"
	"math"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
)

// TranslationSummary 是翻译报表的核心用量、成功率、耗时与费用摘要。
type TranslationSummary struct {
	Calls             int64    `json:"calls" doc:"统计期内翻译模型调用总次数"`
	PrevCalls         int64    `json:"prev_calls" doc:"上一对比期调用总次数"`
	SuccessCalls      int64    `json:"success_calls"`
	FailedCalls       int64    `json:"failed_calls"`
	SuccessRate       *float64 `json:"success_rate,omitempty" doc:"调用成功率百分比（0–100）"`
	PromptTokens      int64    `json:"prompt_tokens"`
	CompletionTokens  int64    `json:"completion_tokens" doc:"输出 token 总数（含思考 token）"`
	ReasoningTokens   int64    `json:"reasoning_tokens" doc:"思考 token 总数"`
	TotalTokens       int64    `json:"total_tokens"`
	PrevTotalTokens   int64    `json:"prev_total_tokens"`
	TotalCostUSD      float64  `json:"total_cost_usd" doc:"供应商返回的费用总计（美元）"`
	PrevTotalCostUSD  float64  `json:"prev_total_cost_usd"`
	CostedCalls       int64    `json:"costed_calls" doc:"带费用返回的调用次数"`
	AvgDurationMS     *float64 `json:"avg_duration_ms,omitempty" doc:"平均耗时（毫秒）"`
	PrevAvgDurationMS *float64 `json:"prev_avg_duration_ms,omitempty"`
}

// TranslationPoint 是翻译调用趋势图中的一个时间桶。
type TranslationPoint struct {
	Date        string  `json:"date"`
	Calls       int64   `json:"calls"`
	FailedCalls int64   `json:"failed_calls"`
	TotalTokens int64   `json:"total_tokens"`
	CostUSD     float64 `json:"cost_usd"`
}

// TranslationOperationBreakdown 是按业务调用场景（incoming / fill / reply / back_translate）拆分的统计。
type TranslationOperationBreakdown struct {
	Operation        string   `json:"operation"`
	Calls            int64    `json:"calls"`
	FailedCalls      int64    `json:"failed_calls"`
	SuccessRate      *float64 `json:"success_rate,omitempty"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	ReasoningTokens  int64    `json:"reasoning_tokens"`
	CostUSD          float64  `json:"cost_usd"`
	AvgDurationMS    *float64 `json:"avg_duration_ms,omitempty"`
}

// TranslationModelBreakdown 是按模型名拆分的翻译调用统计。
type TranslationModelBreakdown struct {
	Model         string   `json:"model"`
	Calls         int64    `json:"calls"`
	FailedCalls   int64    `json:"failed_calls"`
	TotalTokens   int64    `json:"total_tokens"`
	CostUSD       float64  `json:"cost_usd"`
	AvgDurationMS *float64 `json:"avg_duration_ms,omitempty"`
}

// TranslationSceneBreakdown 是按业务场景（service / partner_service / internal_service / ticket / business / internal_chat / other）拆分的翻译调用统计。
type TranslationSceneBreakdown struct {
	Scene         string   `json:"scene" doc:"业务场景：service（客服）、partner_service（商务服务）、internal_service（内部服务）、ticket（工单）、business（商务沟通）、internal_chat（内部聊天）、other（其他）"`
	Calls         int64    `json:"calls"`
	FailedCalls   int64    `json:"failed_calls"`
	SuccessRate   *float64 `json:"success_rate,omitempty"`
	TotalTokens   int64    `json:"total_tokens"`
	CostUSD       float64  `json:"cost_usd"`
	AvgDurationMS *float64 `json:"avg_duration_ms,omitempty"`
}

// TranslationReport 是翻译报表的完整响应。
type TranslationReport struct {
	Period            PeriodMeta                      `json:"period"`
	Summary           TranslationSummary              `json:"summary"`
	Series            []TranslationPoint              `json:"series" nullable:"false"`
	ByScene           []TranslationSceneBreakdown     `json:"by_scene" nullable:"false"`
	ByOperation       []TranslationOperationBreakdown `json:"by_operation" nullable:"false"`
	ByModel           []TranslationModelBreakdown     `json:"by_model" nullable:"false"`
	DetectedLanguages []LanguageCount                 `json:"detected_languages" nullable:"false"`
	ReplyLanguages    []LanguageCount                 `json:"reply_languages" nullable:"false"`
}

// TranslationOutput 是翻译报表的 HTTP 响应。
type TranslationOutput struct {
	Body TranslationReport
}

// Translation 汇总工作区的翻译模型调用量、成功率、token、费用、耗时及语言分布。
type Translation struct {
	db *bun.DB
}

// NewTranslation 返回翻译报表动作。
func NewTranslation(db *bun.DB) *Translation { return &Translation{db: db} }

// Handle 返回 sub 所在工作区按时间窗口 p 统计的翻译报表。
func (a *Translation) Handle(ctx context.Context, sub identity.Subject, p period.Params) (*TranslationReport, error) {
	w, err := resolvePeriodOnly(ctx, a.db, sub.Tenant, p)
	if err != nil {
		return nil, err
	}

	var sum TranslationSummary
	qSum := a.db.NewSelect().Table("llm_calls").
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ?) AS calls", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ?) AS prev_calls", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ? AND error = '') AS success_calls", w.Start, w.End).
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ? AND error <> '') AS failed_calls", w.Start, w.End).
		ColumnExpr("COALESCE(sum(prompt_tokens) FILTER (WHERE created_at >= ? AND created_at < ?), 0) AS prompt_tokens", w.Start, w.End).
		ColumnExpr("COALESCE(sum(completion_tokens) FILTER (WHERE created_at >= ? AND created_at < ?), 0) AS completion_tokens", w.Start, w.End).
		ColumnExpr("COALESCE(sum(reasoning_tokens) FILTER (WHERE created_at >= ? AND created_at < ?), 0) AS reasoning_tokens", w.Start, w.End).
		ColumnExpr("COALESCE(sum(prompt_tokens + completion_tokens) FILTER (WHERE created_at >= ? AND created_at < ?), 0) AS prev_total_tokens", w.PrevStart, w.PrevEnd).
		ColumnExpr("COALESCE(sum(cost) FILTER (WHERE created_at >= ? AND created_at < ?), 0)::float8 AS total_cost", w.Start, w.End).
		ColumnExpr("COALESCE(sum(cost) FILTER (WHERE created_at >= ? AND created_at < ?), 0)::float8 AS prev_total_cost", w.PrevStart, w.PrevEnd).
		ColumnExpr("count(*) FILTER (WHERE created_at >= ? AND created_at < ? AND cost IS NOT NULL) AS costed_calls", w.Start, w.End).
		ColumnExpr("avg(duration_ms) FILTER (WHERE created_at >= ? AND created_at < ?) AS avg_ms", w.Start, w.End).
		ColumnExpr("avg(duration_ms) FILTER (WHERE created_at >= ? AND created_at < ?) AS prev_avg_ms", w.PrevStart, w.PrevEnd).
		Where("tenant_id = ? AND call_type = ? AND created_at >= ? AND created_at < ?", sub.Tenant, llm.Translation, w.PrevStart, w.End)
	if err := qSum.Scan(ctx,
		&sum.Calls, &sum.PrevCalls, &sum.SuccessCalls, &sum.FailedCalls,
		&sum.PromptTokens, &sum.CompletionTokens, &sum.ReasoningTokens, &sum.PrevTotalTokens,
		&sum.TotalCostUSD, &sum.PrevTotalCostUSD, &sum.CostedCalls,
		&sum.AvgDurationMS, &sum.PrevAvgDurationMS,
	); err != nil {
		return nil, errors.Wrap(err, "report translation summary")
	}
	sum.TotalTokens = sum.PromptTokens + sum.CompletionTokens
	sum.TotalCostUSD = round6(sum.TotalCostUSD)
	sum.PrevTotalCostUSD = round6(sum.PrevTotalCostUSD)
	sum.SuccessRate = pct(sum.SuccessCalls, sum.Calls)
	sum.AvgDurationMS = roundPtr(sum.AvgDurationMS)
	sum.PrevAvgDurationMS = roundPtr(sum.PrevAvgDurationMS)

	// 趋势序列
	type seriesRow struct {
		Bucket string  `bun:"bucket"`
		Calls  int64   `bun:"calls"`
		Failed int64   `bun:"failed"`
		Tokens int64   `bun:"tokens"`
		Cost   float64 `bun:"cost"`
	}
	var sRows []seriesRow
	err = a.db.NewSelect().Table("llm_calls").
		ColumnExpr(bucketExpr("created_at", w.Granularity)+" AS bucket", w.TimeZone).
		ColumnExpr("count(*) AS calls, count(*) FILTER (WHERE error <> '') AS failed").
		ColumnExpr("COALESCE(sum(prompt_tokens + completion_tokens), 0) AS tokens, COALESCE(sum(cost), 0)::float8 AS cost").
		Where("tenant_id = ? AND call_type = ? AND created_at >= ? AND created_at < ?", sub.Tenant, llm.Translation, w.Start, w.End).
		GroupExpr("1").
		Scan(ctx, &sRows)
	if err != nil {
		return nil, errors.Wrap(err, "report translation series")
	}
	sparse := make([]TranslationPoint, len(sRows))
	for i, r := range sRows {
		sparse[i] = TranslationPoint{
			Date:        r.Bucket,
			Calls:       r.Calls,
			FailedCalls: r.Failed,
			TotalTokens: r.Tokens,
			CostUSD:     round6(r.Cost),
		}
	}
	series := period.FillSeries(w, sparse,
		func(pt TranslationPoint) string { return pt.Date },
		func(b string) TranslationPoint { return TranslationPoint{Date: b} },
	)

	// 按业务场景拆解（兼容 conversation、timeline_item 与 ticket 三类翻译目标并映射回会话域）
	type sceneRow struct {
		Scene  string   `bun:"scene"`
		Calls  int64    `bun:"calls"`
		Failed int64    `bun:"failed"`
		Tokens int64    `bun:"tokens"`
		Cost   float64  `bun:"cost"`
		AvgMS  *float64 `bun:"avg_ms"`
	}
	var sceneRows []sceneRow
	err = a.db.NewSelect().TableExpr("llm_calls lc").
		Join("LEFT JOIN timeline_items ti ON lc.target_type = 'timeline_item' AND ti.id::text = lc.target_id AND ti.tenant_id = lc.tenant_id").
		Join("LEFT JOIN conversations c ON c.tenant_id = lc.tenant_id AND ((lc.target_type IN ('conversation', 'ticket') AND c.id::text = lc.target_id) OR (lc.target_type = 'timeline_item' AND c.id = ti.conversation_id))").
		Join("LEFT JOIN chats ch ON ch.conversation_id = c.id").
		ColumnExpr(`CASE
			WHEN c.kind = ? AND c.audience = ? THEN 'partner_service'
			WHEN c.kind = ? AND c.audience = ? THEN 'internal_service'
			WHEN c.kind = ? THEN 'service'
			WHEN c.kind = ? OR lc.target_type = 'ticket' THEN 'ticket'
			WHEN c.kind = ? AND ch.scope = ? THEN 'business'
			WHEN c.kind = ? AND ch.scope = ? THEN 'internal_chat'
			ELSE 'other'
		END AS scene`, conversation.Service, channel.AudiencePartner, conversation.Service, channel.AudienceInternal, conversation.Service, conversation.Ticket, conversation.Chat, conversation.ChatExternal, conversation.Chat, conversation.ChatInternal).
		ColumnExpr("count(*) AS calls, count(*) FILTER (WHERE lc.error <> '') AS failed").
		ColumnExpr("COALESCE(sum(lc.prompt_tokens + lc.completion_tokens), 0) AS tokens, COALESCE(sum(lc.cost), 0)::float8 AS cost, avg(lc.duration_ms) AS avg_ms").
		Where("lc.tenant_id = ? AND lc.call_type = ? AND lc.created_at >= ? AND lc.created_at < ?", sub.Tenant, llm.Translation, w.Start, w.End).
		GroupExpr("1").
		OrderExpr("calls DESC, 1 ASC").
		Scan(ctx, &sceneRows)
	if err != nil {
		return nil, errors.Wrap(err, "report translation by scene")
	}
	byScene := make([]TranslationSceneBreakdown, len(sceneRows))
	for i, r := range sceneRows {
		byScene[i] = TranslationSceneBreakdown{
			Scene:         r.Scene,
			Calls:         r.Calls,
			FailedCalls:   r.Failed,
			SuccessRate:   pct(r.Calls-r.Failed, r.Calls),
			TotalTokens:   r.Tokens,
			CostUSD:       round6(r.Cost),
			AvgDurationMS: roundPtr(r.AvgMS),
		}
	}

	// 按业务调用场景拆解（直接使用一等字段 operation）
	type opRow struct {
		Operation  string   `bun:"op"`
		Calls      int64    `bun:"calls"`
		Failed     int64    `bun:"failed"`
		Prompt     int64    `bun:"prompt"`
		Completion int64    `bun:"completion"`
		Reasoning  int64    `bun:"reasoning"`
		Cost       float64  `bun:"cost"`
		AvgMS      *float64 `bun:"avg_ms"`
	}
	var opRows []opRow
	err = a.db.NewSelect().Table("llm_calls").
		ColumnExpr("COALESCE(NULLIF(operation, ''), 'other') AS op").
		ColumnExpr("count(*) AS calls, count(*) FILTER (WHERE error <> '') AS failed").
		ColumnExpr("COALESCE(sum(prompt_tokens), 0) AS prompt, COALESCE(sum(completion_tokens), 0) AS completion, COALESCE(sum(reasoning_tokens), 0) AS reasoning").
		ColumnExpr("COALESCE(sum(cost), 0)::float8 AS cost, avg(duration_ms) AS avg_ms").
		Where("tenant_id = ? AND call_type = ? AND created_at >= ? AND created_at < ?", sub.Tenant, llm.Translation, w.Start, w.End).
		GroupExpr("1").
		OrderExpr("calls DESC, 1 ASC").
		Scan(ctx, &opRows)
	if err != nil {
		return nil, errors.Wrap(err, "report translation by operation")
	}
	byOp := make([]TranslationOperationBreakdown, len(opRows))
	for i, r := range opRows {
		byOp[i] = TranslationOperationBreakdown{
			Operation:        r.Operation,
			Calls:            r.Calls,
			FailedCalls:      r.Failed,
			SuccessRate:      pct(r.Calls-r.Failed, r.Calls),
			PromptTokens:     r.Prompt,
			CompletionTokens: r.Completion,
			ReasoningTokens:  r.Reasoning,
			CostUSD:          round6(r.Cost),
			AvgDurationMS:    roundPtr(r.AvgMS),
		}
	}

	// 按模型拆解
	type modelRow struct {
		Model  string   `bun:"model"`
		Calls  int64    `bun:"calls"`
		Failed int64    `bun:"failed"`
		Tokens int64    `bun:"tokens"`
		Cost   float64  `bun:"cost"`
		AvgMS  *float64 `bun:"avg_ms"`
	}
	var modelRows []modelRow
	err = a.db.NewSelect().Table("llm_calls").
		ColumnExpr("model, count(*) AS calls, count(*) FILTER (WHERE error <> '') AS failed").
		ColumnExpr("COALESCE(sum(prompt_tokens + completion_tokens), 0) AS tokens, COALESCE(sum(cost), 0)::float8 AS cost, avg(duration_ms) AS avg_ms").
		Where("tenant_id = ? AND call_type = ? AND created_at >= ? AND created_at < ?", sub.Tenant, llm.Translation, w.Start, w.End).
		GroupExpr("model").
		OrderExpr("calls DESC, model ASC").
		Scan(ctx, &modelRows)
	if err != nil {
		return nil, errors.Wrap(err, "report translation by model")
	}
	byModel := make([]TranslationModelBreakdown, len(modelRows))
	for i, r := range modelRows {
		byModel[i] = TranslationModelBreakdown{
			Model:         r.Model,
			Calls:         r.Calls,
			FailedCalls:   r.Failed,
			TotalTokens:   r.Tokens,
			CostUSD:       round6(r.Cost),
			AvgDurationMS: roundPtr(r.AvgMS),
		}
	}

	// 客户消息识别语言分布（timeline_items.derived->>'language'，含单聊联系人与外部群发言人）
	detected := []LanguageCount{}
	err = a.db.NewSelect().TableExpr("timeline_items it").
		ColumnExpr("COALESCE(NULLIF(it.derived->>'language', ''), 'und') AS language, count(*) AS count").
		Where("it.tenant_id = ? AND it.kind = ? AND it.actor_type IN (?, ?) AND it.visibility = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Message, conversation.ContactActor, conversation.ExternalUserActor, conversation.Everyone, w.Start, w.End).
		Where("it.derived ? 'language'").
		GroupExpr("1").
		OrderExpr("count DESC, 1 ASC").
		Limit(12).
		Scan(ctx, &detected)
	if err != nil {
		return nil, errors.Wrap(err, "report detected languages")
	}
	if detected == nil {
		detected = []LanguageCount{}
	}

	// 成员回复译文目标语言分布（timeline_items.content->>'language'）
	replyLangs := []LanguageCount{}
	err = a.db.NewSelect().TableExpr("timeline_items it").
		ColumnExpr("it.content->>'language' AS language, count(*) AS count").
		Where("it.tenant_id = ? AND it.kind = ? AND it.actor_type = ? AND it.visibility = ? AND it.created_at >= ? AND it.created_at < ?",
			sub.Tenant, conversation.Message, conversation.MemberActor, conversation.Everyone, w.Start, w.End).
		Where("COALESCE(it.content->>'original', '') <> '' AND COALESCE(it.content->>'language', '') <> ''").
		GroupExpr("1").
		OrderExpr("count DESC, 1 ASC").
		Limit(12).
		Scan(ctx, &replyLangs)
	if err != nil {
		return nil, errors.Wrap(err, "report reply languages")
	}
	if replyLangs == nil {
		replyLangs = []LanguageCount{}
	}

	return &TranslationReport{
		Period:            PeriodMeta(w.Meta()),
		Summary:           sum,
		Series:            series,
		ByScene:           byScene,
		ByOperation:       byOp,
		ByModel:           byModel,
		DetectedLanguages: detected,
		ReplyLanguages:    replyLangs,
	}, nil
}

// round6 把美元费用保留 6 位小数（与数据库 numeric(12, 6) 对齐）。
func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// HTTP 处理 GET /t/{slug}/api/reports/translation。
func (a *Translation) HTTP(ctx context.Context, in *period.Params) (*TranslationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &TranslationOutput{Body: *res}, nil
}
