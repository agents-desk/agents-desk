package widget

import (
	"context"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// Color 是聊天窗口的主题色，采用小写 #rrggbb 十六进制颜色值。
type Color string

// defaultColor 是聊天窗口的默认主题色（bright-blue）。
const defaultColor Color = "#0063ff"

// normalizeColor 校验并标准化主题色：空值返回默认主题色，#rrggbb 转为小写；其余返回 ErrInvalidColor。
func normalizeColor(raw Color) (Color, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return defaultColor, nil
	}
	if isHexColor(s) {
		return Color(strings.ToLower(s)), nil
	}
	return "", ErrInvalidColor.New(errorx.Field("appearance.color"), errorx.Public("color", s))
}

// normalizeAppearance 校验并标准化聊天窗口的外观配置。
func normalizeAppearance(in Appearance) (Appearance, error) {
	color, err := normalizeColor(in.Color)
	if err != nil {
		return Appearance{}, err
	}
	return Appearance{
		Color:    color,
		Greeting: in.Greeting,
	}, nil
}

// isHexColor 报告 s 是否为 #rrggbb 形式的 6 位十六进制颜色值。
func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// appearance 返回设置中的外观配置，缺省主题色填充为 defaultColor。
func (s Settings) appearance() Appearance {
	app := s.Appearance
	if app.Color == "" {
		app.Color = defaultColor
	}
	return app
}

// origins 返回设置中允许嵌入的网站，缺省为空切片。
func (s Settings) origins() []string {
	if s.AllowedOrigins == nil {
		return []string{}
	}
	return s.AllowedOrigins
}

// Schema 声明 OpenAPI 中的 Color 为字符串，由业务逻辑校验 #rrggbb 格式。
func (Color) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString}
}

// WidgetDTO 是接口中的聊天插件。
type WidgetDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	TeamID     *string    `json:"team_id,omitempty" doc:"新会话进入的团队收件箱；为空时进入未指派"`
	TeamName   string     `json:"team_name" doc:"新会话进入的团队收件箱的名称；未指派时为空"`
	Appearance Appearance `json:"appearance" doc:"聊天窗口的外观配置"`
	// AllowedOrigins 是允许嵌入聊天窗口的网站
	AllowedOrigins []string  `json:"allowed_origins" doc:"允许嵌入聊天窗口的网站；为空表示所有网站"`
	Enabled        bool      `json:"enabled" doc:"停用后聊天窗口不再显示，客户不能再发消息"`
	CreatedAt      time.Time `json:"created_at"`
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
			ID: ch.ID, Name: ch.Name, TeamID: ch.TeamID, Appearance: s.appearance(), AllowedOrigins: s.origins(),
			Enabled: ch.Enabled, CreatedAt: ch.CreatedAt,
		}
		if ch.TeamID != nil {
			out[i].TeamName = teams[*ch.TeamID]
		}
	}
	return out, nil
}
