// Package file 是上传文件的接口：文件先保存为上传者的临时文件，业务接口使用它时再确认（filesystem.Service.Confirm），
// 没有被确认的临时文件到期后由定时任务 files:cleanup 回收。
package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

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

// UploadOptions 配置单组上传入口的目标磁盘与即时校验规则。
type UploadOptions struct {
	Private bool               // 为 true 时写入 files.PrivateDisk()，不暴露匿名公开 URL
	Accept  *filesystem.Accept // 非 nil 时在上传落盘前即时校验 MIME 与大小
}

// Upload 把请求中的文件保存为调用者的临时文件。
type Upload struct {
	files *filesystem.Service
	opts  UploadOptions
}

// NewUpload 返回上传文件的入口。
func NewUpload(files *filesystem.Service, opts ...UploadOptions) *Upload {
	var o UploadOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return &Upload{files: files, opts: o}
}

// HTTP 处理 POST /files：拦截可直接在浏览器执行的 HTML/SVG/XML/JS 活跃内容，超限时返回 filesystem.ErrTooLarge。
func (a *Upload) HTTP(ctx context.Context, in *UploadInput) (*Output, error) {
	owner, _ := identity.FromContext(ctx)
	f := in.RawBody.Data().File

	var head [512]byte
	n, err := io.ReadFull(f.File, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if isBlockedActiveUpload(f.Filename, f.ContentType, head[:n]) {
		return nil, filesystem.ErrNotAccepted.New()
	}

	disk := ""
	if a.opts.Private {
		disk = a.files.PrivateDisk()
	}
	reader := io.MultiReader(bytes.NewReader(head[:n]), f.File)
	file, err := a.files.Upload(ctx, owner, filesystem.FileInput{
		Disk:        disk,
		Filename:    f.Filename,
		ContentType: f.ContentType,
		Size:        f.Size,
		Accept:      a.opts.Accept,
	}, reader)
	if err != nil {
		return nil, err
	}
	return &Output{Body: file}, nil
}

// isBlockedActiveUpload 在上传入口即拒绝浏览器可直接执行脚本的 HTML、SVG、XML 与 JavaScript 内容。
func isBlockedActiveUpload(filename, declaredMIME string, sniffBuf []byte) bool {
	if filesystem.IsActiveType(declaredMIME) || filesystem.IsActiveType(http.DetectContentType(sniffBuf)) {
		return true
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(filename))) {
	case ".html", ".htm", ".xhtml", ".svg", ".svgz", ".xml", ".js", ".mjs":
		return true
	default:
		return false
	}
}
