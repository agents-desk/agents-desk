package file

import "github.com/go-tenon/tenon/filesystem"

// Logo 是平台与工作区 Logo 接受的图片规则：PNG、JPEG 或 WebP，不超过 2 MB，按文件内容识别真实类型。
var Logo = filesystem.Accept{
	Types:   []string{"image/png", "image/jpeg", "image/webp"},
	MaxSize: 2 * filesystem.MiB,
}
