package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/cleanup"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/hashing"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/natsx"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/auth"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
)

// App 是进程中各部分共用的资源，每个进程只装配一次。
type App struct {
	cfg   Config
	db    *bun.DB
	nc    *nats.Conn
	roles *authz.Store // 平台和各租户的角色与权限

	adminLogins  *login.Realm    // 平台管理员的邮箱和密码
	adminTokens  *apitoken.Store // 平台管理员的登录 token
	memberLogins *login.Realm    // 工作区成员的邮箱和密码
	memberTokens *apitoken.Store // 工作区成员的登录 token

	llmSettings *settings.Group[llm.Settings] // 模型接口的平台设置
}

// open 安装日志器，连接 PostgreSQL 和 NATS，确保预置的角色存在，创建账号、权限和设置的存储。serve 和需要资源的命令执行时调用它，
// 登记到 stack 的资源在进程退出时按相反顺序关闭。
func open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
	slog.SetDefault(logging.New(logging.Config{}))

	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
	stack.MustAdd("database", func(context.Context) error { return db.Close() })

	nc, err := natsx.Connect(cfg.NATS, "agents-desk", nil)
	if err != nil {
		return nil, err
	}
	stack.MustAdd("nats", func(context.Context) error {
		nc.Close()
		return nil
	})

	keys := crypt.New(cfg.Crypt) // 派生登录验证码的密钥
	hasher := hashing.New(cfg.Hashing)
	roles := authz.NewStore(db, auth.Catalog)
	if err := auth.EnsureRoles(ctx, roles); err != nil {
		return nil, err
	}
	// 每个邮箱 15 分钟内最多输错 5 次密码，计数保存在数据库中，所有实例共享。两套账号各自计数。
	every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
	adminLogins := login.New(db, login.Options{
		Type:     platformadmin.Type,
		Hasher:   hasher,
		Crypt:    keys,
		Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
		Resolve:  platformadmin.Resolve(db, roles), // 登录成功、签发 token 之前检查停用
		Attempts: pglimit.New(db, "login:platform_admin:attempts", every15),
		Sends:    pglimit.New(db, "login:platform_admin:sends", every15),
	})
	memberLogins := login.New(db, login.Options{
		Type:     member.Type,
		Hasher:   hasher,
		Crypt:    keys,
		Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
		Resolve:  member.Resolve(db, roles), // 登录成功、签发 token 之前检查停用
		Attempts: pglimit.New(db, "login:member:attempts", every15),
		Sends:    pglimit.New(db, "login:member:sends", every15),
	})

	return &App{
		cfg: cfg, db: db, nc: nc, roles: roles,
		adminLogins: adminLogins, adminTokens: apitoken.NewStore(db, cfg.AdminToken),
		memberLogins: memberLogins, memberTokens: apitoken.NewStore(db, cfg.MemberToken),
		llmSettings: llm.NewGroup(db),
	}, nil
}

// parts 是常驻进程的运行部分：serve 运行全部部分，serve http、serve worker 只运行其中一个。
func parts(a *App) []command.Part {
	return []command.Part{
		{Name: "http", Short: "接口和页面", Start: a.serveHTTP},
		{Name: "worker", Short: "处理队列任务", Start: a.worker},
	}
}

// worker 装配队列 worker：把业务事务中发布的任务转发到 JetStream 并处理。各模块的任务在这里登记，PR 1 还没有任务。
func (a *App) worker(*command.Daemon) (func(context.Context) error, error) {
	w := queue.NewWorker(a.db, a.nc, a.cfg.Queue, nil)
	return w.Run, nil
}
