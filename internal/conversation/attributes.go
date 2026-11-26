package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
)

// AttributesInput 是修改会话或联系人自定义属性的请求：只传需要更新或清空（值为 null）的属性键。
type AttributesInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Attributes map[string]any `json:"attributes" doc:"要更新的自定义属性，值为 null 时清空该属性"`
	}
}

// PatchAttributes 修改会话的自定义属性。
type PatchAttributes struct {
	db   *bun.DB
	down Downstream
}

// NewPatchAttributes 返回修改会话属性的动作。
func NewPatchAttributes(db *bun.DB, down Downstream) *PatchAttributes {
	return &PatchAttributes{db: db, down: down}
}

// Handle 由 sub 按属性定义更新会话 id 的自定义属性，并追加系统事件 conversation.attributes_changed；没有变化时什么也不做。
func (a *PatchAttributes) Handle(ctx context.Context, sub identity.Subject, id string, patch map[string]any) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		return applyAttributes(ctx, tx, sub, c, patch)
	})
}

// applyAttributes 在事务 tx 中按工作区定义合并会话或工单 c 的自定义属性，返回系统事件；未变化时返回 nil。
func applyAttributes(ctx context.Context, tx bun.IDB, sub identity.Subject, c *Conversation, patch map[string]any) ([]*Item, error) {
	var (
		next    map[string]any
		changes []attribute.Change
		err     error
	)
	if c.Kind == Ticket {
		var typeID string
		if err = tx.NewSelect().Table("tickets").Column("type_id").Where("conversation_id = ?", c.ID).Scan(ctx, &typeID); err != nil {
			return nil, err
		}
		next, changes, err = attribute.ApplyTicketPatch(ctx, tx, c.TenantID, typeID, c.Attributes, patch)
	} else {
		next, changes, err = attribute.ApplyPatch(ctx, tx, c.TenantID, attribute.TargetConversation, c.Attributes, patch)
	}
	if err != nil || len(changes) == 0 {
		return nil, err
	}
	c.Attributes = next
	return []*Item{event(sub, "conversation.attributes_changed", map[string]any{"changes": changes})}, nil
}

// HTTP 处理 PATCH /t/{slug}/api/conversations/{id}/attributes。
func (a *PatchAttributes) HTTP(ctx context.Context, in *AttributesInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Attributes)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}

// PatchContactAttributes 在会话上下文中修改联系人的自定义属性，并在会话时间线中记录系统事件。
type PatchContactAttributes struct {
	db   *bun.DB
	down Downstream
}

// NewPatchContactAttributes 返回修改会话联系人属性的动作。
func NewPatchContactAttributes(db *bun.DB, down Downstream) *PatchContactAttributes {
	return &PatchContactAttributes{db: db, down: down}
}

// Handle 由 sub 按属性定义更新会话 id 对应联系人的自定义属性，并追加系统事件 contact.attributes_changed；没有变化时什么也不做。
func (a *PatchContactAttributes) Handle(ctx context.Context, sub identity.Subject, id string, patch map[string]any) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		if c.ContactID == nil {
			return nil, contact.ErrNotFound.New()
		}
		changes, err := contact.PatchAttributes(ctx, tx, c.TenantID, *c.ContactID, patch)
		if err != nil || len(changes) == 0 {
			return nil, err
		}
		return []*Item{event(sub, "contact.attributes_changed", map[string]any{
			"contact_id": *c.ContactID, "changes": changes,
		})}, nil
	})
}

// HTTP 处理 PATCH /t/{slug}/api/conversations/{id}/contact/attributes。
func (a *PatchContactAttributes) HTTP(ctx context.Context, in *AttributesInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Attributes)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
