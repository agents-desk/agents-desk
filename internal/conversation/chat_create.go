package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// CreateDirectChatParams 是发起或打开单聊的请求体；participant_type 默认为 member，预留 bot 以支持未来 AI Agent 单聊。
type CreateDirectChatParams struct {
	ParticipantType ActorType `json:"participant_type,omitempty" doc:"对方参与者类型：member（默认）或未来扩展的 bot"`
	ParticipantID   string    `json:"participant_id" format:"uuid" doc:"对方参与者 ID"`
}

// CreateDirectChatInput 是发起单聊的 HTTP 请求。
type CreateDirectChatInput struct {
	Body CreateDirectChatParams
}

// CreateDirectChat 在当前工作区中幂等创建或打开与指定成员（或未来机器人）的单聊。
type CreateDirectChat struct {
	db   *bun.DB
	down Downstream
}

// NewCreateDirectChat 返回发起单聊的动作。
func NewCreateDirectChat(db *bun.DB, down Downstream) *CreateDirectChat {
	return &CreateDirectChat{db: db, down: down}
}

// Handle 幂等返回成员 sub 与目标参与者之间的唯一单聊：已存在直接返回，不存在则创建会话、chats 记录与双方参与者。
func (a *CreateDirectChat) Handle(ctx context.Context, sub identity.Subject, p CreateDirectChatParams) (*ChatDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if sub.Type != member.Type {
		return nil, ErrForbidden.New()
	}
	targetType := p.ParticipantType
	if targetType == "" {
		targetType = MemberActor
	}
	if targetType != MemberActor && targetType != BotActor {
		return nil, ErrChatUnsupportedParticipant.New(errorx.Field("participant_type"))
	}
	if targetType == MemberActor && p.ParticipantID == sub.ID {
		return nil, ErrChatSelfDirect.New(errorx.Field("participant_id"))
	}

	dmKey := buildDMKey(MemberActor, sub.ID, targetType, p.ParticipantID)
	var convID string
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if targetType == BotActor {
			ok, err := botEnabledInTenant(ctx, tx, sub.Tenant, p.ParticipantID)
			if err != nil {
				return err
			}
			if !ok {
				return ErrChatUnsupportedParticipant.New(errorx.Field("participant_id"))
			}
		} else {
			// 校验目标成员属于当前工作区
			if _, err := member.RoleOf(ctx, a.down.rolesWithDB(tx), identity.Ref{Type: member.Type, ID: p.ParticipantID}, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field("participant_id"))
				}
				return err
			}
		}

		// 已有单聊直接复用
		var existing string
		err := tx.NewSelect().Table("chats").Column("conversation_id").
			Where("tenant_id = ? AND dm_key = ?", sub.Tenant, dmKey).Scan(ctx, &existing)
		if err == nil && existing != "" {
			convID = existing
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errors.Wrap(err, "find existing direct chat")
		}

		at := database.Now()
		c := &Conversation{
			ID:            database.NewID(),
			TenantID:      sub.Tenant,
			Kind:          Chat,
			HandlingMode:  HandlingHuman,
			Status:        Open,
			Priority:      Normal,
			OpenedAt:      at,
			LastMessageAt: at,
			Attributes:    map[string]any{},
		}
		if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert direct chat conversation")
		}
		createdBy := sub.ID
		key := dmKey
		cm := &ChatMeta{
			ConversationID: c.ID,
			TenantID:       sub.Tenant,
			Mode:           ChatDirect,
			Scope:          ChatInternal,
			Name:           "",
			DMKey:          &key,
			CreatedBy:      &createdBy,
		}
		if _, err := tx.NewInsert().Model(cm).Exec(ctx); err != nil {
			return err
		}
		if err := join(ctx, tx, c.ID, MemberActor, sub.ID); err != nil {
			return err
		}
		if err := join(ctx, tx, c.ID, targetType, p.ParticipantID); err != nil {
			return err
		}
		convID = c.ID
		return nil
	})
	if err != nil {
		// 并发同时建单聊触发唯一键冲突时，直接读回先落库的同一条单聊
		if database.IsUniqueViolation(err, "chats_tenant_dm_key") {
			if scanErr := a.db.NewSelect().Table("chats").Column("conversation_id").
				Where("tenant_id = ? AND dm_key = ?", sub.Tenant, dmKey).Scan(ctx, &convID); scanErr != nil {
				return nil, errors.Wrap(scanErr, "reload concurrent direct chat")
			}
		} else {
			return nil, err
		}
	}
	return NewGetChat(a.db, a.down.Media.Files).Handle(ctx, sub, convID)
}

// HTTP 处理 POST /t/{slug}/api/chats/direct。
func (a *CreateDirectChat) HTTP(ctx context.Context, in *CreateDirectChatInput) (*ChatOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ChatOutput{Body: *dto}, nil
}

// CreateGroupChatParams 是新建群聊的请求体。
type CreateGroupChatParams struct {
	Name      string   `json:"name" minLength:"1" maxLength:"80" doc:"群聊名称"`
	MemberIDs []string `json:"member_ids,omitempty" maxItems:"100" uniqueItems:"true" doc:"初始群成员 ID（自动包含创建者本人）"`
}

// CreateGroupChatInput 是新建群聊的 HTTP 请求。
type CreateGroupChatInput struct {
	Body CreateGroupChatParams
}

// CreateGroupChat 在当前工作区中新建一个内部群聊。
type CreateGroupChat struct {
	db   *bun.DB
	down Downstream
}

// NewCreateGroupChat 返回新建群聊的动作。
func NewCreateGroupChat(db *bun.DB, down Downstream) *CreateGroupChat {
	return &CreateGroupChat{db: db, down: down}
}

// Handle 在 sub 所在工作区创建一个新群聊：校验全部成员属于当前工作区、自动加入创建者，并写入 chat.created 系统事件推送给全员。
func (a *CreateGroupChat) Handle(ctx context.Context, sub identity.Subject, p CreateGroupChatParams) (*ChatDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, ErrChatNameRequired.New(errorx.Field("name"))
	}

	var convID string
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		seen := map[string]bool{}
		memberIDs := make([]string, 0, len(p.MemberIDs)+1)
		if sub.Type == member.Type {
			seen[sub.ID] = true
			memberIDs = append(memberIDs, sub.ID)
		}
		roles := a.down.rolesWithDB(tx)
		for i, mid := range p.MemberIDs {
			if mid == "" || seen[mid] {
				continue
			}
			if _, err := member.RoleOf(ctx, roles, identity.Ref{Type: member.Type, ID: mid}, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return member.ErrNotFound.New(errorx.Field(fmt.Sprintf("member_ids[%d]", i)))
				}
				return err
			}
			seen[mid] = true
			memberIDs = append(memberIDs, mid)
		}

		at := database.Now()
		c := &Conversation{
			ID:            database.NewID(),
			TenantID:      sub.Tenant,
			Kind:          Chat,
			HandlingMode:  HandlingHuman,
			Status:        Open,
			Priority:      Normal,
			OpenedAt:      at,
			LastMessageAt: at,
			Attributes:    map[string]any{},
		}
		if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert group chat conversation")
		}
		var createdBy *string
		if sub.Type == member.Type {
			id := sub.ID
			createdBy = &id
		}
		cm := &ChatMeta{
			ConversationID: c.ID,
			TenantID:       sub.Tenant,
			Mode:           ChatGroup,
			Scope:          ChatInternal,
			Name:           name,
			CreatedBy:      createdBy,
		}
		if _, err := tx.NewInsert().Model(cm).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert group chat meta")
		}
		for _, mid := range memberIDs {
			if err := join(ctx, tx, c.ID, MemberActor, mid); err != nil {
				return err
			}
		}
		ev := event(sub, "chat.created", map[string]any{"name": name})
		if err := commit(ctx, tx, a.down, c, ev); err != nil {
			return err
		}
		convID = c.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewGetChat(a.db, a.down.Media.Files).Handle(ctx, sub, convID)
}

// HTTP 处理 POST /t/{slug}/api/chats/group。
func (a *CreateGroupChat) HTTP(ctx context.Context, in *CreateGroupChatInput) (*ChatOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ChatOutput{Body: *dto}, nil
}
