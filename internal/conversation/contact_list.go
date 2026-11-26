package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/contact"
)

// contactListLimit 是联系人的会话列表最多返回的会话数：一个客户在工作区中的会话不多，列表不分页。
const contactListLimit = 50

// ListForContact 是联系人在聊天插件中看到的会话列表。
type ListForContact struct {
	db *bun.DB
}

// NewListForContact 返回联系人的会话列表的动作。
func NewListForContact(db *bun.DB) *ListForContact { return &ListForContact{db: db} }

// Handle 返回联系人 sub 在渠道 channelID 中可见的会话，最近有消息的在前，最多 50 个。
// 已登录用户（contact.User）跨渠道查看自己在工作区名下的全部服务型会话；访客线索（contact.Lead）仅返回渠道 channelID 下的会话。
// sub 不是联系人时返回 ErrForbidden。
func (a *ListForContact) Handle(ctx context.Context, sub identity.Subject, channelID string) ([]ContactConversationDTO, error) {
	if sub.Type != contact.Type {
		return nil, ErrForbidden.New()
	}
	var kind contact.Kind
	err := a.db.NewSelect().Table("contacts").Column("kind").Where("id = ? AND tenant_id = ?", sub.ID, sub.Tenant).Scan(ctx, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return []ContactConversationDTO{}, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "load contact kind")
	}
	var convs []Conversation
	q := a.db.NewSelect().Model(&convs).
		Where("tenant_id = ? AND kind = ? AND contact_id = ?", sub.Tenant, Service, sub.ID)
	if kind != contact.User {
		q = q.Where("channel_id = ?", channelID)
	}
	err = q.OrderExpr("last_message_at DESC").Limit(contactListLimit).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list contact conversations")
	}
	return toContactDTOs(ctx, a.db, convs)
}
