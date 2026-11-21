package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// GetForContact 读取联系人在一个渠道中的一个会话。
type GetForContact struct {
	db *bun.DB
}

// NewGetForContact 返回联系人读取会话的动作。
func NewGetForContact(db *bun.DB) *GetForContact { return &GetForContact{db: db} }

// Handle 返回联系人 sub 在渠道 channelID 中的会话 id。sub 不是联系人时返回 ErrForbidden；会话不存在、
// 属于别人或别的渠道时返回 ErrNotFound。
func (a *GetForContact) Handle(ctx context.Context, sub identity.Subject, channelID, id string) (*ContactConversationDTO, error) {
	c, err := findForContact(ctx, a.db, sub, channelID, id, false)
	if err != nil {
		return nil, err
	}
	dtos, err := toContactDTOs(ctx, a.db, []Conversation{*c})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}
