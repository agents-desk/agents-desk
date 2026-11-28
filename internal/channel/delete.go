package channel

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// DeleteInput 是删除或归档公共渠道的 HTTP 请求。
type DeleteInput struct {
	ID string `path:"id" format:"uuid"`
}

// DeleteResult 是公共渠道删除或归档后的结果。
type DeleteResult struct {
	Mode string `json:"mode" enum:"deleted,archived" doc:"deleted 表示无历史关联已物理删除，archived 表示存在历史关联已软归档"`
}

// DeleteOutput 是删除或归档公共渠道的 HTTP 响应。
type DeleteOutput struct {
	Body DeleteResult
}

// Delete 删除无历史关联的公共渠道，或将已产生会话、联系人身份、外部群或附件引用的渠道软归档并停用。
type Delete struct {
	db        *bun.DB
	onChanged func(Kind)
}

// NewDelete 返回公共渠道删除或软归档动作；可选传入变更回调用于在归档后断开对应长连接。
func NewDelete(db *bun.DB, onChanged ...func(Kind)) *Delete {
	var fn func(Kind)
	if len(onChanged) > 0 {
		fn = onChanged[0]
	}
	return &Delete{db: db, onChanged: fn}
}

// Handle 校验渠道归属与类型；存在历史数据时停用并设置 archived_at，无引用时物理删除。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) (*DeleteResult, error) {
	var res DeleteResult
	var kind Kind
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch := new(Channel)
		err := tx.NewSelect().Model(ch).Where("id = ? AND tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "lock channel for delete")
		}
		if ch.IsPersonal() {
			return ErrPersonalProtected.New()
		}
		kind = ch.Kind

		var referenced bool
		if err := tx.NewRaw(`SELECT (
			EXISTS (SELECT 1 FROM conversations WHERE tenant_id = ? AND (channel_id = ? OR reply_channel_id = ?))
			OR EXISTS (SELECT 1 FROM timeline_items WHERE tenant_id = ? AND channel_id = ?)
			OR EXISTS (SELECT 1 FROM contact_identities WHERE tenant_id = ? AND channel_id = ?)
			OR EXISTS (SELECT 1 FROM external_groups WHERE tenant_id = ? AND channel_id = ?)
			OR EXISTS (SELECT 1 FROM chats WHERE channel_id = ?)
			OR EXISTS (SELECT 1 FROM timeline_attachments WHERE tenant_id = ? AND source_channel_id = ?)
		)`, sub.Tenant, ch.ID, ch.ID, sub.Tenant, ch.ID, sub.Tenant, ch.ID, sub.Tenant, ch.ID, ch.ID, sub.Tenant, ch.ID).
			Scan(ctx, &referenced); err != nil {
			return errors.Wrap(err, "check channel references")
		}

		if referenced {
			at := time.Now().UTC()
			if _, err := tx.NewUpdate().Table("channels").
				Set("enabled = FALSE").
				Set("archived_at = COALESCE(archived_at, ?)", at).
				Set("updated_at = ?", at).
				Where("id = ? AND tenant_id = ?", ch.ID, sub.Tenant).Exec(ctx); err != nil {
				return errors.Wrap(err, "archive channel")
			}
			res.Mode = "archived"
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "channel.archived",
				TargetType: "channel",
				TargetID:   ch.ID,
				Properties: map[string]any{"name": ch.Name, "kind": string(ch.Kind)},
			}); err != nil {
				return err
			}
		} else {
			if _, err := tx.NewDelete().Table("channels").
				Where("id = ? AND tenant_id = ?", ch.ID, sub.Tenant).Exec(ctx); err != nil {
				return errors.Wrap(err, "delete channel")
			}
			res.Mode = "deleted"
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "channel.deleted",
				TargetType: "channel",
				TargetID:   ch.ID,
				Properties: map[string]any{"name": ch.Name, "kind": string(ch.Kind)},
			}); err != nil {
				return err
			}
		}
		if a.onChanged != nil {
			cb := a.onChanged
			k := kind
			tx.AfterCommit(func(context.Context) error {
				cb(k)
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "processed channel removal", "tenant_id", sub.Tenant, "channel_id", id, "kind", kind, "mode", res.Mode)
	return &res, nil
}

// HTTP 处理 DELETE /t/{slug}/api/channels/{id}。
func (a *Delete) HTTP(ctx context.Context, in *DeleteInput) (*DeleteOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &DeleteOutput{Body: *res}, nil
}
