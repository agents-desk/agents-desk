package organization

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// DeleteInput 是删除客户公司/组织的 HTTP 请求。
type DeleteInput struct {
	ID string `path:"id" format:"uuid"`
}

// Delete 删除工作区中的客户公司/组织，外键约束会自动清空联系人与外部群上的关联 ID。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除组织的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 校验权限与工作区归属后物理删除组织并记录操作日志。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	var orgName string
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		o := new(Organization)
		err := tx.NewSelect().Model(o).Where("org.id = ? AND org.tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "lock organization for delete")
		}
		orgName = o.Name
		if _, err := tx.NewDelete().Table("organizations").
			Where("id = ? AND tenant_id = ?", id, sub.Tenant).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete organization")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event:      "organization.deleted",
			TargetType: "organization",
			TargetID:   o.ID,
			Properties: map[string]any{"name": o.Name},
		})
	})
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "deleted organization", "tenant_id", sub.Tenant, "organization_id", id, "name", orgName)
	return nil
}

// HTTP 处理 DELETE /t/{slug}/api/organizations/{id}。
func (a *Delete) HTTP(ctx context.Context, in *DeleteInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
