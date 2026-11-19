package tenant

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// CurrentDTO 是成员所在的工作区，以及他在其中的权限：工作区端据此显示名称、菜单和按钮。
type CurrentDTO struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions" doc:"当前成员在这个工作区中的权限"`
}

// CurrentOutput 是当前工作区的响应。
type CurrentOutput struct {
	Body CurrentDTO
}

// Current 读取本次请求所在的工作区。
type Current struct {
	db *bun.DB
}

// NewCurrent 返回读取当前工作区的动作。
func NewCurrent(db *bun.DB) *Current { return &Current{db: db} }

// Handle 返回 sub 所在的工作区（sub.Tenant）和 sub 在其中的权限。
func (a *Current) Handle(ctx context.Context, sub identity.Subject) (*CurrentDTO, error) {
	t, err := find(ctx, a.db, sub.Tenant, false)
	if err != nil {
		return nil, err
	}
	return &CurrentDTO{ID: t.ID, Slug: t.Slug, Name: t.Name, Permissions: sub.Permissions}, nil
}

// HTTP 处理 GET /t/{slug}/api/tenant。
func (a *Current) HTTP(ctx context.Context, _ *struct{}) (*CurrentOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &CurrentOutput{Body: *dto}, nil
}
