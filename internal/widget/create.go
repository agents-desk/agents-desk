package widget

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// Changes 是新建或修改聊天插件的请求。
type Changes struct {
	Name     string  `json:"name" minLength:"1" maxLength:"60"`
	TeamID   *string `json:"team_id,omitempty" format:"uuid" doc:"新会话进入的团队收件箱；不填时进入未指派"`
	Greeting string  `json:"greeting,omitempty" maxLength:"500" doc:"聊天窗口顶部的问候语；不填时显示默认的问候语"`
	Color    Color   `json:"color,omitempty" doc:"聊天窗口的主题色，不填时是 bright-blue"`
}

// color 返回 c 的主题色，没有选时是默认的颜色。
func (c Changes) color() Color {
	if c.Color == "" {
		return defaultColor
	}
	return c.Color
}

// CreateInput 是新建聊天插件的 HTTP 请求。
type CreateInput struct {
	Body Changes
}

// Create 在当前工作区中新建一个聊天插件，同时生成它的身份验证密钥。
type Create struct {
	db    *bun.DB
	crypt *crypt.Crypt
}

// NewCreate 返回新建聊天插件的动作；crypt 加密保存身份验证密钥。
func NewCreate(db *bun.DB, c *crypt.Crypt) *Create { return &Create{db: db, crypt: c} }

// Handle 在 sub 所在的工作区中新建一个启用的聊天插件并记录操作日志。团队收件箱不属于这个工作区时返回 ErrTeamNotFound。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, c Changes) (*WidgetDTO, error) {
	settings, err := Settings{Greeting: c.Greeting, Color: string(c.color()), IdentitySecret: a.crypt.Encrypt(newSecret())}.toMap()
	if err != nil {
		return nil, err
	}
	var id string
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := checkTeam(ctx, tx, sub.Tenant, c.TeamID); err != nil {
			return err
		}
		ch := &channel.Channel{ID: newID(), TenantID: sub.Tenant, Kind: channel.Widget, Name: c.Name, Settings: settings, TeamID: c.TeamID, Enabled: true}
		if _, err := tx.NewInsert().Model(ch).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert widget")
		}
		id = ch.ID
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.created", TargetType: "channel", TargetID: ch.ID, Properties: map[string]any{"kind": string(channel.Widget)},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 POST /t/{slug}/api/widgets。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*WidgetOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &WidgetOutput{Body: *dto}, nil
}
