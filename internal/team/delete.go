package team

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// Delete 删除当前工作区中一个空的团队收件箱，成员关系随之删除，成员本身不受影响。
// 只要有会话（包括已关闭的历史会话）、关闭记录或者渠道默认团队收件箱引用，就不能删除，管理员可以改为停用。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除团队收件箱的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 删除 sub 所在工作区中的团队收件箱 id 并记录操作日志。不存在时返回 ErrNotFound；还在被使用时返回 ErrInUse，
// 带有相关会话数 conversations（含历史会话和关闭记录）和以它为默认的渠道数 channels。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		// 团队收件箱被锁住，新会话不会在检查之后进入它：进入时要通过外键锁住这一行
		var use struct {
			Conversations int `bun:"conversations"`
			Channels      int `bun:"channels"`
		}
		err = tx.NewRaw(`SELECT
			(SELECT count(*) FROM (
				SELECT id FROM conversations WHERE tenant_id = ? AND team_id = ?
				UNION
				SELECT conversation_id AS id FROM conversation_rounds WHERE tenant_id = ? AND team_id = ?
			) u) AS conversations,
			(SELECT count(*) FROM channels WHERE tenant_id = ? AND team_id = ?) AS channels`,
			sub.Tenant, id, sub.Tenant, id, sub.Tenant, id).Scan(ctx, &use)
		if err != nil {
			return errors.Wrap(err, "count team usage")
		}
		if use.Conversations > 0 || use.Channels > 0 {
			return ErrInUse.New(errorx.Public("conversations", use.Conversations), errorx.Public("channels", use.Channels))
		}
		if _, err := tx.NewDelete().Model(t).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "delete team")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "team.deleted", TargetType: "team", TargetID: id, Properties: map[string]any{"name": t.Name},
		})
	})
}

// HTTP 处理 DELETE /t/{slug}/api/teams/{id}。
func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
