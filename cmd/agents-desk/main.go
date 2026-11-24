// Command agents-desk 是应用的入口：serve 运行 HTTP 服务和队列 worker，migrate 管理数据库迁移，admin 管理平台管理员，
// health 供 Docker 的健康检查使用，openapi、routes、client 供 go tool tenon 生成文档和客户端。
package main

import (
	"io/fs"
	"net/url"
	// 程序内嵌时区数据：校验成员选的时区时，不依赖服务器（例如容器镜像）中有没有系统的时区文件。
	_ "time/tzdata"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/httpcmd"
	"github.com/go-tenon/tenon/command/migratecmd"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/doctor/doctorcmd"
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/hashing"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/imgproxy"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/natsx"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuecmd"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
	"github.com/go-tenon/tenon/savedview"
	"github.com/go-tenon/tenon/schedule"
	"github.com/go-tenon/tenon/schedule/pgschedule"
	"github.com/go-tenon/tenon/schedule/scheduleapi"
	"github.com/go-tenon/tenon/schedule/schedulecmd"
	"github.com/go-tenon/tenon/search"
	"github.com/go-tenon/tenon/search/searchcmd"
	"github.com/go-tenon/tenon/settings"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/conversation"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
	"agents-desk/internal/widget"
	"agents-desk/locales"
	"agents-desk/migrations"
)

// Config 是应用的配置：config/app.yaml。
type Config struct {
	HTTP         httpx.Config      `yaml:"http"`
	PublicURL    string            `yaml:"public_url"` // 应用对外的地址，例如 https://desk.example.com，生成工作区的地址等链接时使用
	Database     database.Config   `yaml:"database"`
	NATS         natsx.Config      `yaml:"nats"`
	Queue        queue.Config      `yaml:"queue"`
	Crypt        crypt.Config      `yaml:"crypt"`
	Hashing      hashing.Config    `yaml:"hashing"`
	AdminToken   apitoken.Config   `yaml:"admin_token"`   // 平台管理员登录 token 的有效期
	MemberToken  apitoken.Config   `yaml:"member_token"`  // 工作区成员登录 token 的有效期
	ContactToken apitoken.Config   `yaml:"contact_token"` // 客户在聊天插件中的令牌的有效期
	Filesystem   filesystem.Config `yaml:"filesystem"`    // 上传的文件（头像、附件等）：本地目录或 S3
	Imgproxy     imgproxy.Config   `yaml:"imgproxy"`      // 图片缩略图服务，未配置 url 时返回原图地址
	Schedule     schedule.Config   `yaml:"schedule"`      // 定时任务
	DevServers   map[string]string `yaml:"dev_servers"`   // 各前端开发服务器的地址，按前端的名称（apps 中的目录名）；只在 tenon dev 时有值，它把地址写入 .tenon/dev.yaml，页面请求转给对应的开发服务器
}

// SetDefaults 给出开发时的对外地址。
func (c *Config) SetDefaults() {
	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:8081"
	}
}

// Validate 检查对外地址是 http 或 https 的绝对地址，以及全局的请求体上限能容纳最大的上传文件。
func (c Config) Validate() error {
	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.Newf("public_url: %q 不是 http:// 或 https:// 开头的地址", c.PublicURL)
	}
	if c.HTTP.BodyLimit.MaxBytes < int64(c.Filesystem.MaxUploadSize) {
		return errors.Newf("http.body_limit.max_bytes (%d) 小于 filesystem.max_upload_size (%s)，上传会被拒绝", c.HTTP.BodyLimit.MaxBytes, c.Filesystem.MaxUploadSize)
	}
	return nil
}

func main() {
	root := command.Root("agents-desk", "agents-desk")
	root.AddCommand(
		command.Serve(open, parts),
		command.Config[Config](),
		httpcmd.Health(func(cfg Config) httpx.Config { return cfg.HTTP }),
		migrateCommand(),
	)
	// 需要数据库等资源的命令：声明时 App 是零值，执行其中一个命令时才调用 open 装配。
	root.AddCommand(command.Bind(open, func(a *App) []*cobra.Command {
		return []*cobra.Command{
			doctorcmd.New(a.doctor()...),
			queuecmd.New(a.db, queuecmd.WithJetStream(a.nc, a.cfg.Queue)),
			schedulecmd.New(a.tasks),
			searchcmd.New(a.db, conversation.SearchIndex),
			platformadmin.Commands(a.db, a.adminLogins),
			seedCommand(a.db, a.adminLogins, a.memberLogins, a.files, a.crypt, tenant.Addresses{PublicURL: a.cfg.PublicURL}, a.channels, a.downstream()),
			devCommands(a),
		}
	})...)
	// openapi、routes、client 用默认配置和没有打开资源的 App 构造同一套路由，从中读取路由表和接口定义；
	// 翻译文件交给 client 命令，枚举、资源和权限的显示文字来自其中。
	root.AddCommand(httpcmd.Commands(docRouter, httpcmd.Translations(locales.FS))...)
	// 命令失败时，业务错误按应用的翻译文件显示提示和错误码。
	command.Execute(root, command.Languages(i18n.MustNew("zh-CN", locales.FS)))
}

// devCommands 返回开发用的命令组 dev：以客户的身份发消息（dev message），签名聊天插件的用户身份（dev user-token）。
func devCommands(a *App) *cobra.Command {
	dev := conversation.Commands(a.db, a.channels, a.downstream())
	dev.AddCommand(widget.UserTokenCommand(a.db, a.crypt))
	return dev
}

// migrateCommand 返回 migrate 命令：up、status、down、make，发布框架自带的迁移的 publish，
// 以及检查框架的迁移是否都已发布、与框架一致的 verify。
func migrateCommand() *cobra.Command {
	cmd := migratecmd.New(migrations.FS, func(cfg Config) database.Config { return cfg.Database })
	sources := []fs.FS{
		login.Migrations(),
		pglimit.Migrations(),
		apitoken.Migrations(),
		authz.Migrations(),
		settings.Migrations(),
		savedview.Migrations(),
		feature.Migrations(),
		activity.Migrations(),
		notification.Migrations(),
		operation.Migrations(),
		exports.Migrations(),
		queue.Migrations(),
		pgschedule.Migrations(),
		scheduleapi.Migrations(),
		search.Migrations(),
		filesystem.Migrations(),
		pglock.Migrations(),
	}
	cmd.AddCommand(migratecmd.Publish(sources...), migratecmd.Verify(sources...))
	return cmd
}
