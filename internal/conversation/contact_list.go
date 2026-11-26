package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/contact"
)

// contactListLimit 是联系人的会话列表最多返回的会话数：一个客户在一个渠道中的会话不多，列表不分页。
const contactListLimit = 50

// ListForContact 是联系人在一个渠道中的会话列表，例如聊天插件中客户的会话列表。
type ListForContact struct {
	db *bun.DB
}

// NewListForContact 返回联系人的会话列表的动作。
func NewListForContact(db *bun.DB) *ListForContact { return &ListForContact{db: db} }

// Handle 返回联系人 sub 在渠道 channelID 中的会话，最近有消息的在前，最多 50 个。sub 不是联系人时返回 ErrForbidden。
func (a *ListForContact) Handle(ctx context.Context, sub identity.Subject, channelID string) ([]ContactConversationDTO, error) {
	if sub.Type != contact.Type {
		return nil, ErrForbidden.New()
	}
	var convs []Conversation
	err := a.db.NewSelect().Model(&convs).
		Where("tenant_id = ? AND kind = ? AND contact_id = ? AND channel_id = ?", sub.Tenant, Service, sub.ID, channelID).
		OrderExpr("last_message_at DESC").Limit(contactListLimit).Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list contact conversations")
	}
	return toContactDTOs(ctx, a.db, convs)
}
