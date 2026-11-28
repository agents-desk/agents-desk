package macro

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/member"
)

// CreateInput 是新建快捷回复的 HTTP 请求。
type CreateInput struct {
	Body CreateParams
}

// Create 在当前工作区中新建一条共享或个人快捷回复。
type Create struct {
	db    *bun.DB
	roles *authz.Store
}

// NewCreate 返回新建快捷回复的动作。
func NewCreate(db *bun.DB, roles *authz.Store) *Create { return &Create{db: db, roles: roles} }

// Handle 由 sub 按 p 创建快捷回复。共享快捷回复要求 macros:manage 权限并记录工作区操作日志；
// 个人快捷回复归属 sub 本人。同作用域下快捷指令码重复时返回 ErrShortcutTaken。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, p CreateParams) (*MacroDTO, error) {
	var ownerID *string
	switch p.Scope {
	case ScopeShared:
		if !canManageShared(sub) {
			return nil, ErrForbidden.New()
		}
	case ScopePersonal:
		if sub.Type != member.Type {
			return nil, ErrForbidden.New()
		}
		id := sub.ID
		ownerID = &id
	default:
		return nil, ErrInvalidActions.New(errorx.Field("scope"))
	}

	var createdBy *string
	if sub.Type == member.Type {
		id := sub.ID
		createdBy = &id
	}

	var m *Macro
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		title, shortcut, body, actions, err := validateAndNormalize(ctx, tx, a.roles, sub.Tenant, p.Title, p.Shortcut, p.Body, p.Actions)
		if err != nil {
			return err
		}
		m = &Macro{
			ID:        database.NewID(),
			TenantID:  sub.Tenant,
			OwnerID:   ownerID,
			Shortcut:  shortcut,
			Title:     title,
			Body:      body,
			Actions:   actions,
			CreatedBy: createdBy,
		}
		if _, err := tx.NewInsert().Model(m).Exec(ctx); err != nil {
			if isShortcutConflict(err) {
				return ErrShortcutTaken.New(errorx.Field("shortcut"), errorx.Public("shortcut", shortcut))
			}
			return errors.Wrap(err, "insert macro")
		}
		if m.OwnerID == nil {
			return activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "macro.created",
				TargetType: "macro",
				TargetID:   m.ID,
				Properties: map[string]any{"title": m.Title, "shortcut": m.Shortcut},
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dto := m.DTO(sub)
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/macros。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*MacroOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &MacroOutput{Body: *dto}, nil
}
