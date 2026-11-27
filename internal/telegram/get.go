package telegram

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// TelegramDTO 是接口中的 Telegram 机器人渠道（不含明文 Bot Token 和 Webhook 密钥）。
type TelegramDTO struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Audience       channel.Audience `json:"audience" doc:"服务对象类型：customer（外部客户）、partner（合作商家）或 internal（内部员工）"`
	TeamID         *string          `json:"team_id,omitempty" doc:"新会话进入的团队收件箱；为空时进入未指派"`
	TeamName       string           `json:"team_name" doc:"新会话进入的团队收件箱的名称；未指派时为空"`
	BotUsername    string           `json:"bot_username" doc:"Telegram 机器人的用户名（不含 @）"`
	GatewayManaged bool             `json:"gateway_managed" doc:"开启后由外部业务网关转发 Webhook，系统不调用 setWebhook"`
	EnableGroups   bool             `json:"enable_groups" doc:"开启后接收群内 @ 机器人与命令消息"`
	Enabled        bool             `json:"enabled" doc:"停用后不再接收或发送消息"`
	CreatedAt      time.Time        `json:"created_at"`
}

// TelegramOutput 是返回一个 Telegram 机器人渠道的响应。
type TelegramOutput struct {
	Body TelegramDTO
}

// IDInput 是指定一个 Telegram 渠道的请求：路径中的渠道 ID。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Get 读取当前工作区中的一个 Telegram 机器人渠道。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取 Telegram 渠道的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回 sub 所在工作区中的 Telegram 渠道 id，不存在或属于别的工作区时返回 channel.ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*TelegramDTO, error) {
	ch, err := find(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	dtos, err := toDTOs(ctx, a.db, []channel.Channel{*ch})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 GET /t/{slug}/api/telegrams/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*TelegramOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &TelegramOutput{Body: *dto}, nil
}

// toDTOs 把 Telegram 渠道转为接口 DTO，一次查出团队收件箱名称。
func toDTOs(ctx context.Context, db bun.IDB, chs []channel.Channel) ([]TelegramDTO, error) {
	out := make([]TelegramDTO, len(chs))
	var teamIDs []string
	for _, ch := range chs {
		if ch.TeamID != nil {
			teamIDs = append(teamIDs, *ch.TeamID)
		}
	}
	teams := map[string]string{}
	if len(teamIDs) > 0 {
		var rows []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("teams").Column("id", "name").Where("id IN (?)", bun.In(teamIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load team names")
		}
		for _, r := range rows {
			teams[r.ID] = r.Name
		}
	}
	for i := range chs {
		ch := &chs[i]
		s, err := settingsOf(ch)
		if err != nil {
			return nil, err
		}
		out[i] = TelegramDTO{
			ID: ch.ID, Name: ch.Name, Audience: ch.EffectiveAudience(), TeamID: ch.TeamID, BotUsername: s.BotUsername,
			GatewayManaged: s.GatewayManaged, EnableGroups: s.GroupsEnabled(), Enabled: ch.Enabled, CreatedAt: ch.CreatedAt,
		}
		if ch.TeamID != nil {
			out[i].TeamName = teams[*ch.TeamID]
		}
	}
	return out, nil
}
