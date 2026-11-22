package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/natsbroadcast"
	"github.com/go-tenon/tenon/cleanup"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/database/migrate"
	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/local"
	"github.com/go-tenon/tenon/filesystem/s3"
	"github.com/go-tenon/tenon/hashing"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/natsx"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
	"github.com/go-tenon/tenon/realtime"
	"github.com/go-tenon/tenon/realtime/natspresence"
	"github.com/go-tenon/tenon/schedule"
	"github.com/go-tenon/tenon/schedule/scheduleapi"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/auth"
	"agents-desk/internal/automation"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/translation"
	"agents-desk/internal/widget"
	"agents-desk/migrations"
)

// App 是进程中各部分共用的资源，每个进程只装配一次。
type App struct {
	cfg Config
	db  *bun.DB
	nc  *nats.Conn
	hub *broadcast.Hub // 经 NATS 把消息送到每个实例：实时推送
	// presence 保存会话频道的在线成员（谁正在看），所有实例共用 NATS 中的存储
	presence realtime.PresenceStore
	roles    *authz.Store // 平台和各租户的角色与权限

	adminLogins   *login.Realm    // 平台管理员的邮箱和密码
	adminTokens   *apitoken.Store // 平台管理员的登录 token
	memberLogins  *login.Realm    // 工作区成员的邮箱和密码
	memberTokens  *apitoken.Store // 工作区成员的登录 token
	contactTokens *apitoken.Store // 客户（联系人）在聊天插件中的令牌
	crypt         *crypt.Crypt    // 应用密钥：加密保存聊天插件的身份验证密钥

	llmSettings         *settings.Group[llm.Settings]         // 模型接口的平台设置
	translationSettings *settings.Group[translation.Settings] // 各工作区的翻译设置：语气、术语表
	translator          *translation.Translator               // 调用模型识别语言和翻译

	files       *filesystem.Service // 上传的文件：头像等
	defaultDisk filesystem.Disk     // 默认文件存储磁盘，用于运行环境自检
	localDisk   *local.Disk         // 本地磁盘，在 /storage/ 下提供访问；没有配置本地磁盘时为空
	locker      *lock.Locker        // 多个实例之间的锁：定时任务去重

	channels channel.Registry // 各种渠道的连接器
}

// downstream 返回会话有变化时通知的下游：把发给对方的消息交给连接器发出，翻译收到的消息，提交后推送给能看到会话的成员，
// 会话的状态变化推送给客户的聊天窗口。
// 没有广播时（只构造路由的命令）两种推送都不做。
func (a *App) downstream() conversation.Downstream {
	down := conversation.Downstream{Topics: []queue.Topic[conversation.Appended]{conversation.DeliverTopic, translation.Topic, automation.Topic}, Hub: a.hub, Roles: a.roles}
	if a.hub != nil {
		down.ContactPush = widget.Push(a.hub)
	}
	return down
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
	// 广播：会话的变化经它推送给各实例上的实时连接。登记在 NATS 之后，退出时先于 NATS 关闭
	hub := broadcast.New(natsbroadcast.New(nc, "agents-desk.broadcast"), nil)
	stack.MustAdd("broadcast", func(context.Context) error { return hub.Close() })
	presence, err := natspresence.New(ctx, nc, natspresence.Options{Bucket: "agents_desk_presence"})
	if err != nil {
		return nil, err
	}

	keys := crypt.New(cfg.Crypt) // 派生登录验证码的密钥，加密聊天插件的身份验证密钥
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

	// 上传的文件：打开配置了的磁盘（本地目录、S3），交给文件服务。
	disks := map[string]filesystem.Disk{}
	var localDisk *local.Disk
	if cfg.Filesystem.Disks.Local.Root != "" {
		if localDisk, err = local.Open(cfg.Filesystem.Disks.Local); err != nil {
			return nil, err
		}
		stack.MustAdd("files:local", func(context.Context) error { return localDisk.Close() })
		disks["local"] = localDisk
	}
	if cfg.Filesystem.Disks.S3.Bucket != "" {
		s3Disk, err := s3.Open(ctx, cfg.Filesystem.Disks.S3)
		if err != nil {
			return nil, err
		}
		disks["s3"] = s3Disk
	}
	files, err := filesystem.New(db, cfg.Filesystem, disks)
	if err != nil {
		return nil, err
	}
	llmSettings, translationSettings := llm.NewGroup(db), translation.NewGroup(db)

	return &App{
		cfg: cfg, db: db, nc: nc, hub: hub, presence: presence, roles: roles,
		adminLogins: adminLogins, adminTokens: apitoken.NewStore(db, cfg.AdminToken),
		memberLogins: memberLogins, memberTokens: apitoken.NewStore(db, cfg.MemberToken),
		contactTokens: apitoken.NewStore(db, cfg.ContactToken), crypt: keys,
		llmSettings: llmSettings, translationSettings: translationSettings,
		translator: translation.NewTranslator(db, llm.NewClient(db, llmSettings, &http.Client{Timeout: 60 * time.Second}), translationSettings),
		files:      files, defaultDisk: disks[cfg.Filesystem.DefaultDisk], localDisk: localDisk, locker: lock.New(pglock.New(db)),
		channels: registry(hub),
	}, nil
}

// registry 返回各种渠道的连接器：内置的测试渠道，以及把回复推送给聊天窗口的网页聊天插件。
func registry(hub *broadcast.Hub) channel.Registry {
	r := channel.DefaultRegistry()
	r[channel.Widget] = widget.NewConnector(hub)
	return r
}

// parts 是常驻进程的运行部分：serve 运行全部部分，serve http、serve worker 只运行其中一个。
func parts(a *App) []command.Part {
	return []command.Part{
		{Name: "http", Short: "接口和页面", Start: a.serveHTTP},
		{Name: "worker", Short: "处理队列任务", Start: a.worker},
		{Name: "scheduler", Short: "定时任务", Start: a.scheduler},
	}
}

// worker 装配队列 worker：把业务事务中发布的任务转发到 JetStream 并处理。各模块的任务在这里登记。
func (a *App) worker(*command.Daemon) (func(context.Context) error, error) {
	w := queue.NewWorker(a.db, a.nc, a.cfg.Queue, nil)
	down := a.downstream()
	conversation.RegisterJobs(w, a.db, a.channels)
	translation.RegisterJobs(w, a.db, a.locker, a.translator, down)
	automation.RegisterJobs(w, a.db, a.locker, down)
	scheduleapi.HandleRunNow(w, a.tasks)
	return w.Run, nil
}

// tasks 创建调度器并注册全部定时任务：调度器进程、schedules 命令和管理员端定时任务面板共用这一份定义。
func (a *App) tasks() *schedule.Scheduler {
	sched := schedule.New(a.locker, a.cfg.Schedule, nil, schedule.WithObserver(scheduleapi.Observer(a.db)))
	sched.Add("files:cleanup", "*/5 * * * *", schedule.Cleanup(a.files.Cleanup, 500),
		schedule.Describe("删除过期的临时文件和已删除的文件"))
	sched.Add("locks:cleanup", "@hourly", schedule.Cleanup(pglock.New(a.db).Cleanup, 1000),
		schedule.Describe("删除过期的锁"))
	sched.Add("apitoken:cleanup", "@hourly", schedule.Cleanup(a.adminTokens.Cleanup, 1000),
		schedule.Describe("删除过期的登录与访问令牌"))
	sched.Add("login:cleanup", "@hourly", schedule.Cleanup(a.adminLogins.Cleanup, 1000),
		schedule.Describe("删除过期或已完成的登录验证记录"))
	sched.Add("ratelimit:cleanup", "@hourly", schedule.Cleanup(pglimit.New(a.db, "cleanup", ratelimit.Config{Rate: 1, Interval: time.Minute, Burst: 1}).Cleanup, 1000),
		schedule.Describe("删除过期的限流计数"))
	sched.Add("queue:prune-failed", "@daily", schedule.Cleanup(queue.NewFailedJobs(a.db).Cleanup(30*24*time.Hour), 1000),
		schedule.Describe("删除 30 天前的队列失败任务"))
	sched.Add("schedule:prune-runs", "@daily", schedule.Cleanup(scheduleapi.Cleanup(a.db, 7*24*time.Hour), 1000),
		schedule.Describe("删除 7 天前的定时任务执行记录"))
	sched.Add("activity:cleanup", "@daily", schedule.Cleanup(activity.Cleanup(a.db, 180*24*time.Hour), 1000),
		schedule.Describe("删除 180 天前的操作日志"))
	expire := conversation.NewExpireSnoozes(a.db, a.downstream())
	sched.Add("conversations:unsnooze", "* * * * *", func(ctx context.Context) error {
		_, err := expire.Handle(ctx, time.Now())
		return err
	}, schedule.Describe("重新打开稍后处理到期的会话"))
	return sched
}

// scheduler 装配定时任务。部署多个实例时，同一次触发只在一个实例上执行。
func (a *App) scheduler(*command.Daemon) (func(context.Context) error, error) {
	return a.tasks().Run, nil
}

// doctor 返回运行环境自检分组：数据库、迁移、队列（复制槽与 NATS/JetStream）以及默认文件存储。
// doctor 命令和管理后台的 /admin/api/system/doctor 接口共用这一份检查定义。
func (a *App) doctor() []doctor.Group {
	groups := []doctor.Group{
		database.Doctor(a.db),
		migrate.Doctor(a.db, migrations.FS),
		queue.Doctor(a.db, a.nc, a.cfg.Queue),
	}
	if a.defaultDisk != nil {
		groups = append(groups, filesystem.Doctor(a.defaultDisk))
	}
	return groups
}
