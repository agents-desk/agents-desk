package tenant

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// UpdateParams 是可以修改的字段：名称。slug 是地址，创建后不能修改。
type UpdateParams struct {
	Name string `json:"name" minLength:"1" maxLength:"60"`
}

// UpdateInput 是修改租户的请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateParams
}

// Update 修改租户的名称。
type Update struct {
	db     *bun.DB
	logins *login.Realm // 成员的 login.Realm，读取所有者的邮箱
	addr   Addresses
}

// NewUpdate 返回修改租户的动作。
func NewUpdate(db *bun.DB, logins *login.Realm, addr Addresses) *Update {
	return &Update{db: db, logins: logins, addr: addr}
}

// Handle 修改租户 id 的名称并记录操作日志，名称没有变化时什么也不做。租户不存在时返回 ErrNotFound。
func (a *Update) Handle(ctx context.Context, subject identity.Subject, id string, p UpdateParams) (*Tenant, error) {
	var t *Tenant
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if t, err = find(ctx, tx, id, true); err != nil {
			return err
		}
		if t.Name == p.Name {
			return nil
		}
		old := t.Name
		t.Name, t.UpdatedAt = p.Name, time.Now().UTC()
		if _, err := tx.NewUpdate().Model(t).Column("name", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update tenant")
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "tenant.renamed", TargetType: "tenant", TargetID: t.ID,
			Properties: map[string]any{"from": old, "to": t.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// HTTP 处理 PUT /tenants/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*TenantOutput, error) {
	subject, _ := identity.FromContext(ctx)
	t, err := a.Handle(ctx, subject, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	dto, err := toDTO(ctx, a.logins, a.addr, t)
	if err != nil {
		return nil, err
	}
	return &TenantOutput{Body: dto}, nil
}
