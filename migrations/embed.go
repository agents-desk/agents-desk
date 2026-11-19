// Package migrations 嵌入应用的 SQL 迁移文件，包括用 migrate publish 发布的框架迁移。
package migrations

import "embed"

// FS 是全部迁移文件。模式写成 *：还没有迁移时也能编译；goose 只读取其中的 .sql 文件。
//
//go:embed *
var FS embed.FS
