package agent

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// BotInput 是创建或更新机器人的请求参数。
type BotInput struct {
	Name     string      `json:"name" minLength:"1" maxLength:"100" doc:"机器人显示名称"`
	Enabled  *bool       `json:"enabled,omitempty" doc:"是否启用；新建时默认 true"`
	Settings BotSettings `json:"settings" doc:"机器人模型、提示词与工具策略配置"`
}

// botSettingsMap 把结构化的 BotSettings 转换为落库的 map。
func botSettingsMap(cfg BotSettings) map[string]any {
	m := map[string]any{}
	if strings.TrimSpace(cfg.SystemPrompt) != "" {
		m["system_prompt"] = strings.TrimSpace(cfg.SystemPrompt)
	}
	if strings.TrimSpace(cfg.PromptVersion) != "" {
		m["prompt_version"] = strings.TrimSpace(cfg.PromptVersion)
	}
	if strings.TrimSpace(cfg.Model) != "" {
		m["model"] = strings.TrimSpace(cfg.Model)
	}
	if cfg.Temperature != nil {
		m["temperature"] = *cfg.Temperature
	}
	if len(cfg.AllowedTools) > 0 {
		m["allowed_tools"] = cfg.AllowedTools
	}
	if len(cfg.RequireApprovalTools) > 0 {
		m["require_approval_tools"] = cfg.RequireApprovalTools
	}
	if cfg.BargeInPolicy != "" {
		m["barge_in_policy"] = cfg.BargeInPolicy
	}
	if cfg.DebounceMS > 0 {
		m["debounce_ms"] = cfg.DebounceMS
	}
	if cfg.MaxToolIterations > 0 {
		m["max_tool_iterations"] = cfg.MaxToolIterations
	}
	return m
}

// ListBots 返回工作区的全部机器人列表。
type ListBots struct {
	db *bun.DB
}

// NewListBots 返回机器人列表查询动作。
func NewListBots(db *bun.DB) *ListBots { return &ListBots{db: db} }

// Handle 返回 sub 所在工作区中的机器人列表；不按成员语言过滤，按创建时间排序。
func (a *ListBots) Handle(ctx context.Context, sub identity.Subject, onlyEnabled bool) ([]BotDTO, error) {
	if sub.Type != member.Type || !sub.Can(string(auth.ConversationsRead)) {
		return nil, ErrForbidden.New()
	}
	q := a.db.NewSelect().Model((*Bot)(nil)).Where("tenant_id = ?", sub.Tenant).OrderExpr("created_at, id")
	if onlyEnabled {
		q = q.Where("enabled = true")
	}
	var bots []Bot
	if err := q.Scan(ctx, &bots); err != nil {
		return nil, errors.Wrap(err, "list bots")
	}
	out := make([]BotDTO, len(bots))
	for i := range bots {
		out[i] = bots[i].dto()
	}
	return out, nil
}

// CreateBot 在工作区中新建一个机器人并记录操作日志。
type CreateBot struct {
	db *bun.DB
}

// NewCreateBot 返回新建机器人动作。
func NewCreateBot(db *bun.DB) *CreateBot { return &CreateBot{db: db} }

// Handle 创建工作区机器人，要求工作区管理权限（tenant-settings:manage）。
func (a *CreateBot) Handle(ctx context.Context, sub identity.Subject, in BotInput) (*BotDTO, error) {
	if sub.Type != member.Type || !sub.Can(string(auth.TenantSettingsManage)) {
		return nil, ErrForbidden.New()
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	at := now()
	b := &Bot{
		ID:       newID(),
		TenantID: sub.Tenant,
		Name:     strings.TrimSpace(in.Name),
		Enabled:  enabled,
		Settings: botSettingsMap(in.Settings),
	}
	b.CreatedAt, b.UpdatedAt = at, at
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(b).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert bot")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "bot.created", TargetType: "bot", TargetID: b.ID,
			Properties: map[string]any{"name": b.Name, "enabled": b.Enabled},
		})
	})
	if err != nil {
		return nil, err
	}
	dto := b.dto()
	return &dto, nil
}

// UpdateBot 修改工作区中的机器人名称、启停状态与配置，并记录操作日志。
type UpdateBot struct {
	db *bun.DB
}

// NewUpdateBot 返回修改机器人动作。
func NewUpdateBot(db *bun.DB) *UpdateBot { return &UpdateBot{db: db} }

// Handle 更新工作区机器人 id。
func (a *UpdateBot) Handle(ctx context.Context, sub identity.Subject, id string, in BotInput) (*BotDTO, error) {
	if sub.Type != member.Type || !sub.Can(string(auth.TenantSettingsManage)) {
		return nil, ErrForbidden.New()
	}
	var b Bot
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := tx.NewSelect().Model(&b).Where("id = ? AND tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrBotNotFound.New()
			}
			return errors.Wrap(err, "load bot")
		}
		if strings.TrimSpace(in.Name) != "" {
			b.Name = strings.TrimSpace(in.Name)
		}
		if in.Enabled != nil {
			b.Enabled = *in.Enabled
		}
		b.Settings = botSettingsMap(in.Settings)
		b.UpdatedAt = now()
		if _, err := tx.NewUpdate().Model(&b).Column("name", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update bot")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "bot.updated", TargetType: "bot", TargetID: b.ID,
			Properties: map[string]any{"name": b.Name, "enabled": b.Enabled},
		})
	})
	if err != nil {
		return nil, err
	}
	dto := b.dto()
	return &dto, nil
}

// DeleteBot 删除未被占用的机器人并记录操作日志。
type DeleteBot struct {
	db *bun.DB
}

// NewDeleteBot 返回删除机器人动作。
func NewDeleteBot(db *bun.DB) *DeleteBot { return &DeleteBot{db: db} }

// Handle 删除工作区机器人 id；若正被未关闭会话、渠道 AI 接待或活跃生成使用则返回 ErrBotInUse。
func (a *DeleteBot) Handle(ctx context.Context, sub identity.Subject, id string) error {
	if sub.Type != member.Type || !sub.Can(string(auth.TenantSettingsManage)) {
		return ErrForbidden.New()
	}
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var b Bot
		if err := tx.NewSelect().Model(&b).Where("id = ? AND tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrBotNotFound.New()
			}
			return errors.Wrap(err, "load bot for delete")
		}
		var inUse bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM conversations
			WHERE tenant_id = ? AND status <> 'closed' AND assignee_type = 'bot' AND assignee_id = ?
		) OR EXISTS (
			SELECT 1 FROM channels
			WHERE tenant_id = ? AND archived_at IS NULL AND enabled = true
			  AND settings->'ai_reception'->>'bot_id' = ?
			  AND COALESCE((settings->'ai_reception'->>'enabled')::boolean, false) = true
		) OR EXISTS (
			SELECT 1 FROM generations
			WHERE tenant_id = ? AND actor_id = ? AND state IN (?)
		)`, sub.Tenant, id, sub.Tenant, id, sub.Tenant, id, bun.In([]State{Pending, Streaming, WaitingApproval})).Scan(ctx, &inUse); err != nil {
			return errors.Wrap(err, "check bot usage")
		}
		if inUse {
			return ErrBotInUse.New()
		}
		if _, err := tx.NewDelete().Model(&b).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "delete bot")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "bot.deleted", TargetType: "bot", TargetID: id,
			Properties: map[string]any{"name": b.Name},
		})
	})
}
