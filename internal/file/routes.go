package file

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
)

// RegisterRoutes 注册上传文件的接口。require 要求这个 API 的登录凭据：管理员端、成员和聊天插件各在自己的 API 中注册一次。
// maxSize 是 filesystem.max_upload_size：请求体的上限在它之上留出表单其余部分的余量（Huma 默认只接受 1 MB）。
func RegisterRoutes(api *rest.API, require func(*huma.Operation), files *filesystem.Service, maxSize filesystem.ByteSize, opts ...UploadOptions) {
	op := huma.Operation{OperationID: "file-upload", Method: http.MethodPost, Path: "/files", Summary: "上传文件", DefaultStatus: http.StatusCreated}
	op.MaxBodyBytes = int64(maxSize) + 64<<10
	require(&op)
	rest.Register(api, op, NewUpload(files, opts...).HTTP)
}
