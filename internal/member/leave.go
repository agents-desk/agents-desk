package member

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// Leave 在事务 tx 中让成员 memberID 离开工作区 tenant：撤销他在其中的全部角色，删除他的在线状态，
// 再执行其他模块登记的 hooks.Removed，例如把他移出这个工作区的团队收件箱。
// 移出成员、更换主管理员时原主管理员离开，都经过这里，离开后不留下他在这个工作区中的数据。
func Leave(ctx context.Context, tx database.Tx, hooks Hooks, tenant, memberID string) error {
	if _, err := authz.NewStore(tx, auth.Catalog).RemoveMember(ctx, identity.Ref{Type: Type, ID: memberID}, tenant); err != nil {
		return err
	}
	if _, err := tx.NewDelete().Model((*memberStatus)(nil)).Where("tenant_id = ? AND member_id = ?", tenant, memberID).Exec(ctx); err != nil {
		return errors.Wrap(err, "delete member status")
	}
	for _, removed := range hooks.Removed {
		if err := removed(ctx, tx, tenant, memberID); err != nil {
			return err
		}
	}
	return nil
}
