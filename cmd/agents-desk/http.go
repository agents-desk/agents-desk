package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/command/httpcmd"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/doctor/doctorapi"
	"github.com/go-tenon/tenon/frontend"
	"github.com/go-tenon/tenon/health"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/natsx"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queueapi"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/realtime"
	"github.com/go-tenon/tenon/savedview"
	"github.com/go-tenon/tenon/schedule/scheduleapi"

	"agents-desk/apps"
	"agents-desk/internal/adminnotify"
	"agents-desk/internal/attribute"
	"agents-desk/internal/audit"
	"agents-desk/internal/auth"
	"agents-desk/internal/automation"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/file"
	"agents-desk/internal/flags"
	"agents-desk/internal/hours"
	"agents-desk/internal/llm"
	"agents-desk/internal/macro"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/platformsettings"
	"agents-desk/internal/push"
	"agents-desk/internal/report"
	"agents-desk/internal/role"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
	"agents-desk/internal/telegram"
	"agents-desk/internal/tenant"
	"agents-desk/internal/translation"
	"agents-desk/internal/widget"
	"agents-desk/locales"
)

// serveHTTP 装配 HTTP 服务，返回运行它的函数。apps 中的前端由 frontend.Host 按挂载路径提供：只在浏览器中渲染的前端
// 由 Go 直接返回页面；服务端渲染的前端由 Go 托管的 SSR 进程渲染，它们渲染时经内部 socket 调用同一组路由。
// /t/{slug}/realtime 是工作区端的实时连接（WebSocket），成员在其中订阅自己的个人频道；/w/{channel}/realtime 是聊天窗口的
// 实时连接，客户在其中订阅自己的频道，收到成员的回复。
func (a *App) serveHTTP(d *command.Daemon) (func(context.Context) error, error) {
	pages, err := frontend.New(apps.Dist, frontend.Options{DevServers: a.cfg.DevServers, CSP: a.pageCSP})
	if err != nil {
		return nil, err
	}
	rt, err := a.newRealtime()
	if err != nil {
		return nil, err
	}
	widgetRT, err := a.newWidgetRealtime()
	if err != nil {
		return nil, err
	}
	router := a.routes(pages.Handler())
	router.With(tenant.Middleware(a.db)).Handle("/t/{slug}/realtime", rt.Handler())
	router.With(widget.Middleware(a.db)).Handle("/w/{channel}/realtime", widgetRT.Handler())
	pages.SetAPI(router)
	ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
	if err != nil {
		return nil, err
	}
	serveOpts := httpcmd.ServeOptions(d)
	return func(ctx context.Context) error {
		err := pages.Run(ctx, func(ctx context.Context) error {
			return httpx.Serve(ctx, a.cfg.HTTP, ln, router, serveOpts...)
		})
		// 先断开实时连接，广播随后在退出时关闭
		return errors.Join(err, rt.Shutdown(context.WithoutCancel(ctx)), widgetRT.Shutdown(context.WithoutCancel(ctx)))
	}, nil
}

// pageCSP 是页面的内容安全策略：从框架默认策略出发，放行本地预览（blob:）及配置的 imgproxy / 文件存储域名作为图片来源；
// 聊天窗口的页面（/widget/?channel=<id>）按这个聊天插件允许嵌入的网站生成 frame-ancestors，其他页面都不能被嵌入。
func (a *App) pageCSP(r *http.Request, nonce string) string {
	p := frontend.DefaultPolicy(nonce)
	p.AddImgSrc(
		"blob:",
		frontend.Origin(a.cfg.Imgproxy.URL),
		frontend.Origin(a.cfg.Filesystem.Disks.S3.PublicURL),
		frontend.Origin(a.cfg.Filesystem.Disks.Local.PublicURL),
	)
	if r.URL.Path != "/widget" && !strings.HasPrefix(r.URL.Path, "/widget/") {
		return p.String()
	}
	// 预览路由（/widget/preview）仅允许同源工作区设置工作台以 iframe 嵌入。
	if r.URL.Path == "/widget/preview" || r.URL.Path == "/widget/preview/" {
		p.FrameAncestors = []string{"'self'"}
		return p.String()
	}
	ancestors, err := widget.FrameAncestors(r.Context(), a.db, r.URL.Query().Get("channel"))
	if err != nil {
		slog.ErrorContext(r.Context(), "load widget frame ancestors", "error", err)
		return p.String()
	}
	p.FrameAncestors = strings.Fields(ancestors)
	return p.String()
}

// bodyLimitFor 为文件上传接口（POST .../files）放宽请求体上限至 MaxUploadSize + 512 KiB，其余接口沿用默认 1 MiB 上限。
func (a *App) bodyLimitFor(r *http.Request) int64 {
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/files") {
		return int64(a.cfg.Filesystem.MaxUploadSize) + 512<<10
	}
	return 0
}

// memberImpersonation 返回工作区端允许平台管理员代为登录的策略：操作者必须未停用且在平台域拥有 tenants:impersonate 权限。
func (a *App) memberImpersonation() *identity.Impersonation {
	return &identity.Impersonation{
		Actors:      identity.Accounts{platformadmin.Type: platformadmin.Resolve(a.db, a.roles)},
		Permissions: []string{string(auth.TenantsImpersonate)},
	}
}

// newRealtime 创建工作区端的实时连接，挂在 /t/{slug}/realtime（前面是 tenant.Middleware）。它与工作区的接口使用
// 同一套成员 token、会话 cookie、租户、代为登录策略和权限；成员在其中订阅自己的个人频道（push.Register），以及能看到的会话的频道
// （conversation.RegisterRealtime：谁正在看、谁正在输入）。
func (a *App) newRealtime() (*realtime.Realtime, error) {
	rt, err := realtime.New(a.hub, realtime.Options{
		Validate:      a.memberTokens.Validate,
		Accounts:      identity.Accounts{member.Type: tenant.Resolve(member.Resolve(a.db, a.roles))},
		Impersonation: a.memberImpersonation(),
		SessionCookie: member.SessionCookie,
		Credentials:   a.creds,
		Tenant:        func(r *http.Request) string { return tenant.IDFrom(r.Context()) },
		CheckTenant:   tenant.CheckEnabled(a.db),
		Presence:      a.presence,
	})
	if err != nil {
		return nil, err
	}
	push.Register(rt)
	conversation.RegisterRealtime(rt, a.db)
	return rt, nil
}

// newWidgetRealtime 创建聊天窗口的实时连接，挂在 /w/{channel}/realtime（前面是 widget.Middleware）。它只接受联系人的令牌：
// 聊天窗口在第三方网站的页面中，令牌由 SDK 传入，不用 cookie；客户只能订阅自己在这个聊天插件中的频道（widget.RegisterRealtime）。
func (a *App) newWidgetRealtime() (*realtime.Realtime, error) {
	rt, err := realtime.New(a.hub, realtime.Options{
		Validate:    a.contactTokens.Validate,
		Accounts:    identity.Accounts{contact.Type: widget.Resolve(a.db)},
		Credentials: a.creds,
		Tenant:      func(r *http.Request) string { return widget.TenantFrom(r.Context()) },
		CheckTenant: tenant.CheckEnabled(a.db),
	})
	if err != nil {
		return nil, err
	}
	widget.RegisterRealtime(rt, a.db)
	return rt, nil
}

// routes 是应用的全部路由：存活和就绪检查，/admin/api 下管理员端的接口，/api 下成员账号的接口，/t/{slug}/api 下
// 工作区的接口，/w/{channel}/api 下聊天窗口的接口，其余路径是页面（pages）。
// 它只保存 App 的字段、处理请求时才使用，所以 docRouter 可以用没有打开资源的 App 调用它。
func (a *App) routes(pages http.Handler) chi.Router {
	router := chi.NewRouter()
	router.Use(httpx.Defaults(a.cfg.HTTP,
		httpx.Languages(i18n.MustNew("zh-CN", locales.FS)),
		httpx.BodyLimitFor(a.bodyLimitFor),
		httpx.WithContext(database.TrackPrimary),
	)...)
	router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
		Checks: []health.Check{
			{Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
			{Name: "nats", Ping: func(ctx context.Context) error { return natsx.Ping(a.nc)(ctx) }},
		},
	}))

	addr := tenant.Addresses{PublicURL: a.cfg.PublicURL}
	// 成员离开工作区之后（移出成员、更换主管理员），在同一个事务中：他负责的未关闭会话放回团队收件箱重新分配，
	// 团队收件箱把他移出，个人快捷回复和个人视图一并清理
	removeViews := func(ctx context.Context, tx database.Tx, tenantID, memberID string) error {
		return savedview.DeleteByOwner(ctx, tx, tenantID, identity.Ref{Type: member.Type, ID: memberID})
	}
	memberHooks := member.Hooks{Removed: []member.RemovedHook{
		conversation.OnMemberLeft(a.downstream()), team.OnMemberRemoved, macro.OnMemberRemoved, removeViews,
	}}

	// 管理员端：只接受平台管理员，会话 cookie 是 __Host-admin，权限按平台的域加载；未配置 Impersonation，默认拒绝所有代为登录凭据。
	router.Route("/admin/api", func(r chi.Router) {
		r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
		api := rest.New(r, rest.Options{
			Title: "agents-desk 管理员端", Version: "0.1.0", BasePath: "/admin/api",
			Validate:      a.adminTokens.Validate,
			Accounts:      identity.Accounts{platformadmin.Type: platformadmin.Resolve(a.db, a.roles)},
			SessionCookie: platformadmin.SessionCookie,
			Credentials:   a.creds,
			Permissions:   auth.Catalog,
		})
		require := func(perms ...auth.Permission) func(*huma.Operation) {
			names := make([]string, len(perms))
			for i, p := range perms {
				names[i] = string(p)
			}
			return api.RequireToken(names...)
		}
		platformadmin.RegisterRoutes(api, require(), a.db, a.adminLogins, a.adminTokens, a.files, a.cfg.Filesystem.MaxUploadSize, a.cfg.AdminToken.Lifetime)
		platformadmin.RegisterManageRoutes(api, require(auth.PlatformAdminsManage), a.db, a.roles, a.adminLogins, a.creds)
		adminnotify.RegisterRoutes(api, require(), a.db, a.hub)
		tenant.RegisterRoutes(api, require(auth.TenantsRead), require(auth.TenantsManage), require(auth.TenantsImpersonate), a.db, a.roles, a.memberLogins, a.memberTokens, addr, memberHooks, a.creds)
		tenant.RegisterExportRoutes(api, require(auth.TenantsRead), a.db, a.memberLogins, addr, a.exporter, a.hub)
		platformsettings.RegisterRoutes(api, require(auth.PlatformSettingsManage), a.db, a.generalSettings, a.mailSettings)
		llm.RegisterRoutes(api, require(auth.PlatformSettingsManage), a.db, a.llmSettings)
		flags.RegisterRoutes(api, require(auth.FeaturesManage), a.db, a.features)
		audit.RegisterRoutes(api, require(auth.AuditRead), a.db)
		queueOpts := queueapi.Options{
			Topics: []queue.TopicMeta{
				conversation.DeliverTopic.Meta("queue.topic.conversation.deliver.label", "queue.topic.conversation.deliver.description"),
				translation.Topic.Meta("queue.topic.conversation.translate.label", "queue.topic.conversation.translate.description"),
				translation.FillTopic.Meta("queue.topic.translation.fill.label", "queue.topic.translation.fill.description"),
				automation.Topic.Meta("queue.topic.conversation.automate.label", "queue.topic.conversation.automate.description"),
				conversation.SearchIndex.SyncTopic().Meta("queue.topic.search.sync.conversation_items.label", "queue.topic.search.sync.conversation_items.description"),
				conversation.SearchIndex.ReindexTopic().Meta("queue.topic.search.reindex.conversation_items.label", "queue.topic.search.reindex.conversation_items.description"),
				tenant.ExportJob.Meta("queue.topic.tenants.export.label", "queue.topic.tenants.export.description"),
				contact.ExportJob.Meta("queue.topic.contacts.export.label", "queue.topic.contacts.export.description"),
			},
		}
		if a.nc != nil { // docRouter 离线构造路由时没有 NATS 连接
			queueOpts.Admin = queue.NewAdmin(a.nc, a.cfg.Queue)
		}
		queueapi.Register(api, require(auth.QueueManage), a.db, queueOpts)
		scheduleapi.Register(api, require(auth.SchedulesManage), a.db, a.tasks)
		doctorapi.Register(api, require(auth.SystemRead), a.doctor)
	})

	// 工作区端的账号级接口：成员的登录、退出、当前账号和所属的工作区。只接受成员，会话 cookie 是 __Host-session（代登时优先读 __Host-impersonation），
	// AccountScope 放行锁定在单个工作区的代登凭据并保留 Subject.BoundTenant；修改个人资料与密码等敏感路由标有 rest.DirectOnly。
	router.Route("/api", func(r chi.Router) {
		r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
		api := rest.New(r, rest.Options{
			Title: "agents-desk", Version: "0.1.0", BasePath: "/api",
			Validate:      a.memberTokens.Validate,
			Accounts:      identity.Accounts{member.Type: member.Resolve(a.db, a.roles)},
			Impersonation: a.memberImpersonation(),
			AccountScope:  true,
			SessionCookie: member.SessionCookie,
			Credentials:   a.creds,
		})
		requireMember := api.RequireToken()
		member.RegisterRoutes(api, requireMember, a.db, a.memberLogins, a.memberTokens, a.files, a.cfg.Filesystem.MaxUploadSize, a.cfg.MemberToken.Lifetime)
		tenant.RegisterMemberRoutes(api, requireMember, a.db, a.roles, addr)
	})

	// 工作区的接口：tenant.Middleware 按路径中的 slug 查出工作区，业务代码只见到它的 ID（sub.Tenant）。
	// 不是成员（包括 slug 不存在）时 404，工作区停用时 403；权限按这个工作区的域加载。
	router.Route("/t/{slug}/api", func(r chi.Router) {
		r.Use(httpx.Timeout(a.cfg.HTTP.Timeout), tenant.Middleware(a.db))
		api := rest.New(r, rest.Options{
			Title: "agents-desk 工作区", Version: "0.1.0", BasePath: "/t/{slug}/api",
			Validate:      a.memberTokens.Validate,
			Tenant:        tenant.FromRequest,
			Accounts:      identity.Accounts{member.Type: tenant.Resolve(member.Resolve(a.db, a.roles))},
			Impersonation: a.memberImpersonation(),
			SessionCookie: member.SessionCookie,
			Credentials:   a.creds,
			Permissions:   auth.Catalog,
		})
		require := func(perms ...auth.Permission) func(*huma.Operation) {
			names := make([]string, len(perms))
			for i, p := range perms {
				names[i] = string(p)
			}
			return api.RequireToken(names...)
		}
		tenant.RegisterTenantRoutes(api, require, a.db)
		member.RegisterTenantRoutes(api, require, a.db, a.roles, a.memberLogins, a.files, memberHooks)
		role.RegisterRoutes(api, require, a.db, a.roles)
		hours.RegisterRoutes(api, require, a.db)
		team.RegisterRoutes(api, require, a.db, a.roles, a.memberLogins, a.files)
		channel.RegisterRoutes(api, require, a.db)
		contact.RegisterRoutes(api, require, a.db, a.hub, a.roles, a.exporter)
		tag.RegisterRoutes(api, require, a.db)
		attribute.RegisterRoutes(api, require, a.db)
		macro.RegisterRoutes(api, require, a.db, a.roles)
		conversation.RegisterRoutes(api, require, a.db, a.downstream())
		file.RegisterRoutes(api, require(auth.ConversationsReply), a.files, a.cfg.Filesystem.MaxUploadSize)
		widget.RegisterRoutes(api, require, a.db, a.crypt, a.cfg.PublicURL)
		telegram.RegisterRoutes(api, require, a.db, a.crypt, a.telegram, a.cfg.PublicURL)
		translation.RegisterRoutes(api, require, a.db, a.translator, translation.NewIncoming(a.db, a.locker, a.translator, a.downstream()), a.translationSettings)
		automation.RegisterRoutes(api, require, a.db)
		report.RegisterRoutes(api, require, a.db, a.roles, a.files)
	})

	// 聊天窗口的接口：widget.Middleware 按路径中的渠道 ID 查出聊天插件和它所在的工作区。只接受联系人的令牌，
	// 客户的接口不需要登录，所以整组按客户端地址限流。
	router.Route("/w/{channel}/api", func(r chi.Router) {
		r.Use(httpx.Timeout(a.cfg.HTTP.Timeout), httpx.RateLimit(ratelimit.NewMemory(a.cfg.HTTP.RateLimit), httpx.KeyByIP), widget.Middleware(a.db))
		api := rest.New(r, rest.Options{
			Title: "agents-desk 聊天插件", Version: "0.1.0", BasePath: "/w/{channel}/api",
			Validate:    a.contactTokens.Validate,
			Tenant:      widget.TenantFromRequest,
			Accounts:    identity.Accounts{contact.Type: widget.Resolve(a.db)},
			Credentials: a.creds,
		})
		down := a.downstream()
		widget.RegisterVisitorRoutes(api, api.RequireToken(), a.db, a.crypt, a.contactTokens,
			conversation.NewReceive(a.db, a.channels, down), down, a.cfg.Filesystem.MaxUploadSize)
	})

	// Telegram 机器人 Webhook：接收 Telegram 官方直连或外部业务网关转发的 Update，使用渠道自己的 secret_token 鉴权。
	router.With(httpx.Timeout(a.cfg.HTTP.Timeout)).Post("/webhooks/telegram/{channel}",
		telegram.NewWebhook(a.db, a.crypt, a.files, a.telegram, conversation.NewReceive(a.db, a.channels, a.downstream()), a.downstream()).ServeHTTP)

	// 本地磁盘中上传的文件（头像等），地址是 filesystem.disks.local.public_url。
	if a.localDisk != nil {
		router.Handle("/storage/*", a.localDisk)
	}

	if pages != nil {
		router.Handle("/*", pages)
	}
	return router
}

// docRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func docRouter() chi.Routes {
	return (&App{cfg: config.Default[Config]()}).routes(nil)
}
