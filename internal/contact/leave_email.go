package contact

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// LeaveEmail 是线索在聊天插件等渠道中留下自己的邮箱，方便团队后续联系。
type LeaveEmail struct {
	db *bun.DB
}

// NewLeaveEmail 返回线索留邮箱的动作。
func NewLeaveEmail(db *bun.DB) *LeaveEmail { return &LeaveEmail{db: db} }

// Handle 由联系人 sub 在他所在的工作区中留下邮箱 email：只允许联系人修改自己，且仅限线索（Kind == Lead）；
// 已登录用户的邮箱由产品后端签名提供，返回 ErrNotLead。email 去掉首尾空白并转小写，格式由入口校验。
// changed 报告邮箱是否确实发生了变化，用于避免重复提交时追加重复的系统事件。
func (a *LeaveEmail) Handle(ctx context.Context, sub identity.Subject, email string) (*Contact, bool, error) {
	if sub.Type != Type {
		return nil, false, ErrForbidden.New()
	}
	normalized := strings.ToLower(strings.TrimSpace(email))
	var c *Contact
	var changed bool
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c = new(Contact)
		err := tx.NewSelect().Model(c).Where("id = ? AND tenant_id = ?", sub.ID, sub.Tenant).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load contact")
		}
		if c.Kind != Lead {
			return ErrNotLead.New()
		}
		if c.Email == normalized {
			return nil
		}
		c.Email, changed = normalized, true
		_, err = tx.NewUpdate().Model(c).Column("email", "updated_at").WherePK().Exec(ctx)
		return errors.Wrap(err, "save contact email")
	})
	if err != nil {
		return nil, false, err
	}
	return c, changed, nil
}
