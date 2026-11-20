package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// IDInput 是指定一个会话的请求：路径中的会话 ID。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Get 读取当前工作区中的一个会话。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取会话的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回 sub 所在工作区中的会话 id，不存在、属于别的工作区或 sub 看不到时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*ConversationDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	c, err := findVisible(ctx, a.db, sub, id, false)
	if err != nil {
		return nil, err
	}
	dtos, err := toDTOs(ctx, a.db, sub, []Conversation{*c})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 GET /t/{slug}/api/conversations/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
