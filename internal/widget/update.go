package widget

import (
	"context"
	"maps"
	"slices"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// UpdateBody 是修改聊天插件的请求体：新建时的各项，以及是否启用。
type UpdateBody struct {
	Changes
	Enabled bool `json:"enabled" doc:"停用后聊天窗口不再显示，客户不能再发消息"`
}

// UpdateInput 是修改聊天插件的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBody
}

// Update 修改当前工作区中一个聊天插件的名称、团队收件箱、外观配置与允许嵌入的网站，启用或停用它。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改聊天插件的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 修改 sub 所在工作区中的聊天插件 id 并记录操作日志，身份验证密钥保持不变。聊天插件不存在时返回
// channel.ErrNotFound，团队收件箱不属于这个工作区时返回 ErrTeamNotFound，主题色格式不对时返回 ErrInvalidColor，
// 允许嵌入的网站写得不对时返回 ErrInvalidOrigin。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, b UpdateBody) (*WidgetDTO, error) {
	appearance, err := normalizeAppearance(b.Appearance)
	if err != nil {
		return nil, err
	}
	behavior, err := normalizeBehavior(b.Behavior)
	if err != nil {
		return nil, err
	}
	origins, err := normalizeOrigins(b.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if !sameTeam(ch.TeamID, b.TeamID) {
			if err := checkTeam(ctx, tx, sub.Tenant, b.TeamID); err != nil {
				return err
			}
		}
		s, err := settingsOf(ch)
		if err != nil {
			return err
		}
		var changed []string
		if ch.Name != b.Name {
			changed = append(changed, "name")
		}
		if !sameTeam(ch.TeamID, b.TeamID) {
			changed = append(changed, "team_id")
		}
		if ch.Enabled != b.Enabled {
			changed = append(changed, "enabled")
		}
		if s.appearance() != appearance || s.behavior() != behavior || !slices.Equal(s.AllowedOrigins, origins) {
			changed = append(changed, "settings")
		}
		if len(changed) == 0 {
			return nil
		}
		s.Appearance, s.Behavior, s.AllowedOrigins = appearance, behavior, origins
		settings, err := s.toMap()
		if err != nil {
			return err
		}
		maps.Copy(ch.Settings, settings)
		ch.Name, ch.TeamID, ch.Enabled = b.Name, b.TeamID, b.Enabled
		if _, err := tx.NewUpdate().Model(ch).Column("name", "team_id", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update widget")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.updated", TargetType: "channel", TargetID: id, Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/widgets/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*WidgetOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &WidgetOutput{Body: *dto}, nil
}

// sameTeam 报告两个团队收件箱是不是同一个，都为空也算。
func sameTeam(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
