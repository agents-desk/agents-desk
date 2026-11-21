package widget

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// Color 是聊天窗口的主题色，取值见 colors。
type Color string

// colors 是聊天窗口可以选的主题色：界面主题中预设的色板。
var colors = []string{
	"blue-gray", "brown", "tomato", "deep-orange", "bright-orange", "yellow", "bright-green", "green", "light-blue",
	"sky-blue", "pale-blue", "bright-blue", "deep-blue", "pale-indigo", "purple", "pale-purple", "violet", "pale-violet",
	"dark-pink", "bright-pink", "pink", "magenta", "bright-red", "pale-red", "deep-red", "red",
}

// defaultColor 是没有选主题色时聊天窗口的颜色。
const defaultColor Color = "bright-blue"

// color 返回设置中的主题色，没有选时是默认的颜色。
func (s Settings) color() Color {
	if s.Color == "" {
		return defaultColor
	}
	return Color(s.Color)
}

// Schema 让 OpenAPI 中的 Color 只接受 colors 中的取值。
func (Color) Schema(huma.Registry) *huma.Schema {
	s := &huma.Schema{Type: huma.TypeString}
	for _, c := range colors {
		s.Enum = append(s.Enum, c)
	}
	return s
}

// WidgetDTO 是接口中的聊天插件。
type WidgetDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TeamID    *string   `json:"team_id,omitempty" doc:"新会话进入的团队收件箱；为空时进入未指派"`
	TeamName  string    `json:"team_name" doc:"新会话进入的团队收件箱的名称；未指派时为空"`
	Greeting  string    `json:"greeting" doc:"聊天窗口顶部的问候语"`
	Color     Color     `json:"color" doc:"聊天窗口的主题色"`
	Enabled   bool      `json:"enabled" doc:"停用后聊天窗口不再显示，客户不能再发消息"`
	CreatedAt time.Time `json:"created_at"`
}

// WidgetOutput 是返回一个聊天插件的响应。
type WidgetOutput struct {
	Body WidgetDTO
}

// IDInput 是指定一个聊天插件的请求：路径中的渠道 ID。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Get 读取当前工作区中的一个聊天插件。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取聊天插件的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回 sub 所在工作区中的聊天插件 id，不存在或属于别的工作区时返回 channel.ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*WidgetDTO, error) {
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

// HTTP 处理 GET /t/{slug}/api/widgets/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*WidgetOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &WidgetOutput{Body: *dto}, nil
}

// toDTOs 把聊天插件转换为接口中的形式，一次读出团队收件箱的名称。
func toDTOs(ctx context.Context, db bun.IDB, chs []channel.Channel) ([]WidgetDTO, error) {
	out := make([]WidgetDTO, len(chs))
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
		out[i] = WidgetDTO{
			ID: ch.ID, Name: ch.Name, TeamID: ch.TeamID, Greeting: s.Greeting, Color: s.color(), Enabled: ch.Enabled, CreatedAt: ch.CreatedAt,
		}
		if ch.TeamID != nil {
			out[i].TeamName = teams[*ch.TeamID]
		}
	}
	return out, nil
}
