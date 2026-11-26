package conversation

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
)

// UpdateChatParams 是修改群聊名称的请求体。
type UpdateChatParams struct {
	Name string `json:"name" minLength:"1" maxLength:"80" doc:"新的群聊名称"`
}

// UpdateChatInput 是修改群聊名称的 HTTP 请求。
type UpdateChatInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateChatParams
}

// UpdateChat 修改群聊名称（单聊拒绝）。
type UpdateChat struct {
	db   *bun.DB
	down Downstream
}

// NewUpdateChat 返回修改群聊名称的动作。
func NewUpdateChat(db *bun.DB, down Downstream) *UpdateChat {
	return &UpdateChat{db: db, down: down}
}

// Handle 将群聊 id 改名为 p.Name，并在时间线写入 chat.renamed 系统事件。
func (a *UpdateChat) Handle(ctx context.Context, sub identity.Subject, id string, p UpdateChatParams) (*ChatDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, ErrChatNameRequired.New(errorx.Field("name"))
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, cm, err := findVisibleChat(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if cm.Mode != ChatGroup {
			return ErrChatGroupOnly.New()
		}
		if cm.Name == name {
			return nil
		}
		from := cm.Name
		cm.Name = name
		if _, err := tx.NewUpdate().Model(cm).Column("name", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "rename group chat")
		}
		c.LastMessageAt = now()
		ev := event(sub, "chat.renamed", map[string]any{"from": from, "to": name})
		return commit(ctx, tx, a.down, c, ev)
	})
	if err != nil {
		return nil, err
	}
	return NewGetChat(a.db, a.down.Media.Files).Handle(ctx, sub, id)
}

// HTTP 处理 PATCH /t/{slug}/api/chats/{id}。
func (a *UpdateChat) HTTP(ctx context.Context, in *UpdateChatInput) (*ChatOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ChatOutput{Body: *dto}, nil
}

// AddChatParticipantsBody 是向群聊添加成员的请求体。
type AddChatParticipantsBody struct {
	MemberIDs []string `json:"member_ids" minItems:"1" maxItems:"100" uniqueItems:"true" doc:"要加入群聊的工作区成员 ID"`
}

// AddChatParticipantsInput 是向群聊添加成员的 HTTP 请求。
type AddChatParticipantsInput struct {
	ID   string `path:"id" format:"uuid"`
	Body AddChatParticipantsBody
}

// AddChatParticipants 向群聊中添加成员（单聊拒绝）。
type AddChatParticipants struct {
	db   *bun.DB
	down Downstream
}

// NewAddChatParticipants 返回向群聊添加成员的动作。
func NewAddChatParticipants(db *bun.DB, down Downstream) *AddChatParticipants {
	return &AddChatParticipants{db: db, down: down}
}

// Handle 将 memberIDs 中尚未在群里的工作区成员加入群聊 id，并写入 chat.member_added 系统事件。
func (a *AddChatParticipants) Handle(ctx context.Context, sub identity.Subject, id string, memberIDs []string) (*ChatDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, cm, err := findVisibleChat(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if cm.Mode != ChatGroup {
			return ErrChatGroupOnly.New()
		}
		roles := authz.NewStore(tx, auth.Catalog)
		var added []string
		for i, mid := range memberIDs {
			if _, err := member.RoleOf(ctx, roles, identity.Ref{Type: member.Type, ID: mid}, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field(fmt.Sprintf("member_ids[%d]", i)))
				}
				return err
			}
			res, err := tx.NewRaw(`INSERT INTO conversation_participants (conversation_id, participant_type, participant_id, created_at)
				VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, c.ID, MemberActor, mid, now()).Exec(ctx)
			if err != nil {
				return errors.Wrap(err, "add chat participant")
			}
			if n, _ := res.RowsAffected(); n > 0 {
				added = append(added, mid)
			}
		}
		if len(added) == 0 {
			return nil
		}
		c.LastMessageAt = now()
		ev := event(sub, "chat.member_added", map[string]any{"member_ids": added})
		return commit(ctx, tx, a.down, c, ev)
	})
	if err != nil {
		return nil, err
	}
	return NewGetChat(a.db, a.down.Media.Files).Handle(ctx, sub, id)
}

// HTTP 处理 POST /t/{slug}/api/chats/{id}/participants。
func (a *AddChatParticipants) HTTP(ctx context.Context, in *AddChatParticipantsInput) (*ChatOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.MemberIDs)
	if err != nil {
		return nil, err
	}
	return &ChatOutput{Body: *dto}, nil
}

// RemoveChatParticipantInput 是从群聊移出参与者的 HTTP 请求。
type RemoveChatParticipantInput struct {
	ID              string    `path:"id" format:"uuid"`
	ParticipantType ActorType `path:"participant_type" doc:"参与者类型，例如 member 或 bot"`
	ParticipantID   string    `path:"participant_id" format:"uuid"`
}

// RemoveChatParticipant 将指定参与者移出群聊（单聊拒绝）。
type RemoveChatParticipant struct {
	db   *bun.DB
	down Downstream
}

// NewRemoveChatParticipant 返回移出群聊成员的动作。
func NewRemoveChatParticipant(db *bun.DB, down Downstream) *RemoveChatParticipant {
	return &RemoveChatParticipant{db: db, down: down}
}

// Handle 将 (participantType, participantID) 从群聊 id 移出，记入系统事件并同时通知被移出的成员刷新列表。
func (a *RemoveChatParticipant) Handle(ctx context.Context, sub identity.Subject, id string, participantType ActorType, participantID string) (*ChatDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	removedSelf := sub.Type == member.Type && participantType == MemberActor && participantID == sub.ID
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, cm, err := findVisibleChat(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if cm.Mode != ChatGroup {
			return ErrChatGroupOnly.New()
		}
		res, err := tx.NewDelete().Model((*Participant)(nil)).
			Where("conversation_id = ? AND participant_type = ? AND participant_id = ?", c.ID, participantType, participantID).
			Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "remove chat participant")
		}
		n, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "remove chat participant rows affected")
		}
		if n == 0 {
			return ErrChatNotParticipant.New()
		}

		c.LastMessageAt = now()
		evName := "chat.member_removed"
		if removedSelf {
			evName = "chat.member_left"
		}
		ev := event(sub, evName, map[string]any{"participant_type": string(participantType), "participant_id": participantID})
		if err := commit(ctx, tx, a.down, c, ev); err != nil {
			return err
		}
		// 也向已被移出/退出的成员推送一次事件，让其侧栏实时移除该群聊
		if a.down.Hub != nil && participantType == MemberActor {
			hub, tenant, targetID, chatEv := a.down.Hub, c.TenantID, participantID, push.Event{Type: push.Chat, ID: c.ID, Seq: c.LastSeq}
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Wrap(push.Send(ctx, hub, tenant, []string{targetID}, chatEv), "push chat participant removed")
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if removedSelf {
		return nil, nil
	}
	return NewGetChat(a.db, a.down.Media.Files).Handle(ctx, sub, id)
}

// HTTP 处理 DELETE /t/{slug}/api/chats/{id}/participants/{participant_type}/{participant_id}。
func (a *RemoveChatParticipant) HTTP(ctx context.Context, in *RemoveChatParticipantInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	_, err := a.Handle(ctx, sub, in.ID, in.ParticipantType, in.ParticipantID)
	return nil, err
}

// LeaveChat 让当前登录成员退出群聊（单聊拒绝）。
type LeaveChat struct {
	remove *RemoveChatParticipant
}

// NewLeaveChat 返回退出群聊的动作。
func NewLeaveChat(db *bun.DB, down Downstream) *LeaveChat {
	return &LeaveChat{remove: NewRemoveChatParticipant(db, down)}
}

// Handle 让成员 sub 退出群聊 id。
func (a *LeaveChat) Handle(ctx context.Context, sub identity.Subject, id string) error {
	if sub.Type != member.Type {
		return ErrForbidden.New()
	}
	_, err := a.remove.Handle(ctx, sub, id, MemberActor, sub.ID)
	return err
}

// HTTP 处理 POST /t/{slug}/api/chats/{id}/leave。
func (a *LeaveChat) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}
