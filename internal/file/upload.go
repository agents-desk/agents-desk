// Package file 是上传文件的接口：文件先保存为上传者的临时文件，业务接口使用它时再确认（filesystem.Service.Confirm），
// 没有被确认的临时文件到期后由定时任务 files:cleanup 回收。
package file

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
)

// UploadInput 是上传的请求：multipart/form-data 中名为 file 的字段。
type UploadInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true"`
	}]
}

// Output 是上传后的文件：id 交给使用它的业务接口，url 用于预览。
type Output struct {
	Body filesystem.File
}

// Upload 把请求中的文件保存为调用者的临时文件。
type Upload struct {
	files *filesystem.Service
}

// NewUpload 返回上传文件的入口。
func NewUpload(files *filesystem.Service) *Upload { return &Upload{files: files} }

// HTTP 处理 POST /files。文件超过 filesystem.max_upload_size 时返回 filesystem.ErrTooLarge。
func (a *Upload) HTTP(ctx context.Context, in *UploadInput) (*Output, error) {
	owner, _ := identity.FromContext(ctx)
	f := in.RawBody.Data().File
	file, err := a.files.Upload(ctx, owner, filesystem.FileInput{
		Filename:    f.Filename,
		ContentType: f.ContentType,
		Size:        f.Size,
	}, f.File)
	if err != nil {
		return nil, err
	}
	return &Output{Body: file}, nil
}
