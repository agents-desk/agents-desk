package macro

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
)

// Get 读取当前工作区中调用者可见的单条快捷回复。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取单条快捷回复的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回 sub 可见的快捷回复 id；不存在或不可见时返回 ErrNotFound。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*MacroDTO, error) {
	m, err := findVisible(ctx, a.db, sub, id, false)
	if err != nil {
		return nil, err
	}
	dto := m.DTO(sub)
	return &dto, nil
}

// HTTP 处理 GET /t/{slug}/api/macros/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*MacroOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &MacroOutput{Body: *dto}, nil
}
