package contact

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// BlockBody 是拉黑联系人的请求体。
type BlockBody struct {
	Reason string `json:"reason,omitempty" maxLength:"500" doc:"拉黑原因说明"`
}

// BlockInput 是拉黑联系人的 HTTP 请求。
type BlockInput struct {
	ID   string `path:"id" format:"uuid"`
	Body BlockBody
}

// Block 将联系人加入黑名单并吊销其全部网页聊天插件访问令牌。
type Block struct {
	db     *bun.DB
	tokens *apitoken.Store
}

// NewBlock 返回拉黑联系人的动作。
func NewBlock(db *bun.DB, tokens *apitoken.Store) *Block {
	return &Block{db: db, tokens: tokens}
}

// Handle 将 sub 所在工作区中可见的联系人 id 标为已拉黑、写入原因与操作日志并撤销令牌。
func (a *Block) Handle(ctx context.Context, sub identity.Subject, id string, b BlockBody) (*ContactDetailDTO, error) {
	reason := strings.TrimSpace(b.Reason)
	var updated *Contact
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		at := database.Now()
		c.BlockedAt = &at
		c.BlockedReason = reason
		c.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(c).Column("blocked_at", "blocked_reason", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "block contact")
		}
		if a.tokens != nil {
			if _, err := a.tokens.WithDB(tx).RevokeSubject(ctx, identity.Ref{Type: Type, ID: c.ID}); err != nil {
				return errors.Wrap(err, "revoke blocked contact tokens")
			}
		}
		if err := activity.Record(ctx, tx, sub, activity.Activity{
			Event: "contact.blocked", TargetType: string(Type), TargetID: c.ID,
			Properties: map[string]any{"reason": reason},
		}); err != nil {
			return err
		}
		updated = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return loadDetail(ctx, a.db, sub, updated)
}

// HTTP 处理 POST /t/{slug}/api/contacts/{id}/block。
func (a *Block) HTTP(ctx context.Context, in *BlockInput) (*ContactDetailOutput, error) {
	sub, _ := identity.FromContext(ctx)
	d, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ContactDetailOutput{Body: *d}, nil
}

// UnblockInput 是解除联系人拉黑的 HTTP 请求。
type UnblockInput struct {
	ID string `path:"id" format:"uuid"`
}

// Unblock 解除联系人的拉黑状态。
type Unblock struct {
	db *bun.DB
}

// NewUnblock 返回解除联系人拉黑的动作。
func NewUnblock(db *bun.DB) *Unblock { return &Unblock{db: db} }

// Handle 清空 sub 所在工作区联系人 id 的拉黑时间与原因并记录操作日志。
func (a *Unblock) Handle(ctx context.Context, sub identity.Subject, id string) (*ContactDetailDTO, error) {
	var updated *Contact
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		at := database.Now()
		c.BlockedAt = nil
		c.BlockedReason = ""
		c.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(c).Column("blocked_at", "blocked_reason", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "unblock contact")
		}
		if err := activity.Record(ctx, tx, sub, activity.Activity{
			Event: "contact.unblocked", TargetType: string(Type), TargetID: c.ID,
		}); err != nil {
			return err
		}
		updated = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return loadDetail(ctx, a.db, sub, updated)
}

// HTTP 处理 POST /t/{slug}/api/contacts/{id}/unblock。
func (a *Unblock) HTTP(ctx context.Context, in *UnblockInput) (*ContactDetailOutput, error) {
	sub, _ := identity.FromContext(ctx)
	d, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ContactDetailOutput{Body: *d}, nil
}
