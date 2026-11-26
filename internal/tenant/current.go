package tenant

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/file"
)

// CurrentDTO 是成员所在的工作区，以及他在其中的权限：工作区端据此显示名称、Logo、常规设置、菜单和按钮。
type CurrentDTO struct {
	ID                string   `json:"id"`
	Slug              string   `json:"slug"`
	Name              string   `json:"name"`
	LogoFileID        string   `json:"logo_file_id,omitempty" doc:"工作区 Logo 文件 ID，未设置时为空"`
	LogoURL           string   `json:"logo_url" doc:"工作区 Logo 公开地址，未设置时为空"`
	ReopenWindowHours int      `json:"reopen_window_hours" doc:"会话关闭后的重开窗口（小时），0 表示关闭后不再重开"`
	Permissions       []string `json:"permissions" doc:"当前成员在这个工作区中的权限"`
}

// CurrentOutput 是当前工作区的响应。
type CurrentOutput struct {
	Body CurrentDTO
}

// Current 读取本次请求所在的工作区。
type Current struct {
	db    *bun.DB
	files *filesystem.Service
}

// NewCurrent 返回读取当前工作区的动作。
func NewCurrent(db *bun.DB, files *filesystem.Service) *Current {
	return &Current{db: db, files: files}
}

// Handle 返回 sub 所在的工作区（sub.Tenant）和 sub 在其中的权限。
func (a *Current) Handle(ctx context.Context, sub identity.Subject) (*CurrentDTO, error) {
	t, err := find(ctx, a.db, sub.Tenant, false)
	if err != nil {
		return nil, err
	}
	logoURL, err := file.URL(ctx, a.files, t.LogoFileID)
	if err != nil {
		return nil, err
	}
	var logoFileID string
	if t.LogoFileID != nil {
		logoFileID = t.LogoFileID.String()
	}
	return &CurrentDTO{
		ID: t.ID, Slug: t.Slug, Name: t.Name, LogoFileID: logoFileID, LogoURL: logoURL,
		ReopenWindowHours: t.ReopenWindowHours, Permissions: sub.Permissions,
	}, nil
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
