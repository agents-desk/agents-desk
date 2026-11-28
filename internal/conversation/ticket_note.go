package conversation

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// AddTicketNote 在工单中写一条内部讨论（仅成员可见），支持 @ 提及同事、媒体附件与发送前校对的译文。
type AddTicketNote struct {
	db   *bun.DB
	down Downstream
}

// NewAddTicketNote 返回在工单中写内部讨论的动作；down 是时间线有新内容时通知的下游。
func NewAddTicketNote(db *bun.DB, down Downstream) *AddTicketNote {
	return &AddTicketNote{db: db, down: down}
}

// Handle 在 sub 可见的工单 id 中写入一条内部讨论，@ 到成员 mentions，附带可选附件 attachments。
func (a *AddTicketNote) Handle(ctx context.Context, sub identity.Subject, id, text string, mentions []string, attachments ...AttachmentInput) (*ItemDTO, error) {
	return a.HandleBody(ctx, sub, id, NoteBody{Text: text, Mentions: mentions, Attachments: attachments})
}

// HandleBody 使用完整请求体 body 在工单 id 中写入内部讨论，并推进工单最近活动时间。
func (a *AddTicketNote) HandleBody(ctx context.Context, sub identity.Subject, id string, body NoteBody) (*ItemDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	if err := requireMessageContent(body.Text, len(body.Attachments)); err != nil {
		return nil, err
	}
	var item Item
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, _, err := findVisibleTicket(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		c.LastMessageAt = database.Now()
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

// HTTP 处理 POST /t/{slug}/api/tickets/{id}/notes。
func (a *AddTicketNote) HTTP(ctx context.Context, in *NoteInput) (*ItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := a.HandleBody(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ItemOutput{Body: *item}, nil
}
