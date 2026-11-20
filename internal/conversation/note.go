package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// AddNote 是成员在会话中写内部备注：只有成员可见，对方看不到，会话的等待状态保持原样。
type AddNote struct {
	db   *bun.DB
	down Downstream
}

// NewAddNote 返回写内部备注的动作；down 是时间线有新内容时通知的下游。
func NewAddNote(db *bun.DB, down Downstream) *AddNote { return &AddNote{db: db, down: down} }

// Handle 由 sub 在 sub 所在工作区的会话 id 中写一条内部备注，sub 成为会话的参与者。
// 会话不存在时返回 ErrNotFound，没有权限时返回 ErrForbidden。
func (a *AddNote) Handle(ctx context.Context, sub identity.Subject, id, text string) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		actor := actorOf(sub)
		item = Item{Kind: Note, ActorType: actor, ActorID: sub.ID, Visibility: MembersOnly, Content: Content{Text: text}}
		if err := join(ctx, tx, c.ID, actor, sub.ID); err != nil {
			return err
		}
		return commit(ctx, tx, a.down, c, &item)
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toItemDTOs(ctx, a.db, []Item{item})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/notes。
func (a *AddNote) HTTP(ctx context.Context, in *TextInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body.Text)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}
