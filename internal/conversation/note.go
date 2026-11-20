package conversation

import (
	"context"
	"fmt"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// AddNote 是成员在会话中写内部备注：只有成员可见，对方看不到，会话的等待状态保持原样。
type AddNote struct {
	db   *bun.DB
	down Downstream
}

// NewAddNote 返回写内部备注的动作；down 是时间线有新内容时通知的下游。
func NewAddNote(db *bun.DB, down Downstream) *AddNote { return &AddNote{db: db, down: down} }

// NoteInput 是写内部备注的 HTTP 请求：文字和 @ 到的成员。
type NoteInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		TextBody
		Mentions []string `json:"mentions,omitempty" maxItems:"20" uniqueItems:"true" doc:"@ 到的成员 ID；他们的“@我”中出现这个会话，并且能看到它"`
	}
}

// Handle 由 sub 在 sub 所在工作区的会话 id 中写一条内部备注，@ 到成员 mentions，sub 成为会话的参与者。
// 会话不存在或 sub 看不到时返回 ErrNotFound，没有权限时返回 ErrForbidden；
// @ 到的人不是这个工作区的成员时返回 member.ErrNotFound（字段 mentions[i]）。
func (a *AddNote) Handle(ctx context.Context, sub identity.Subject, id, text string, mentions []string) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		actor := actorOf(sub)
		roles := authz.NewStore(tx, auth.Catalog)
		for i, m := range mentions {
			if _, err := member.RoleOf(ctx, roles, identity.Ref{Type: member.Type, ID: m}, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field(fmt.Sprintf("mentions[%d]", i)))
				}
				return err
			}
		}
		item = Item{Kind: Note, ActorType: actor, ActorID: sub.ID, Visibility: MembersOnly, Content: Content{Text: text, Mentions: mentions}}
		if err := join(ctx, tx, c.ID, actor, sub.ID); err != nil {
			return err
		}
		if err := commit(ctx, tx, a.down, c, &item); err != nil {
			return err
		}
		for _, m := range mentions {
			_, err := tx.NewRaw(`INSERT INTO conversation_mentions (tenant_id, member_id, conversation_id, item_id, seq, created_at)
				VALUES (?, ?, ?, ?, ?, ?)`, sub.Tenant, m, c.ID, item.ID, item.Seq, item.CreatedAt).Exec(ctx)
			if err != nil {
				return errors.Wrap(err, "insert mention")
			}
		}
		return nil
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
func (a *AddNote) HTTP(ctx context.Context, in *NoteInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body.Text, in.Body.Mentions)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}
