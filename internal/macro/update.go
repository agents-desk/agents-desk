package macro

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// UpdateInput 是修改快捷回复的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateParams
}

// Update 修改一条快捷回复的标题、快捷指令码、正文和附带操作；作用域不可修改。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改快捷回复的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 由 sub 修改快捷回复 id。共享快捷回复要求 macros:manage 权限并记录操作日志；
// 个人快捷回复只允许本人修改。不存在或不可见返回 ErrNotFound，无权管理返回 ErrForbidden。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, p UpdateParams) (*MacroDTO, error) {
	var m *Macro
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		m, err = findVisible(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		if !canManage(sub, m) {
			return ErrForbidden.New()
		}
		title, shortcut, body, actions, err := validateAndNormalize(ctx, tx, sub.Tenant, p.Title, p.Shortcut, p.Body, p.Actions)
		if err != nil {
			return err
		}
		m.Title, m.Shortcut, m.Body, m.Actions = title, shortcut, body, actions
		if _, err := tx.NewUpdate().Model(m).Column("title", "shortcut", "body", "actions", "updated_at").WherePK().Exec(ctx); err != nil {
			if isShortcutConflict(err) {
				return ErrShortcutTaken.New(errorx.Field("shortcut"), errorx.Public("shortcut", shortcut))
			}
			return errors.Wrap(err, "update macro")
		}
		if m.OwnerID == nil {
			return activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "macro.updated",
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

// HTTP 处理 PUT /t/{slug}/api/macros/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*MacroOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &MacroOutput{Body: *dto}, nil
}
