// Package account 是两套账号（平台管理员、工作区成员）共用的个人资料规则：头像接受什么样的图片、换头像时怎样处理文件，
// 以及修改密码。账号自己的表由 platformadmin、member 修改。
package account

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
)

// ErrWrongPassword 是修改密码时当前密码不正确（422），提示显示在“当前密码”下面。
// 登录的 login.ErrInvalidCredentials 是 401，前端收到 401 会当作登录失效，所以这里换成这个错误。
var ErrWrongPassword = errorx.Define(errorx.InvalidArgument, "account.wrong_password")

// Avatar 是头像接受的图片：PNG、JPEG、WebP，不超过 2 MB，按文件内容识别真实类型。
var Avatar = filesystem.Accept{
	Types:   []string{"image/png", "image/jpeg", "image/webp"},
	MaxSize: 2 * filesystem.MiB,
}

// ReplaceAvatar 在事务 tx 中把头像从 current 换成 next（文件 ID 的字符串，为空表示去掉头像），返回新头像的文件 ID：
// 确认 next（只能是 owner 自己上传的、符合 Avatar 的图片），删除原来的文件。两者相同时什么也不做。
// next 不能使用时返回带字段 avatar_file_id 的 filesystem.ErrNotFound 或 filesystem.ErrNotAccepted。
func ReplaceAvatar(ctx context.Context, tx bun.IDB, files *filesystem.Service, owner identity.Subject, current *uuid.UUID, next *string) (*uuid.UUID, error) {
	var id *uuid.UUID
	if next != nil && *next != "" {
		parsed, err := uuid.Parse(*next)
		if err != nil {
			return nil, filesystem.ErrNotFound.New(errorx.Field("avatar_file_id"))
		}
		id = &parsed
	}
	if id == current || id != nil && current != nil && *id == *current {
		return current, nil
	}
	fs := files.WithDB(tx)
	if id != nil {
		if _, err := fs.Confirm(ctx, owner, *id, Avatar); err != nil {
			for _, known := range []*errorx.Template{filesystem.ErrNotFound, filesystem.ErrNotAccepted, filesystem.ErrNotUploaded} {
				if errors.Is(err, known) {
					return nil, known.New(errorx.Field("avatar_file_id"))
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

// AvatarURL 返回文件 id 的公开地址，id 为空时返回空字符串。
func AvatarURL(ctx context.Context, files *filesystem.Service, id *uuid.UUID) (string, error) {
	if id == nil {
		return "", nil
	}
	f, err := files.Get(ctx, *id)
	if err != nil {
		return "", err
	}
	return f.URL, nil
}

// ProfileChanges 是修改个人资料的请求：名字和头像。
type ProfileChanges struct {
	Name         string  `json:"name" minLength:"1" maxLength:"60"`
	AvatarFileID *string `json:"avatar_file_id,omitempty" format:"uuid" doc:"头像的文件 ID：刚上传的图片或当前的头像；不传表示去掉头像"`
}

// ProfileInput 是修改个人资料的 HTTP 请求。
type ProfileInput struct {
	Body ProfileChanges
}
