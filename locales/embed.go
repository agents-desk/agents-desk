// Package locales 嵌入应用的翻译文件：业务错误的提示、资源和字段的显示文字、枚举和权限的名称。
package locales

import "embed"

// FS 是每种语言的翻译文件，例如 zh-CN.yaml。
//
//go:embed *.yaml
var FS embed.FS
