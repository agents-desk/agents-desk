package conversation

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/push"
)

var (
	// ErrMessageNotAuthor 是不是作者本人编辑这条消息。
	ErrMessageNotAuthor = errorx.Define(errorx.PermissionDenied, "conversation.message_not_author")
	// ErrMessageNotEditable 是这条消息不能编辑：发给客户的消息要等更正流程，其他种类的会话不开放编辑。
	ErrMessageNotEditable = errorx.Define(errorx.Conflict, "conversation.message_not_editable")
	// ErrMessageEditWindow 是超过了可以编辑已发出消息的时间。
	ErrMessageEditWindow = errorx.Define(errorx.Conflict, "conversation.message_edit_window")
)

// editWindow 是可以编辑已经发出的消息的时间范围；内部备注不受限。
const editWindow = 15 * time.Minute

// EditMessageBody 是编辑消息的请求体。
type EditMessageBody struct {
	Text string `json:"text,omitempty" minLength:"1" maxLength:"10000" doc:"新的正文"`
}

// EditMessageInput 是编辑消息的 HTTP 请求。
type EditMessageInput struct {
	ID     string `path:"id" format:"uuid"`
	ItemID string `path:"item_id" format:"uuid"`
	Body   EditMessageBody
}

// EditMessage 编辑自己发出的一条消息或内部备注：时间线项保持原样，编辑作为新的修订追加。
// 只有不对客户发出的内容可以编辑（内部聊天的消息和内部备注），发出的消息要等更正流程。
type EditMessage struct {
	db   *bun.DB
	down Downstream
}

// NewEditMessage 返回编辑消息的动作；down 用于提交后通知能看到会话的成员。
func NewEditMessage(db *bun.DB, down Downstream) *EditMessage {
	return &EditMessage{db: db, down: down}
}

// Handle 由成员 sub 把会话 conversationID 中自己发出的消息 itemID 改成 text，并返回编辑后的时间线项。
// 不是作者返回 ErrMessageNotAuthor；消息不能编辑返回 ErrMessageNotEditable；超过时间返回 ErrMessageEditWindow。
func (a *EditMessage) Handle(ctx context.Context, sub identity.Subject, conversationID, itemID, text string) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrEmptyMessage.New()
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, conversationID, true)
		if err != nil {
			return err
		}
		if err := tx.NewSelect().Model(&item).
			Where("id = ? AND tenant_id = ? AND conversation_id = ?", itemID, c.TenantID, c.ID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound.New()
			}
			return errors.Wrap(err, "load item for edit")
		}
		if item.ActorType != MemberActor || item.ActorID != sub.ID {
			return ErrMessageNotAuthor.New()
		}
		if item.Kind != Message && item.Kind != Note {
			return ErrMessageNotEditable.New()
		}
		switch c.Kind {
		case Chat:
			var scope ChatScope
			if err := tx.NewSelect().Table("chats").Column("scope").
				Where("conversation_id = ?", c.ID).Scan(ctx, &scope); err != nil {
				return errors.Wrap(err, "load chat scope for edit")
			}
			if scope != ChatInternal {
				return ErrMessageNotEditable.New()
			}
		case Service:
			// 服务型会话只开放内部备注；发给客户的消息已经送达，改动要等更正流程
			if item.Kind != Note {
				return ErrMessageNotEditable.New()
			}
		default:
			return ErrMessageNotEditable.New()
		}
		if item.Kind == Message && now().Sub(item.CreatedAt) > editWindow {
			return ErrMessageEditWindow.New()
		}

		var next int
		if err := tx.NewRaw("SELECT coalesce(max(revision), 0) + 1 FROM message_revisions WHERE item_id = ?", item.ID).
			Scan(ctx, &next); err != nil {
			return errors.Wrap(err, "next message revision")
		}
		if _, err := tx.NewRaw(`INSERT INTO message_revisions (item_id, revision, tenant_id, conversation_id, text, editor_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			item.ID, next, c.TenantID, c.ID, text, sub.ID, now()).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert message revision")
		}
		if err := SearchIndex.Sync(ctx, tx, item.ID); err != nil {
			return err
		}
		if a.down.Hub != nil {
			audience, err := audienceOf(ctx, tx, a.down.Roles, c, nil)
			if err != nil {
				return err
			}
			if len(audience) > 0 {
				hub, tenant, ev := a.down.Hub, c.TenantID, push.Event{
					Type: pushType(c.Kind), ID: c.ID, Seq: c.LastSeq, ItemSeq: item.Seq,
				}
				tx.AfterCommit(func(ctx context.Context) error {
					return errors.Wrap(push.Send(ctx, hub, tenant, audience, ev), "push message edited")
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toItemDTOs(ctx, a.db, a.down.Media, []Item{item})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 PATCH /t/{slug}/api/conversations/{id}/items/{item_id}。
func (a *EditMessage) HTTP(ctx context.Context, in *EditMessageInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.ItemID, in.Body.Text)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *dto}, nil
}
