package main

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/nats-io/nats.go"
	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"

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
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/local"
	"github.com/go-tenon/tenon/filesystem/s3"
	"github.com/go-tenon/tenon/hashing"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/imgproxy"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/logging"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/mcpx/opsmcp"
	"github.com/go-tenon/tenon/natsx"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/outbound"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/ratelimit/pglimit"
	"github.com/go-tenon/tenon/realtime"
	"github.com/go-tenon/tenon/realtime/natspresence"
	"github.com/go-tenon/tenon/schedule"
	"github.com/go-tenon/tenon/schedule/pgschedule"
	"github.com/go-tenon/tenon/schedule/scheduleapi"
	"github.com/go-tenon/tenon/settings"
	"github.com/go-tenon/tenon/sheet/exports"
	"github.com/go-tenon/tenon/telemetry"

	"agents-desk/internal/adminnotify"
	"agents-desk/internal/agent"
	"agents-desk/internal/auth"
	"agents-desk/internal/automation"
	"agents-desk/internal/business"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/emails"
	"agents-desk/internal/flags"
	"agents-desk/internal/lang"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/platformsettings"
	"agents-desk/internal/push"
	"agents-desk/internal/telegram"
	"agents-desk/internal/tenant"
	"agents-desk/internal/tgaccount"
	"agents-desk/internal/translation"
	"agents-desk/internal/wecom"
	"agents-desk/internal/widget"
	"agents-desk/migrations"
)

// App 是进程中各部分共用的资源，每个进程只装配一次。
type App struct {
	cfg   Config
	db    *bun.DB
	opsDB *bun.DB // 只读运维排障数据库连接池；未配置 ops_dsn 时与 db 相同
	nc    *nats.Conn
	tel   *telemetry.Providers   // OpenTelemetry 指标、链路和日志提供方；离线构造路由时为 nil
	hub   *broadcast.Hub         // 经 NATS 把消息送到每个实例：实时推送
	creds *broadcast.Credentials // 凭据撤销、管理员停用时通知长连接立即复查
	// presence 保存会话频道的在线成员（谁正在看），所有实例共用 NATS 中的存储
	presence realtime.PresenceStore
	roles    *authz.Store // 平台和各租户的角色与权限

	adminLogins   *login.Realm    // 平台管理员的邮箱和密码
	adminTokens   *apitoken.Store // 平台管理员的登录 token
	memberLogins  *login.Realm    // 工作区成员的邮箱和密码
	memberTokens  *apitoken.Store // 工作区成员的登录 token
	contactTokens *apitoken.Store // 客户（联系人）在聊天插件中的令牌
	crypt         *crypt.Crypt    // 应用密钥：加密保存聊天插件的身份验证密钥

	generalSettings     *settings.Group[platformsettings.General] // 平台常规设置
	mailSettings        *settings.Group[platformsettings.Mail]    // 平台邮件服务设置
	tgCredentials       *settings.Group[tgaccount.Credentials]    // 各工作区独立的 Telegram MTProto 凭据与代理设置
	llmSettings         *settings.Group[llm.Settings]             // 模型接口的平台设置
	translationSettings *settings.Group[translation.Settings]     // 各工作区的翻译设置：语气、术语表
	businessPolicy      *settings.Group[business.Policy]          // 各工作区的商务沟通默认策略与配额
	features            *feature.Store                            // 平台功能开关与灰度规则
	translator          *translation.Translator                   // 调用模型识别语言和翻译
	emailTemplates      *mail.Templates                           // 嵌入的双语邮件模板
	mailer              *platformsettings.DynamicMailer           // 动态读取 Resend 设置的邮件发送器
	offlineMailLimiter  ratelimit.Limiter                         // 离线访客回复邮件提醒冷却限流器
	alertNotifier       *adminnotify.AlertNotifier                // 平台管理员主动告警通知器（含冷却防风暴）

	files       *filesystem.Service // 上传的文件：头像、会话附件与导出文件等
	images      *imgproxy.Proxy     // 图片缩略图地址生成；未配置 url 时返回原图地址
	ops         *operation.Service  // 后台长任务状态与进度
	exporter    *exports.Service    // 后台表格导出服务
	defaultDisk filesystem.Disk     // 默认文件存储磁盘，用于运行环境自检
	localDisk   *local.Disk         // 本地磁盘，在 /storage/ 下提供访问；没有配置本地磁盘时为空
	locker      *lock.Locker        // 多个实例之间的锁：协调翻译和自动化等任务的互斥执行

	channels   channel.Registry   // 各种渠道的连接器
	runners    []channel.Runner   // 长连接类渠道的后台运行器（如 Telegram 个人号、企业微信智能机器人）
	telegram   *telegram.Client   // 调用 Telegram Bot API 的客户端
	tgAccounts *tgaccount.Manager // 成员 Telegram 个人号（纯 Go MTProto）管理器
	wecomBots  *wecom.Manager     // 企业微信智能机器人（WebSocket 长连接）管理器
	agentSvc   *agent.Service     // 统一托管于 tenon/ai/chat 的 AI Agent 运行时服务
}

// downstream 返回会话有变化时通知的下游：把发给对方的消息交给连接器发出，翻译收到的消息，提交后推送给能看到会话的成员，
// 会话的状态变化推送给客户的聊天窗口。
// 没有广播时（只构造路由的命令）两种推送都不做。
func (a *App) downstream() conversation.Downstream {
	down := conversation.Downstream{
		Sinks: []conversation.Sink{
			{Topic: conversation.DeliverTopic, Accept: conversation.Delivers},
			{Topic: translation.Topic, Accept: translation.Accepts, OnEdit: true},
			{Topic: translation.TicketBroadcastTopic, Accept: translation.Broadcasts},
			{Topic: automation.Topic, Accept: automation.Accepts},
			{Topic: agent.Topic, Accept: agent.Triggers},
		},
		Hub:      a.hub,
		Roles:    a.roles,
		Registry: a.channels,
		Media:    conversation.Media{Files: a.files, Images: a.images, Crypt: a.crypt},
	}
	if a.hub != nil {
		down.ContactPush = widget.Push(a.hub)
	}
	if a.db != nil && a.mailer != nil && a.emailTemplates != nil {
		down.NotifyOfflineReply = a.notifyOfflineReply
	}
	if a.contactTokens != nil {
		tokens := a.contactTokens
		down.RevokeContactTokens = func(ctx context.Context, tx database.Tx, _ string, contactID string) error {
			_, err := tokens.WithDB(tx).RevokeSubject(ctx, identity.Ref{Type: contact.Type, ID: contactID})
			return errors.Wrap(err, "revoke contact tokens")
		}
	}
	if a.alertNotifier != nil {
		down.OnDeadAlert = a.alertNotifier.NotifyDeadJob
	}
	if a.agentSvc != nil {
		down.OnGenerationCanceled = a.agentSvc.CancelRun
	}
	return down
}

// notifyOfflineReply 在客服向网页聊天插件或商务链接客户成功发出回复后检查条件并在同事务中异步发布离线邮件提醒。
func (a *App) notifyOfflineReply(ctx context.Context, tx database.Tx, tenantID, conversationID, itemID string) error {
	configured, err := a.mailer.Configured(ctx)
	if err != nil || !configured {
		return err
	}
	var row struct {
		ChannelKind  channel.Kind `bun:"channel_kind"`
		ChannelID    string       `bun:"channel_id"`
		ChannelName  string       `bun:"channel_name"`
		ContactID    string       `bun:"contact_id"`
		ContactEmail string       `bun:"contact_email"`
		ContactName  string       `bun:"contact_name"`
		ContactLang  string       `bun:"contact_lang"`
		Preview      string       `bun:"preview"`
	}
	err = tx.NewRaw(`SELECT ch.kind AS channel_kind, ch.id AS channel_id, ch.name AS channel_name,
			ct.id AS contact_id, ct.email AS contact_email, ct.name AS contact_name, ct.language AS contact_lang,
			ti.content->>'text' AS preview
		FROM timeline_items ti
		JOIN conversations c ON c.id = ti.conversation_id AND c.tenant_id = ti.tenant_id
		JOIN channels ch ON ch.id = ti.channel_id AND ch.tenant_id = ti.tenant_id
		JOIN contacts ct ON ct.id = c.contact_id AND ct.tenant_id = c.tenant_id
		WHERE ti.id = ? AND ti.tenant_id = ? AND ti.conversation_id = ?`,
		itemID, tenantID, conversationID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.ChannelKind != channel.Widget && row.ChannelKind != channel.BusinessLink {
		return nil
	}
	emailAddr := strings.TrimSpace(row.ContactEmail)
	if emailAddr == "" {
		return nil
	}
	if a.offlineMailLimiter != nil {
		if err := a.offlineMailLimiter.Allow(ctx, "offline_reply:"+conversationID); err != nil {
			if errors.Is(err, ratelimit.ErrExceeded) {
				return nil
			}
			return err
		}
	}
	gen, err := a.generalSettings.Get(ctx)
	if err != nil {
		return err
	}
	targetLocale := lang.MatchUI(row.ContactLang)
	renderCtx := emails.WithLocale(ctx, targetLocale)
	returnURL := ""
	if base := strings.TrimRight(strings.TrimSpace(a.cfg.HTTP.PublicURL), "/"); base != "" {
		returnURL = base + "/widget/?channel=" + row.ChannelID
	}
	preview := strings.TrimSpace(row.Preview)
	if r := []rune(preview); len(r) > 200 {
		preview = string(r[:200]) + "…"
	}
	msg, err := a.emailTemplates.Render(renderCtx, "offline_reply", map[string]any{
		"SiteName":    gen.SiteName,
		"ChannelName": row.ChannelName,
		"ContactName": row.ContactName,
		"Preview":     preview,
		"ReturnURL":   returnURL,
	})
	if err != nil {
		return err
	}
	msg.To = []mail.Address{{Name: row.ContactName, Email: emailAddr}}
	return emails.SendTopic.Publish(ctx, tx, msg)
}

// sendResetCode 生成用于 login.Realm 的密码重置验证码邮件发送回调。
func sendResetCode(general *settings.Group[platformsettings.General], templates *mail.Templates, mailer mail.Mailer) func(context.Context, login.Code) error {
	return func(ctx context.Context, c login.Code) error {
		if c.To.Kind != login.KindEmail {
			return nil
		}
		gen, err := general.Get(ctx)
		if err != nil {
			return err
		}
		renderCtx := emails.EnsureLocale(ctx, string(gen.DefaultLanguage))
		msg, err := templates.Render(renderCtx, "code_reset", map[string]any{
			"SiteName":       gen.SiteName,
			"Code":           c.Code,
			"ExpiresMinutes": 15,
		})
		if err != nil {
			return err
		}
		msg.To = []mail.Address{{Email: c.To.Value}}
		return mailer.Send(ctx, msg)
	}
}

// open 安装可观测性与日志器，连接 PostgreSQL 和 NATS，确保预置的角色存在，创建账号、权限和设置的存储。serve 和需要资源的命令执行时调用它，
// 登记到 stack 的资源在进程退出时按相反顺序关闭。
func open(ctx context.Context, cfg Config, stack *cleanup.Stack) (*App, error) {
	tel, err := telemetry.Open(ctx, cfg.Telemetry)
	if err != nil {
		return nil, err
	}
	stack.MustAdd("telemetry", tel.Shutdown)
	slog.SetDefault(logging.New(cfg.Logs, logging.Export(tel.Logs)))

	db, err := database.Open(ctx, cfg.Database, database.WithTelemetry(tel.Meters, tel.Tracers))
	if err != nil {
		return nil, err
	}
	stack.MustAdd("database", func(context.Context) error { return db.Close() })

	opsDB := db
	if cfg.OpsDSN != "" {
		opsDB, err = database.Open(ctx, database.Config{DSN: cfg.OpsDSN, MaxConns: 2},
			database.ReadOnly(), database.Name("ops"), database.WithTelemetry(tel.Meters, tel.Tracers))
		if err != nil {
			return nil, err
		}
		stack.MustAdd("ops_database", func(context.Context) error { return opsDB.Close() })
	}

	nc, err := natsx.Connect(cfg.NATS, "agents-desk")
	if err != nil {
		return nil, err
	}
	stack.MustAdd("nats", func(context.Context) error {
		nc.Close()
		return nil
	})
	// 广播：会话的变化经它推送给各实例上的实时连接。登记在 NATS 之后，退出时先于 NATS 关闭
	hub := broadcast.New(natsbroadcast.New(nc, "agents-desk.broadcast"))
	stack.MustAdd("broadcast", func(context.Context) error { return hub.Close() })
	creds := broadcast.NewCredentials(hub)
	presence, err := natspresence.New(ctx, nc, natspresence.Options{Bucket: "agents_desk_presence"})
	if err != nil {
		return nil, err
	}

	keys := crypt.New(cfg.Crypt) // 派生登录验证码的密钥，加密保存聊天插件密钥与平台密钥设置
	hasher := hashing.New(cfg.Hashing)
	roles := authz.NewStore(db, auth.Catalog, authz.Credentials(creds))
	if err := auth.EnsureRoles(ctx, roles); err != nil {
		return nil, err
	}
	emailTemplates, err := emails.NewTemplates()
	if err != nil {
		return nil, err
	}
	generalSettings, mailSettings := platformsettings.NewGeneralGroup(db), platformsettings.NewMailGroup(db, keys)
	mailer := platformsettings.NewDynamicMailer(mailSettings)
	// 每个邮箱 15 分钟内最多输错 5 次密码，计数保存在数据库中，所有实例共享。两套账号各自计数。
	every15 := ratelimit.Config{Rate: 5, Interval: 15 * time.Minute, Burst: 5}
	sendCode := sendResetCode(generalSettings, emailTemplates, mailer)
	adminLogins := login.New(db, login.Options{
		Type:     platformadmin.Type,
		Hasher:   hasher,
		Crypt:    keys,
		Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
		Send:     sendCode,
		Resolve:  platformadmin.Resolve(db, roles), // 登录成功、签发 token 之前检查停用
		Attempts: pglimit.New(db, "login:platform_admin:attempts", every15),
		Sends:    pglimit.New(db, "login:platform_admin:sends", every15),
	})
	memberLogins := login.New(db, login.Options{
		Type:     member.Type,
		Hasher:   hasher,
		Crypt:    keys,
		Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
		Send:     sendCode,
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
	// 私有磁盘仅用于会话附件和导出文件，严格禁止挂载到公开 /storage/*。
	if cfg.Filesystem.Disks.LocalPrivate.Root != "" {
		privCfg := cfg.Filesystem.Disks.LocalPrivate
		privCfg.Private = true
		privDisk, err := local.Open(privCfg)
		if err != nil {
			return nil, err
		}
		stack.MustAdd("files:local_private", func(context.Context) error { return privDisk.Close() })
		disks["local_private"] = privDisk
	}
	if cfg.Filesystem.Disks.S3Private.Bucket != "" {
		privCfg := cfg.Filesystem.Disks.S3Private
		privCfg.Private = true
		s3Priv, err := s3.Open(ctx, privCfg)
		if err != nil {
			return nil, err
		}
		disks["s3_private"] = s3Priv
	}
	files, err := filesystem.New(db, cfg.Filesystem, disks)
	if err != nil {
		return nil, err
	}
	images, err := imgproxy.New(cfg.Imgproxy)
	if err != nil {
		return nil, err
	}
	tgCredentials := tgaccount.NewCredentialsGroup(db, keys)
	llmSettings, translationSettings, businessPolicy := llm.NewGroup(db, keys), translation.NewGroup(db), business.NewPolicyGroup(db)
	featureStore := flags.NewStore(db)
	ops := operation.New(db)
	exporter := exports.New(db, ops, files, exports.WithDisk(files.PrivateDisk()))

	locker := lock.New(pglock.New(db))
	tgClient := telegram.NewClient(outbound.New(outbound.Options{Timeout: 2 * time.Minute}))
	tgMgr := tgaccount.NewManager(db, keys, files, tgCredentials, nil)
	tgMgr.SetLocker(locker)
	tgMgr.SetNATS(nc)
	bizGuard := business.NewPolicyGuard(db, businessPolicy, featureStore, tgCredentials)
	tgMgr.SetPolicyChecker(bizGuard.CheckConnectTelegram)
	wecomMgr := wecom.NewManager(db, keys, files, locker, nc)
	// 模型网关不设固定整请求超时（由调用上下文控制），以便兼容流式 SSE 长响应及自托管网关地址
	llmClient := llm.NewClient(db, llmSettings, outbound.New(outbound.Options{Timeout: -1, AnyPort: true}))
	tr := translation.NewTranslator(db, llmClient, translationSettings)
	tr.SetBusinessPolicy(businessPolicy)
	tr.SetFeatures(featureStore)
	agentSvc := agent.NewService(db, conversation.Downstream{}, hub, locker, llmClient, translationSettings)
	stack.MustAdd("ai", agentSvc.Shutdown)
	app := &App{
		cfg: cfg, db: db, opsDB: opsDB, nc: nc, tel: tel, hub: hub, creds: creds, presence: presence, roles: roles,
		adminLogins:     adminLogins,
		adminTokens:     apitoken.NewStore(db, cfg.AdminToken, apitoken.Credentials(creds)),
		memberLogins:    memberLogins,
		memberTokens:    apitoken.NewStore(db, cfg.MemberToken, apitoken.Credentials(creds)),
		contactTokens:   apitoken.NewStore(db, cfg.ContactToken, apitoken.Credentials(creds)),
		crypt:           keys,
		generalSettings: generalSettings, mailSettings: mailSettings, tgCredentials: tgCredentials,
		llmSettings: llmSettings, translationSettings: translationSettings, businessPolicy: businessPolicy, features: featureStore,
		emailTemplates:     emailTemplates,
		mailer:             mailer,
		offlineMailLimiter: pglimit.New(db, "mail:offline_reply", ratelimit.Config{Rate: 1, Interval: 15 * time.Minute, Burst: 1}),
		alertNotifier:      adminnotify.NewAlertNotifier(db, hub, pglimit.New(db, "admin:alert", ratelimit.Config{Rate: 1, Interval: 15 * time.Minute, Burst: 1})),
		ops:                ops, exporter: exporter,
		translator: tr,
		files:      files, images: images, defaultDisk: disks[cfg.Filesystem.DefaultDisk], localDisk: localDisk, locker: locker,
		channels:   registry(hub, keys, files, tgClient, tgMgr, wecomMgr),
		runners:    []channel.Runner{tgMgr, wecomMgr},
		telegram:   tgClient,
		tgAccounts: tgMgr,
		wecomBots:  wecomMgr,
		agentSvc:   agentSvc,
	}
	down := app.downstream()
	agentSvc.SetDownstream(down)
	recv := conversation.NewReceive(db, app.channels, down)
	tgMgr.SetReceive(recv)
	wecomMgr.SetReceive(recv)
	return app, nil
}

// registry 返回各种渠道的连接器：内置测试渠道、网页聊天插件、个人网页商务沟通链接、Telegram 机器人、Telegram 个人号和企业微信智能机器人。
func registry(hub *broadcast.Hub, keys *crypt.Crypt, files *filesystem.Service, tgClient *telegram.Client, tgMgr *tgaccount.Manager, wecomMgr *wecom.Manager) channel.Registry {
	r := channel.DefaultRegistry()
	widgetConn := widget.NewConnector(hub)
	r[channel.Widget] = widgetConn
	r[channel.BusinessLink] = widgetConn
	r[channel.Telegram] = telegram.NewConnector(keys, files, tgClient)
	if tgMgr != nil {
		r[channel.TelegramAccount] = tgMgr.Connector()
	}
	if wecomMgr != nil {
		r[channel.WeCom] = wecomMgr.Connector()
	}
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
	var opts []queue.WorkerOption
	if a.tel != nil {
		opts = append(opts, queue.WithTelemetry(a.tel.Meters, a.tel.Tracers))
	}
	w := queue.NewWorker(a.db, a.nc, a.cfg.Queue, opts...)
	down := a.downstream()
	down.AutoTranslate = translation.AutoTranslateFunc(a.db, a.translator, translation.NewIncoming(a.db, a.locker, a.translator, down))
	conversation.RegisterJobs(w, a.db, a.channels, down)
	translation.RegisterJobs(w, a.db, a.locker, a.translator, down)
	automation.RegisterJobs(w, a.db, a.locker, down, a.features)
	emails.RegisterJobs(w, a.mailer, func(ctx context.Context, _ mail.Message, cause error) error {
		if a.alertNotifier != nil {
			a.alertNotifier.NotifyDeadJob(ctx, string(emails.SendTopic), cause)
		}
		return nil
	})
	a.agentSvc.SetDownstream(down)
	agent.RegisterJobs(w, a.agentSvc.Runner())
	tenant.RegisterJobs(w, a.db, a.memberLogins, tenant.Addresses{PublicURL: a.cfg.HTTP.PublicURL}, a.hub, a.exporter)
	contact.RegisterJobs(w, a.db, a.hub, a.exporter)
	conversation.RegisterExportJobs(w, a.db, a.hub, a.exporter)
	scheduleapi.HandleRunNow(w, a.tasks)
	return func(ctx context.Context) error {
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error { return w.Run(gctx) })
		for _, runner := range a.runners {
			if runner == nil {
				continue
			}
			r := runner
			g.Go(func() error { return r.Run(gctx) })
		}
		return g.Wait()
	}, nil
}

// tasks 创建调度器并注册全部定时任务：调度器进程、schedules 命令和管理员端定时任务面板共用这一份定义。
func (a *App) tasks() *schedule.Scheduler {
	opts := []schedule.SchedulerOption{schedule.WithObserver(scheduleapi.Observer(a.db))}
	if a.tel != nil {
		opts = append(opts, schedule.WithTelemetry(a.tel.Meters, a.tel.Tracers))
	}
	sched := schedule.New(pgschedule.New(a.db), a.cfg.Schedule, opts...)
	sched.Add("files:cleanup", "*/5 * * * *", schedule.Cleanup(a.files.Cleanup, 500),
		schedule.Label("schedule.task.files_cleanup.label"),
		schedule.Describe("schedule.task.files_cleanup.description"))
	sched.Add("media:evict-cache", "@daily", func(ctx context.Context) error {
		_, err := conversation.EvictCachedMedia(ctx, a.db, a.files, time.Now().UTC().Add(-30*24*time.Hour), 200)
		return err
	},
		schedule.Label("schedule.task.media_evict_cache.label"),
		schedule.Describe("schedule.task.media_evict_cache.description"))
	sched.Add("locks:cleanup", "@hourly", schedule.Cleanup(pglock.Cleanup(a.db), 1000),
		schedule.Label("schedule.task.locks_cleanup.label"),
		schedule.Describe("schedule.task.locks_cleanup.description"))
	sched.Add("apitoken:cleanup", "@hourly", schedule.Cleanup(apitoken.Cleanup(a.db), 1000),
		schedule.Label("schedule.task.apitoken_cleanup.label"),
		schedule.Describe("schedule.task.apitoken_cleanup.description"))
	sched.Add("login:cleanup", "@hourly", schedule.Cleanup(login.Cleanup(a.db), 1000),
		schedule.Label("schedule.task.login_cleanup.label"),
		schedule.Describe("schedule.task.login_cleanup.description"))
	sched.Add("ratelimit:cleanup", "@hourly", schedule.Cleanup(pglimit.Cleanup(a.db), 1000),
		schedule.Label("schedule.task.ratelimit_cleanup.label"),
		schedule.Describe("schedule.task.ratelimit_cleanup.description"))
	sched.Add("queue:prune-failed", "@daily", schedule.Cleanup(queue.CleanupFailed(a.db, 30*24*time.Hour), 1000),
		schedule.Label("schedule.task.queue_prune_failed.label"),
		schedule.Describe("schedule.task.queue_prune_failed.description"))
	sched.Add("schedule:prune-runs", "@daily", schedule.Cleanup(scheduleapi.Cleanup(a.db, 7*24*time.Hour), 1000),
		schedule.Label("schedule.task.schedule_prune_runs.label"),
		schedule.Describe("schedule.task.schedule_prune_runs.description"))
	sched.Add("activity:cleanup", "@daily", schedule.Cleanup(activity.Cleanup(a.db, 180*24*time.Hour), 1000),
		schedule.Label("schedule.task.activity_cleanup.label"),
		schedule.Describe("schedule.task.activity_cleanup.description"))
	sched.Add("notifications:cleanup", "@daily", schedule.Cleanup(notification.Cleanup(a.db, 90*24*time.Hour), 1000),
		schedule.Label("schedule.task.notifications_cleanup.label"),
		schedule.Describe("schedule.task.notifications_cleanup.description"))
	sched.Add("operations:cleanup", "@daily", schedule.Cleanup(operation.Cleanup(a.db, 30*24*time.Hour), 1000),
		schedule.Label("schedule.task.operations_cleanup.label"),
		schedule.Describe("schedule.task.operations_cleanup.description"))
	sched.Add("llm:prune-calls", "30 3 * * *", schedule.Cleanup(llm.CleanupCalls(a.db, 90*24*time.Hour), 500),
		schedule.Label("schedule.task.llm_prune_calls.label"),
		schedule.Describe("schedule.task.llm_prune_calls.description"))
	expire := conversation.NewExpireSnoozes(a.db, a.downstream())
	sched.Add("conversations:unsnooze", "* * * * *", func(ctx context.Context) error {
		_, err := expire.Handle(ctx, time.Now())
		return err
	},
		schedule.Label("schedule.task.conversations_unsnooze.label"),
		schedule.Describe("schedule.task.conversations_unsnooze.description"))
	sched.Add("generations:expire", "* * * * *", schedule.Cleanup(func(ctx context.Context, limit int) (int, error) {
		return agent.ExpireGenerations(ctx, a.db, time.Now().UTC(), limit)
	}, 500),
		schedule.Label("schedule.task.generations_expire.label"),
		schedule.Describe("schedule.task.generations_expire.description"))
	sched.Add("media:recover-fetching", "*/5 * * * *", schedule.Cleanup(func(ctx context.Context, limit int) (int, error) {
		return conversation.RecoverStaleMediaFetches(ctx, a.db, 5*time.Minute, limit)
	}, 500),
		schedule.Label("schedule.task.media_recover_fetching.label"),
		schedule.Describe("schedule.task.media_recover_fetching.description"))
	sched.Add("members:auto-away", "* * * * *", schedule.Cleanup(func(ctx context.Context, limit int) (int, error) {
		var notify func(context.Context, string, string) error
		if a.hub != nil {
			hub := a.hub
			notify = func(ctx context.Context, tenantID, memberID string) error {
				return push.Send(ctx, hub, tenantID, []string{memberID}, push.Event{Type: push.MemberStatus})
			}
		}
		return member.ExpireIdleOnline(ctx, a.db, 5*time.Minute, limit, notify)
	}, 500),
		schedule.Label("schedule.task.members_auto_away.label"),
		schedule.Describe("schedule.task.members_auto_away.description"))
	sched.Add("conversations:auto-close", "*/5 * * * *", schedule.Cleanup(func(ctx context.Context, limit int) (int, error) {
		return conversation.AutoCloseIdle(ctx, a.db, a.downstream(), time.Now().UTC(), limit)
	}, 200),
		schedule.Label("schedule.task.conversations_auto_close.label"),
		schedule.Describe("schedule.task.conversations_auto_close.description"))
	sched.Add("conversations:sla-reassign", "* * * * *", schedule.Cleanup(func(ctx context.Context, limit int) (int, error) {
		return conversation.ReassignSLABreached(ctx, a.db, a.downstream(), time.Now().UTC(), limit)
	}, 200),
		schedule.Label("schedule.task.conversations_sla_reassign.label"),
		schedule.Describe("schedule.task.conversations_sla_reassign.description"))
	sched.Add("system:health-monitor", "*/5 * * * *", func(ctx context.Context) error {
		return adminnotify.MonitorHealth(ctx, a.db, a.alertNotifier, func(c context.Context) doctor.DoctorReport {
			return doctor.Run(c, a.doctor()...)
		}, time.Now().UTC())
	},
		schedule.Label("schedule.task.system_health_monitor.label"),
		schedule.Describe("schedule.task.system_health_monitor.description"))
	return sched
}

// scheduler 装配定时任务。部署多个实例时，同一次触发只在一个实例上执行。
func (a *App) scheduler(*command.Daemon) (func(context.Context) error, error) {
	return a.tasks().Run, nil
}

// doctor 返回运行环境自检分组：数据库、迁移、队列（数据表与 NATS/JetStream）以及默认文件存储。
// doctor 命令和管理后台的 /admin/api/system/doctor 接口共用这一份检查定义。
func (a *App) doctor() []doctor.Group {
	groups := []doctor.Group{
		database.Doctor(a.db),
		migrate.Doctor(a.db, migrations.FS),
		queue.Doctor(a.db, a.nc),
	}
	if a.defaultDisk != nil {
		groups = append(groups, filesystem.Doctor(a.defaultDisk))
	}
	opsDB := a.opsDB
	if opsDB == nil {
		opsDB = a.db
	}
	if opsDB != nil {
		groups = append(groups, opsmcp.Doctor(opsDB))
	}
	return groups
}
