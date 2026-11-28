package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
	"agents-desk/internal/tag"
)

// AddTagInput 是给会话或联系人添加标签的请求。
type AddTagInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		TagID string `json:"tag_id" format:"uuid"`
	}
}

// RemoveTagInput 是从会话或联系人移除标签的请求。
type RemoveTagInput struct {
	ID    string `path:"id" format:"uuid"`
	TagID string `path:"tag_id" format:"uuid"`
}

// AddTag 给会话添加标签。
type AddTag struct {
	db   *bun.DB
	down Downstream
}

// NewAddTag 返回给会话添加标签的动作。
func NewAddTag(db *bun.DB, down Downstream) *AddTag { return &AddTag{db: db, down: down} }

// Handle 由 sub 给会话 id 添加标签 tagID，并追加系统事件 conversation.tag_added；已经打过该标签时什么也不做。
func (a *AddTag) Handle(ctx context.Context, sub identity.Subject, id, tagID string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		return applyAddTag(ctx, tx, sub, c, tagID)
	})
}

// applyAddTag 在事务 tx 中给会话 c 添加标签 tagID，返回要追加的系统事件；已存在该标签时返回 nil。
func applyAddTag(ctx context.Context, tx bun.IDB, sub identity.Subject, c *Conversation, tagID string) ([]*Item, error) {
	if c.Kind == Chat {
		return nil, ErrWrongKind.New()
	}
	t, err := tag.Find(ctx, tx, c.TenantID, tagID, false)
	if err != nil {
		return nil, err
	}
	res, err := tx.NewRaw(`INSERT INTO conversation_tags (tenant_id, conversation_id, tag_id, created_at)
		VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, c.TenantID, c.ID, t.ID, database.Now()).Exec(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "add conversation tag")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, errors.Wrap(err, "count added conversation tag")
	}
	if n == 0 {
		return nil, nil
	}
	return []*Item{event(sub, "conversation.tag_added", map[string]any{"tag_id": t.ID, "name": t.Name})}, nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/tags。
func (a *AddTag) HTTP(ctx context.Context, in *AddTagInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.TagID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}

// RemoveTag 从会话移除标签。
type RemoveTag struct {
	db   *bun.DB
	down Downstream
}

// NewRemoveTag 返回从会话移除标签的动作。
func NewRemoveTag(db *bun.DB, down Downstream) *RemoveTag { return &RemoveTag{db: db, down: down} }

// Handle 由 sub 从会话 id 移除标签 tagID，并追加系统事件 conversation.tag_removed；原本没有该标签时什么也不做。
func (a *RemoveTag) Handle(ctx context.Context, sub identity.Subject, id, tagID string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		if c.Kind == Chat {
			return nil, ErrWrongKind.New()
		}
		t, err := tag.Find(ctx, tx, c.TenantID, tagID, false)
		if err != nil {
			return nil, err
		}
		res, err := tx.NewRaw(`DELETE FROM conversation_tags WHERE tenant_id = ? AND conversation_id = ? AND tag_id = ?`,
			c.TenantID, c.ID, t.ID).Exec(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "remove conversation tag")
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, errors.Wrap(err, "count removed conversation tag")
		}
		if n == 0 {
			return nil, nil
		}
		return []*Item{event(sub, "conversation.tag_removed", map[string]any{"tag_id": t.ID, "name": t.Name})}, nil
	})
}

// HTTP 处理 DELETE /t/{slug}/api/conversations/{id}/tags/{tag_id}。
func (a *RemoveTag) HTTP(ctx context.Context, in *RemoveTagInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.TagID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}

// AddContactTag 在会话上下文中给联系人添加标签，并在会话时间线中记录系统事件。
type AddContactTag struct {
	db   *bun.DB
	down Downstream
}

// NewAddContactTag 返回给会话联系人添加标签的动作。
func NewAddContactTag(db *bun.DB, down Downstream) *AddContactTag {
	return &AddContactTag{db: db, down: down}
}

// Handle 由 sub 给会话 id 的联系人添加标签 tagID，并追加系统事件 contact.tag_added；已经打过该标签时什么也不做。
func (a *AddContactTag) Handle(ctx context.Context, sub identity.Subject, id, tagID string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		if c.ContactID == nil {
			return nil, contact.ErrNotFound.New()
		}
		t, added, err := contact.AddTag(ctx, tx, c.TenantID, *c.ContactID, tagID)
		if err != nil || !added {
			return nil, err
		}
		return []*Item{event(sub, "contact.tag_added", map[string]any{
			"contact_id": *c.ContactID, "tag_id": t.ID, "name": t.Name,
		})}, nil
	})
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/contact/tags。
func (a *AddContactTag) HTTP(ctx context.Context, in *AddTagInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.TagID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}

// RemoveContactTag 在会话上下文中从联系人移除标签，并在会话时间线中记录系统事件。
type RemoveContactTag struct {
	db   *bun.DB
	down Downstream
}

// NewRemoveContactTag 返回从会话联系人移除标签的动作。
func NewRemoveContactTag(db *bun.DB, down Downstream) *RemoveContactTag {
	return &RemoveContactTag{db: db, down: down}
}

// Handle 由 sub 从会话 id 的联系人移除标签 tagID，并追加系统事件 contact.tag_removed；原本没有该标签时什么也不做。
func (a *RemoveContactTag) Handle(ctx context.Context, sub identity.Subject, id, tagID string) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		if c.ContactID == nil {
			return nil, contact.ErrNotFound.New()
		}
		t, removed, err := contact.RemoveTag(ctx, tx, c.TenantID, *c.ContactID, tagID)
		if err != nil || !removed {
			return nil, err
		}
		return []*Item{event(sub, "contact.tag_removed", map[string]any{
			"contact_id": *c.ContactID, "tag_id": t.ID, "name": t.Name,
		})}, nil
	})
}

// HTTP 处理 DELETE /t/{slug}/api/conversations/{id}/contact/tags/{tag_id}。
func (a *RemoveContactTag) HTTP(ctx context.Context, in *RemoveTagInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.TagID)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
