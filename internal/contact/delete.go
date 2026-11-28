package contact

import (
	"context"
	"log/slog"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// DeleteInput 是删除或匿名化联系人的 HTTP 请求。
type DeleteInput struct {
	ID string `path:"id" format:"uuid"`
}

// DeleteResult 是联系人删除或匿名化后的结果。
type DeleteResult struct {
	Mode string `json:"mode" enum:"deleted,anonymized" doc:"deleted 表示无历史会话已物理删除，anonymized 表示存在历史会话已完成不可逆匿名化"`
}

// DeleteOutput 是删除或匿名化联系人的 HTTP 响应。
type DeleteOutput struct {
	Body DeleteResult
}

// Delete 删除无历史会话的联系人，或对已有会话/工单历史的联系人执行 GDPR 不可逆匿名化并吊销其访问令牌。
type Delete struct {
	db     *bun.DB
	tokens *apitoken.Store
}

// NewDelete 返回联系人删除或匿名化动作。
func NewDelete(db *bun.DB, tokens *apitoken.Store) *Delete {
	return &Delete{db: db, tokens: tokens}
}

// Handle 校验 sub 对联系人 id 的修改权限与可见范围；无关联会话及工单时物理删除，否则不可逆清除个人资料并封禁。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) (*DeleteResult, error) {
	var res DeleteResult
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if a.tokens != nil {
			if _, err := a.tokens.WithDB(tx).RevokeSubject(ctx, identity.Ref{Type: Type, ID: c.ID}); err != nil {
				return errors.Wrap(err, "revoke deleted contact tokens")
			}
		}

		hasConv, err := tx.NewSelect().Table("conversations").
			Where("tenant_id = ? AND contact_id = ?", sub.Tenant, c.ID).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check contact conversations before delete")
		}
		hasTicket, err := tx.NewSelect().Table("ticket_targets").
			Where("tenant_id = ? AND contact_id = ?", sub.Tenant, c.ID).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check contact tickets before delete")
		}

		// contact_identities 未设置 ON DELETE CASCADE，删前与匿名化都先清理渠道身份绑定。
		if _, err := tx.NewDelete().Table("contact_identities").
			Where("tenant_id = ? AND contact_id = ?", sub.Tenant, c.ID).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete contact identities")
		}

		if !hasConv && !hasTicket {
			if _, err := tx.NewDelete().Table("contacts").
				Where("tenant_id = ? AND id = ?", sub.Tenant, c.ID).Exec(ctx); err != nil {
				return errors.Wrap(err, "delete contact")
			}
			res.Mode = "deleted"
			return activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "contact.deleted",
				TargetType: string(Type),
				TargetID:   c.ID,
				Properties: map[string]any{"name": c.Name},
			})
		}

		if _, err := tx.NewDelete().Table("contact_tags").
			Where("tenant_id = ? AND contact_id = ?", sub.Tenant, c.ID).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete contact tags on anonymize")
		}
		at := now()
		if _, err := tx.NewUpdate().Table("contacts").
			Set("name = ''").
			Set("email = ''").
			Set("company = ''").
			Set("location = ''").
			Set("time_zone = ''").
			Set("referrer = ''").
			Set("notes = ''").
			Set("notes_derived = '{}'::jsonb").
			Set("external_user_id = ''").
			Set("member_id = NULL").
			Set("attributes = '{}'::jsonb").
			Set("blocked_at = COALESCE(blocked_at, ?)", at).
			Set("blocked_reason = 'anonymized'").
			Set("updated_at = ?", at).
			Where("tenant_id = ? AND id = ?", sub.Tenant, c.ID).
			Exec(ctx); err != nil {
			return errors.Wrap(err, "anonymize contact")
		}
		res.Mode = "anonymized"
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event:      "contact.anonymized",
			TargetType: string(Type),
			TargetID:   c.ID,
		})
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "processed contact removal", "tenant_id", sub.Tenant, "contact_id", id, "mode", res.Mode)
	return &res, nil
}

// HTTP 处理 DELETE /t/{slug}/api/contacts/{id}。
func (a *Delete) HTTP(ctx context.Context, in *DeleteInput) (*DeleteOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &DeleteOutput{Body: *res}, nil
}
