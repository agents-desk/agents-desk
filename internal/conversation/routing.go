package conversation

import (
	"context"
	"encoding/json"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"agents-desk/internal/channel"
)

// RoutingDecision 描述新服务型会话在入站路由阶段算出的默认归属、接待模式与需要加入会话的机器人。
type RoutingDecision struct {
	TeamID          *string
	DefaultMemberID string
	HandlingMode    HandlingMode
	AssigneeType    ActorType
	AssigneeID      string
	JoinBotID       string
	AssignReason    AssignReason
}

// InboundRouter 解耦新会话的默认团队/负责人分配与 AI 接待决策。
type InboundRouter interface {
	RouteInbound(ctx context.Context, tx bun.IDB, ch *channel.Channel, preferredTeams ...*string) (RoutingDecision, error)
}

// channelAIReception 保存 channels.settings["ai_reception"] 中的渠道级 AI 接待开关。
type channelAIReception struct {
	Enabled bool         `json:"enabled"`
	BotID   string       `json:"bot_id"`
	Mode    HandlingMode `json:"mode"`
}

// DefaultInboundRouter 是默认入站路由器：
// 先按群级/渠道级/工作区默认配置得出归属团队与成员；若渠道开启了 ai_reception 且目标机器人启用，
// 则按配置进入 AI 自动接待（指派给 BotActor 并保留团队作为转人工回退）或 AI 副驾驶模式。
type DefaultInboundRouter struct{}

// RouteInbound 计算渠道 ch 新建服务型会话时的默认路由与 AI 接待控制权。
func (DefaultInboundRouter) RouteInbound(ctx context.Context, tx bun.IDB, ch *channel.Channel, preferredTeams ...*string) (RoutingDecision, error) {
	teamID, defaultMemberID, err := resolveDefaultRouting(ctx, tx, ch.TenantID, preferredTeams...)
	if err != nil {
		return RoutingDecision{}, err
	}
	decision := RoutingDecision{
		TeamID:          teamID,
		DefaultMemberID: defaultMemberID,
		HandlingMode:    HandlingHuman,
	}
	cfg := parseChannelAIReception(ch)
	if !cfg.Enabled || cfg.BotID == "" {
		return decision, nil
	}
	ok, err := botEnabledInTenant(ctx, tx, ch.TenantID, cfg.BotID)
	if err != nil {
		return RoutingDecision{}, err
	}
	if !ok {
		return decision, nil
	}
	if cfg.Mode == HandlingAICopilot {
		decision.HandlingMode = HandlingAICopilot
		decision.JoinBotID = cfg.BotID
		return decision, nil
	}
	decision.HandlingMode = HandlingAIAuto
	decision.DefaultMemberID = ""
	decision.AssigneeType = BotActor
	decision.AssigneeID = cfg.BotID
	decision.JoinBotID = cfg.BotID
	decision.AssignReason = ReasonAIAuto
	return decision, nil
}

// parseChannelAIReception 从渠道 settings 中读取 ai_reception 配置。
func parseChannelAIReception(ch *channel.Channel) channelAIReception {
	if ch == nil || ch.Settings == nil {
		return channelAIReception{}
	}
	raw, ok := ch.Settings["ai_reception"]
	if !ok || raw == nil {
		return channelAIReception{}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return channelAIReception{}
	}
	var cfg channelAIReception
	if json.Unmarshal(data, &cfg) != nil {
		return channelAIReception{}
	}
	return cfg
}

// botEnabledInTenant 检查机器人 botID 是否存在于工作区 tenantID 且处于启用状态。
func botEnabledInTenant(ctx context.Context, db bun.IDB, tenantID, botID string) (bool, error) {
	if botID == "" {
		return false, nil
	}
	ok, err := db.NewSelect().Table("bots").
		Where("id = ? AND tenant_id = ? AND enabled = true", botID, tenantID).
		Exists(ctx)
	return ok, errors.Wrap(err, "check enabled bot")
}
