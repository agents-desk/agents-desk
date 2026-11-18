package main

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/frontend"
	"github.com/go-tenon/tenon/health"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/natsx"

	"agents-desk/apps"
	"agents-desk/internal/auth"
	"agents-desk/internal/llm"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
	"agents-desk/locales"
)

// serveHTTP 装配 HTTP 服务，返回运行它的函数。apps 中的前端由 frontend.Host 按挂载路径提供：只在浏览器中渲染的前端
// 由 Go 直接返回页面；服务端渲染的前端由 Go 托管的 SSR 进程渲染，它们渲染时经内部 socket 调用同一组路由。
func (a *App) serveHTTP(d *command.Daemon) (func(context.Context) error, error) {
	var router chi.Router
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) })
	pages, err := frontend.New(apps.Dist, frontend.Options{API: api, DevServers: a.cfg.DevServers})
	if err != nil {
		return nil, err
	}
	router = a.routes(pages.Handler())
	ln, err := d.Listen("tcp", a.cfg.HTTP.Addr)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		return pages.Run(ctx, func(ctx context.Context) error {
			return httpx.Serve(ctx, a.cfg.HTTP, ln, router)
		})
	}, nil
}

// routes 是应用的全部路由：存活和就绪检查，/admin/api 下管理员端的接口，/api 下工作区端的接口，其余路径是页面（pages）。
// 它只保存 App 的字段、处理请求时才使用，所以 docRouter 可以用没有打开资源的 App 调用它。
func (a *App) routes(pages http.Handler) chi.Router {
	router := chi.NewRouter()
	router.Use(httpx.Defaults(a.cfg.HTTP, httpx.Languages(i18n.MustNew("zh-CN", locales.FS)))...)
	router.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router.Handle("/readyz", httpx.Ready(httpx.ReadyOptions{
		Checks: []health.Check{
			{Name: "database", Ping: func(ctx context.Context) error { return a.db.PingContext(ctx) }},
			{Name: "nats", Ping: func(ctx context.Context) error { return natsx.Ping(a.nc)(ctx) }},
		},
	}))

	// 管理员端：只接受平台管理员，会话 cookie 是 __Host-admin，权限按平台的域加载。
	router.Route("/admin/api", func(r chi.Router) {
		r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
		api := rest.New(r, rest.Options{
			Title: "agents-desk 管理员端", Version: "0.1.0", BasePath: "/admin/api",
			Accounts:      identity.Accounts{platformadmin.Type: platformadmin.Resolve(a.db, a.roles)},
			SessionCookie: platformadmin.SessionCookie,
			Permissions:   auth.Catalog,
		})
		require := func(perms ...auth.Permission) func(*huma.Operation) {
			names := make([]string, len(perms))
			for i, p := range perms {
				names[i] = string(p)
			}
			return rest.RequireToken(api, a.adminTokens.Validate, names...)
		}
		addr := tenant.Addresses{PublicURL: a.cfg.PublicURL}
		platformadmin.RegisterRoutes(api, require(), a.db, a.adminLogins, a.adminTokens, a.cfg.AdminToken.Lifetime)
		tenant.RegisterRoutes(api, require(auth.TenantsRead), require(auth.TenantsManage), a.db, a.memberLogins, addr)
		llm.RegisterRoutes(api, require(auth.PlatformSettingsManage), a.db, a.llmSettings, a.crypt)
	})

	// 工作区端：成员登录和工作区的接口在 PR 2 中加入。
	router.Route("/api", func(r chi.Router) {
		r.Use(httpx.Timeout(a.cfg.HTTP.Timeout))
		rest.New(r, rest.Options{Title: "agents-desk", Version: "0.1.0", BasePath: "/api"})
	})

	if pages != nil {
		router.Handle("/*", pages)
	}
	return router
}

// docRouter 用默认配置和没有打开任何资源的 App 构造路由，openapi、routes、client 命令用它。
func docRouter() chi.Routes {
	return (&App{cfg: config.Default[Config]()}).routes(nil)
}
