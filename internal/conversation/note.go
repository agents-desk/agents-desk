package conversation

import (
	"context"
	"fmt"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

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

// NoteBody 是写内部备注的请求体：文字、@ 到的成员、可选媒体附件与发送前校对的译文。文字和附件不能同时为空。
type NoteBody struct {
	Text        string            `json:"text,omitempty" maxLength:"10000" doc:"内部备注的文字"`
	Mentions    []string          `json:"mentions,omitempty" maxItems:"20" uniqueItems:"true" doc:"@ 到的成员 ID；他们的“@我”中出现这个会话，并且能看到它"`
	Attachments []AttachmentInput `json:"attachments,omitempty" maxItems:"10" doc:"通过 /files 上传的媒体附件列表"`
	Translation *Translation      `json:"translation,omitempty" doc:"成员在发送前校对或微调过的某一种目标语言译文；消息正文仍保存原文，该译文直接写入派生译文供该语言成员查看"`
}

// NoteInput 是写内部备注的 HTTP 请求：文字、@ 到的成员和附件。
type NoteInput struct {
	ID   string `path:"id" format:"uuid"`
	Body NoteBody
}

// Handle 使用完整请求体 body 在会话 id 中写入内部备注，@ 到成员 mentions，附带可选附件，并支持发送前已校对的译文。
// 文字和附件都为空时返回 ErrEmptyMessage；会话不存在或 sub 看不到时返回 ErrNotFound，没有权限时返回 ErrForbidden；
// @ 到的人不是这个工作区的成员时返回 member.ErrNotFound（字段 mentions[i]）。
func (a *AddNote) Handle(ctx context.Context, sub identity.Subject, id string, body NoteBody) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	if err := requireMessageContent(body.Text, len(body.Attachments)); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if c.Kind != Service {
			return ErrWrongKind.New()
		}
		item, err = writeNote(ctx, tx, a.down, sub, c, body)
		return err
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

// writeNote 在事务 tx 中校验附件与 @ 成员，写入一条仅成员可见的 Note 时间线项及关联记录。
func writeNote(ctx context.Context, tx database.Tx, down Downstream, sub identity.Subject, c *Conversation, body NoteBody) (Item, error) {
	confirmed, err := confirmAttachmentInputs(ctx, tx, down.Media.Files, sub, body.Attachments)
	if err != nil {
		return Item{}, err
	}
	actor := actorOf(sub)
	roles := down.rolesWithDB(tx)
	for i, m := range body.Mentions {
		if _, err := member.RoleOf(ctx, roles, identity.Ref{Type: member.Type, ID: m}, sub.Tenant); err != nil {
			if errors.Is(err, member.ErrNotFound) {
				return Item{}, member.ErrNotFound.New(errorx.Field(fmt.Sprintf("mentions[%d]", i)))
			}
			return Item{}, err
		}
	}
	item := Item{Kind: Note, ActorType: actor, ActorID: sub.ID, Visibility: MembersOnly, Content: Content{Text: body.Text, Mentions: body.Mentions}}
	if err := applyVerifiedTranslation(ctx, tx, sub, &item, body.Translation); err != nil {
		return Item{}, err
	}
	if err := join(ctx, tx, c.ID, actor, sub.ID); err != nil {
		return Item{}, err
	}
	if err := commit(ctx, tx, down, c, &item); err != nil {
		return Item{}, err
	}
	if err := insertAttachments(ctx, tx, c.TenantID, item.ID, confirmed); err != nil {
		return Item{}, err
	}
	for _, m := range body.Mentions {
		_, err := tx.NewRaw(`INSERT INTO conversation_mentions (tenant_id, member_id, conversation_id, item_id, seq, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, sub.Tenant, m, c.ID, item.ID, item.Seq, item.CreatedAt).Exec(ctx)
		if err != nil {
			return Item{}, errors.Wrap(err, "insert mention")
		}
	}
	return item, nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/notes。
func (a *AddNote) HTTP(ctx context.Context, in *NoteInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}
