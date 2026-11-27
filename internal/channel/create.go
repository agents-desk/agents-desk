package channel

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// CreateParams 是新建渠道的参数。
type CreateParams struct {
	Kind     Kind
	Audience Audience // 服务对象域；留空默认为 customer
	Name     string
	TeamID   *string        // 新会话进入的团队收件箱；为空时进入未指派
	Settings map[string]any // 连接器的设置
}

// Create 在工作区中新建渠道。渠道的设置页面在聊天插件的计划项中加入，现在由种子数据和测试调用。
type Create struct {
	db *bun.DB
}

// NewCreate 返回新建渠道的动作。
func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 在 sub 所在的工作区中新建一个启用的渠道，并记录操作日志。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, p CreateParams) (*Channel, error) {
	settings := p.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	aud := p.Audience
	if aud == "" {
		aud = AudienceCustomer
	}
	c := &Channel{ID: newID(), TenantID: sub.Tenant, Kind: p.Kind, Audience: aud, Name: p.Name, Settings: settings, TeamID: p.TeamID, Enabled: true}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert channel")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.created", TargetType: "channel", TargetID: c.ID, Properties: map[string]any{"kind": string(c.Kind)},
		})
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
