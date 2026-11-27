package emails

import "embed"

// Files 是嵌入的应用邮件模板文件，由 mail.NewTemplates 在启动时解析一次。
//
//go:embed *.html *.txt
var Files embed.FS
