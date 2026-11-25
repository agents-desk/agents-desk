package file

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
)

// Logo 是平台与工作区 Logo 接受的图片规则：PNG、JPEG 或 WebP，不超过 2 MB，按文件内容识别真实类型。
var Logo = filesystem.Accept{
	Types:   []string{"image/png", "image/jpeg", "image/webp"},
	MaxSize: 2 * filesystem.MiB,
}

// ReplaceImage 在事务 tx 中把图片文件从 current 替换为 next（文件 ID 字符串，为空表示清除），返回新文件 ID：
// 确认 next（必须由 owner 上传且符合 accept），并删除原有文件；两者相同时不执行写操作。
// 文件不存在或不符合要求时返回带字段 field 的 filesystem 错误。
func ReplaceImage(ctx context.Context, tx bun.IDB, files *filesystem.Service, owner identity.Subject, field string, accept filesystem.Accept, current *uuid.UUID, next string) (*uuid.UUID, error) {
	var id *uuid.UUID
	if next != "" {
		parsed, err := uuid.Parse(next)
		if err != nil {
			return nil, filesystem.ErrNotFound.New(errorx.Field(field))
		}
		id = &parsed
	}
	if id == current || id != nil && current != nil && *id == *current {
		return current, nil
	}
	fs := files.WithDB(tx)
	if id != nil {
		if _, err := fs.Confirm(ctx, owner, *id, accept); err != nil {
			for _, known := range []*errorx.Template{filesystem.ErrNotFound, filesystem.ErrNotAccepted, filesystem.ErrNotUploaded} {
				if errors.Is(err, known) {
					return nil, known.New(errorx.Field(field))
				}
			}
			return nil, err
		}
	}
	if current != nil {
		if err := fs.Delete(ctx, *current); err != nil {
			return nil, err
		}
	}
	return id, nil
}

// URL 返回文件 id 的公开访问地址；id 为空时返回空字符串。
func URL(ctx context.Context, files *filesystem.Service, id *uuid.UUID) (string, error) {
	if id == nil {
		return "", nil
	}
	f, err := files.Get(ctx, *id)
	if err != nil {
		return "", err
	}
	return f.URL, nil
}
