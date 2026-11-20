package llm

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"
)

// SaveInput 是保存一种调用类型的接口的请求。
type SaveInput struct {
	CallType CallType `path:"call_type"`
	Body     Endpoint
}

// EndpointBody 是保存后的接口。
type EndpointBody struct {
	Body Endpoint
}

// SaveEndpoint 保存一种调用类型的模型接口，其他调用类型保持原样。
type SaveEndpoint struct {
	db    *bun.DB
	group *settings.Group[Settings]
}

// NewSaveEndpoint 返回保存一种调用类型的模型接口的动作。
func NewSaveEndpoint(db *bun.DB, group *settings.Group[Settings]) *SaveEndpoint {
	return &SaveEndpoint{db: db, group: group}
}

// Handle 在一个事务中锁住设置、只修改调用类型 t 的接口，有修改时记录操作日志。操作日志只记下改了哪些字段，不记录密钥。
func (a *SaveEndpoint) Handle(ctx context.Context, subject identity.Subject, t CallType, e Endpoint) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Update(ctx, func(s *Settings) error {
			cur := s.endpoint(t)
			changed = changes(*cur, e)
			*cur = e
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "platform_settings.changed", TargetType: "settings", TargetID: "llm",
			Properties: map[string]any{"call_type": string(t), "changed": changed},
		})
	})
}

// HTTP 处理 PUT /settings/model/{call_type}。
func (a *SaveEndpoint) HTTP(ctx context.Context, in *SaveInput) (*EndpointBody, error) {
	subject, _ := identity.FromContext(ctx)
	if err := a.Handle(ctx, subject, in.CallType, in.Body); err != nil {
		return nil, err
	}
	return &EndpointBody{Body: in.Body}, nil
}
