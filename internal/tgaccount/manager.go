package tgaccount

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// loginEntry 保存进行中的单次 Telegram 个人号交互式登录会话，归属隔离到 (tenantID, memberID)。
type loginEntry struct {
	id              string
	tenantID        string
	memberID        string
	targetAccountID string // 可选：原地重新授权已有渠道时指向目标 channel.ID
	name            string
	mode            LoginMode
	driver          LoginDriver
	createdAt       time.Time
	accountDTO      *AccountDTO
	persisted       bool
	sub             *nats.Subscription
	mu              sync.Mutex
}

func (e *loginEntry) close() {
	e.mu.Lock()
	sub := e.sub
	e.sub = nil
	drv := e.driver
	e.mu.Unlock()
	if sub != nil {
		_ = sub.Unsubscribe()
	}
	if drv != nil {
		drv.Close()
	}
}

// accountRunner 管理单个已绑定 Telegram 个人号的长连接协程、内存同步白名单与同步策略。
type accountRunner struct {
	channelID string
	tenantID  string
	creds     MTProtoConfig
	cancel    context.CancelFunc

	mu            sync.RWMutex
	client        LiveClient
	syncedPeers   map[string]SyncedPeer
	syncCfg       SyncConfig
	lastSyncTouch time.Time
}

func (r *accountRunner) setClient(c LiveClient) {
	r.mu.Lock()
	r.client = c
	r.mu.Unlock()
}

func (r *accountRunner) getClient() LiveClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.client
}

func (r *accountRunner) setPeers(peers []SyncedPeer) {
	m := make(map[string]SyncedPeer, len(peers))
	for _, p := range peers {
		m[p.ExternalChatID] = p
	}
	r.mu.Lock()
	r.syncedPeers = m
	r.mu.Unlock()
}

func (r *accountRunner) setSync(cfg SyncConfig) {
	r.mu.Lock()
	r.syncCfg = cfg
	r.mu.Unlock()
}

func (r *accountRunner) getSync() SyncConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.syncCfg
}

func (r *accountRunner) lookupPeer(extChatID string) (SyncedPeer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.syncedPeers[extChatID]
	return p, ok
}

// shouldTouchSync 按 30 秒节流窗口判断是否需要将 last_synced_at 写回数据库。
func (r *accountRunner) shouldTouchSync(now time.Time) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.lastSyncTouch.IsZero() && now.Sub(r.lastSyncTouch) < 30*time.Second {
		return false
	}
	r.lastSyncTouch = now
	return true
}

// UpdateAccountParams 是修改 Telegram 个人号名称、启停、同步策略与通用商务策略的参数。
type UpdateAccountParams struct {
	Name     string
	Enabled  bool
	Sync     *SyncConfig
	Business *channel.BusinessPolicy
}

// Manager 统一管理 Telegram 个人号交互式登录、长连接同步协程与出站消息投递连接器。
type Manager struct {
	db         *bun.DB
	crypt      *crypt.Crypt
	files      *filesystem.Service
	locker     *lock.Locker
	nc         *nats.Conn
	tgCreds    *settings.Group[Credentials]
	receive    *conversation.Receive
	backend    Backend
	checkQuota func(ctx context.Context, sub identity.Subject, isReconnect bool) error

	loginsMu sync.Mutex
	logins   map[string]*loginEntry

	runnersMu sync.RWMutex
	runners   map[string]*accountRunner
	wakeCh    chan struct{}
}

// NewManager 返回 Telegram 个人号管理器；backend 为空时使用纯 Go GotdBackend。
func NewManager(db *bun.DB, c *crypt.Crypt, files *filesystem.Service, tgCreds *settings.Group[Credentials], backend Backend) *Manager {
	if backend == nil {
		backend = NewGotdBackend()
	}
	return &Manager{
		db:      db,
		crypt:   c,
		files:   files,
		tgCreds: tgCreds,
		backend: backend,
		logins:  map[string]*loginEntry{},
		runners: map[string]*accountRunner{},
		wakeCh:  make(chan struct{}, 1),
	}
}

var _ channel.Runner = (*Manager)(nil)

// Name 返回后台长连接运行器名称，实现 channel.Runner。
func (*Manager) Name() string { return "tgaccount" }

// SetLocker 绑定分布式锁，多实例部署时保证每个 Telegram 个人号只由单个实例持有 MTProto 长连接。
func (m *Manager) SetLocker(l *lock.Locker) { m.locker = l }

// SetNATS 绑定 NATS 连接，用于跨实例转发个人号 RPC、控制信号与交互式登录请求。
func (m *Manager) SetNATS(nc *nats.Conn) { m.nc = nc }

// SetReceive 绑定消息入库动作（因 channel.Registry 与 conversation.Receive 存在初始化顺序依赖，由入口装配完成后注入）。
func (m *Manager) SetReceive(r *conversation.Receive) { m.receive = r }

// SetPolicyChecker 绑定工作区商务策略与配额校验函数。
func (m *Manager) SetPolicyChecker(fn func(ctx context.Context, sub identity.Subject, isReconnect bool) error) {
	m.checkQuota = fn
}

// Wake 通知后台常驻循环立即重新对齐已启用的个人号连接列表（供管理员启停渠道后触发）。
func (m *Manager) Wake() { m.wake() }

// wake 通知后台常驻循环立即重新对齐已启用的个人号连接列表。
func (m *Manager) wake() {
	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
}

// ReconnectTenant 断开指定工作区当前全部个人号长连接并触发重连（含跨实例广播），供该工作区更新 API 凭据或代理后调用，不影响其他工作区。
func (m *Manager) ReconnectTenant(tenantID string) {
	m.runnersMu.Lock()
	for id, r := range m.runners {
		if r.tenantID == tenantID {
			r.cancel()
			delete(m.runners, id)
		}
	}
	m.runnersMu.Unlock()
	if m.nc != nil && m.db != nil && strings.TrimSpace(tenantID) != "" {
		qCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var ids []string
		err := m.db.NewSelect().Model((*channel.Channel)(nil)).Column("id").
			Where("tenant_id = ? AND kind = ?", tenantID, channel.TelegramAccount).Scan(qCtx, &ids)
		cancel()
		if err != nil {
			slog.Warn("list tenant telegram accounts for reconnect broadcast failed", "tenant_id", tenantID, "error", err)
		} else {
			for _, id := range ids {
				m.publishAccountCtrl(id, ctrlRestart)
			}
		}
	}
	m.wake()
}

// credentials 读取工作区 tenantID 配置的 Telegram API ID、API Hash 与独立代理地址，未配置时返回 ErrNotConfigured。
func (m *Manager) credentials(ctx context.Context, tenantID string) (Credentials, error) {
	if m.tgCreds == nil || strings.TrimSpace(tenantID) == "" {
		return Credentials{}, ErrNotConfigured.New()
	}
	cfg, err := m.tgCreds.Tenant(tenantID).Get(ctx)
	if err != nil {
		return Credentials{}, errors.Wrap(err, "load tenant telegram credentials")
	}
	if !cfg.Configured() {
		return Credentials{}, ErrNotConfigured.New()
	}
	return cfg, nil
}

func mtprotoConfig(creds Credentials) MTProtoConfig {
	return MTProtoConfig{APIID: creds.APIID, APIHash: creds.APIHash, ProxyURL: creds.ProxyURL}
}

// Configured 报告工作区 tenantID 是否已配置 Telegram API 凭据。
func (m *Manager) Configured(ctx context.Context, tenantID string) (bool, error) {
	if m.tgCreds == nil || strings.TrimSpace(tenantID) == "" {
		return false, nil
	}
	cfg, err := m.tgCreds.Tenant(tenantID).Get(ctx)
	if err != nil {
		return false, errors.Wrap(err, "load tenant telegram credentials")
	}
	return cfg.Configured(), nil
}

// StartLogin 为成员 sub 启动一次交互式 Telegram 个人号登录（扫码或手机号验证码）。
// accountID 非空时原地重新授权对应个人号（跳过新建配额检查并保留原名称、同步白名单与策略）。
func (m *Manager) StartLogin(ctx context.Context, sub identity.Subject, mode LoginMode, name, phone, accountID string) (*LoginSessionDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	accountID = strings.TrimSpace(accountID)
	isReconnect := accountID != ""
	name = strings.TrimSpace(name)
	if isReconnect {
		existing, err := findOwnedAccount(ctx, m.db, sub.Tenant, sub.ID, accountID, false)
		if err != nil {
			return nil, err
		}
		if name == "" {
			name = existing.Name
		}
	}
	if m.checkQuota != nil {
		if err := m.checkQuota(ctx, sub, isReconnect); err != nil {
			return nil, err
		}
	}
	creds, err := m.credentials(ctx, sub.Tenant)
	if err != nil {
		return nil, err
	}
	if mode == "" {
		mode = LoginModeQR
	}
	phone = strings.TrimSpace(phone)
	if mode == LoginModePhone && phone == "" {
		return nil, ErrPhoneRequired.New()
	}

	var drv LoginDriver
	mc := mtprotoConfig(creds)
	if mode == LoginModePhone {
		drv, err = m.backend.StartPhoneLogin(ctx, mc, phone)
	} else {
		drv, err = m.backend.StartQRLogin(ctx, mc)
	}
	if err != nil {
		return nil, ErrLoginFailed.Wrap(err)
	}

	m.pruneExpiredLogins()
	entry := &loginEntry{
		id:              newID(),
		tenantID:        sub.Tenant,
		memberID:        sub.ID,
		targetAccountID: accountID,
		name:            name,
		mode:            mode,
		driver:          drv,
		createdAt:       time.Now().UTC(),
	}
	if m.nc != nil {
		subNATS, err := m.nc.Subscribe(natsLoginPrefix+entry.id, func(msg *nats.Msg) {
			go m.handleLoginRPC(entry, msg)
		})
		if err != nil {
			drv.Close()
			return nil, errors.Wrap(err, "subscribe tgaccount login nats subject")
		}
		entry.sub = subNATS
		_ = m.nc.Flush()
	}
	m.loginsMu.Lock()
	m.logins[entry.id] = entry
	m.loginsMu.Unlock()

	return m.buildLoginDTO(ctx, entry)
}

// GetLogin 查询进行中登录会话的当前状态；若扫码已完成则自动落库并启动长连接。
func (m *Manager) GetLogin(ctx context.Context, sub identity.Subject, loginID string) (*LoginSessionDTO, error) {
	entry, err := m.findLogin(sub, loginID)
	if errors.Is(err, ErrLoginNotFound) && m.nc != nil {
		return m.forwardLoginRPC(ctx, sub, loginID, loginRPCRequest{Op: "get"})
	}
	if err != nil {
		return nil, err
	}
	return m.buildLoginDTO(ctx, entry)
}

// SubmitCode 向手机号登录会话提交短信或 Telegram App 验证码。
func (m *Manager) SubmitCode(ctx context.Context, sub identity.Subject, loginID, code string) (*LoginSessionDTO, error) {
	entry, err := m.findLogin(sub, loginID)
	if errors.Is(err, ErrLoginNotFound) && m.nc != nil {
		return m.forwardLoginRPC(ctx, sub, loginID, loginRPCRequest{Op: "code", Code: code})
	}
	if err != nil {
		return nil, err
	}
	if _, err := entry.driver.SubmitCode(ctx, code); err != nil {
		return nil, err
	}
	return m.buildLoginDTO(ctx, entry)
}

// SubmitPassword 向需要两步验证的登录会话提交 2FA 云密码。
func (m *Manager) SubmitPassword(ctx context.Context, sub identity.Subject, loginID, password string) (*LoginSessionDTO, error) {
	entry, err := m.findLogin(sub, loginID)
	if errors.Is(err, ErrLoginNotFound) && m.nc != nil {
		return m.forwardLoginRPC(ctx, sub, loginID, loginRPCRequest{Op: "password", Password: password})
	}
	if err != nil {
		return nil, err
	}
	if _, err := entry.driver.SubmitPassword(ctx, password); err != nil {
		return nil, err
	}
	return m.buildLoginDTO(ctx, entry)
}

func (m *Manager) findLogin(sub identity.Subject, loginID string) (*loginEntry, error) {
	m.pruneExpiredLogins()
	m.loginsMu.Lock()
	entry := m.logins[loginID]
	m.loginsMu.Unlock()
	if entry == nil || entry.tenantID != sub.Tenant || entry.memberID != sub.ID {
		return nil, ErrLoginNotFound.New()
	}
	return entry, nil
}

func (m *Manager) pruneExpiredLogins() {
	cutoff := time.Now().UTC().Add(-15 * time.Minute)
	m.loginsMu.Lock()
	defer m.loginsMu.Unlock()
	for id, e := range m.logins {
		if e.createdAt.Before(cutoff) {
			e.close()
			delete(m.logins, id)
		}
	}
}

func (m *Manager) closeAllLogins() {
	m.loginsMu.Lock()
	defer m.loginsMu.Unlock()
	for id, e := range m.logins {
		e.close()
		delete(m.logins, id)
	}
}

func (m *Manager) buildLoginDTO(ctx context.Context, entry *loginEntry) (*LoginSessionDTO, error) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	st := entry.driver.State()
	dto := &LoginSessionDTO{
		LoginID:      entry.id,
		Mode:         entry.mode,
		Step:         st.Step,
		QRDataURL:    st.QRDataURL,
		QRExpiresAt:  st.QRExpiresAt,
		PasswordHint: st.PasswordHint,
		Error:        st.Error,
	}
	if st.Step == StepConnected {
		if !entry.persisted {
			acct, err := m.persistConnectedAccount(ctx, entry, st)
			if err != nil {
				return nil, err
			}
			entry.accountDTO = acct
			entry.persisted = true
			entry.driver.Close()
			m.stopRunner(acct.ID)
			m.publishAccountCtrl(acct.ID, ctrlRestart)
			m.wake()
		}
		dto.Account = entry.accountDTO
	}
	return dto, nil
}

// persistConnectedAccount 将登录成功的 Telegram 个人号加密持久化为成员个人商务渠道（指定 targetAccountID 或同号重新登录时原地刷新会话）。
func (m *Manager) persistConnectedAccount(ctx context.Context, entry *loginEntry, st LoginState) (*AccountDTO, error) {
	cipher := encryptSessionBytes(m.crypt, st.SessionData)
	displayName := st.Profile.DisplayName
	if displayName == "" {
		displayName = st.Profile.Username
	}
	name := entry.name
	if name == "" {
		if st.Profile.Username != "" {
			name = "Telegram (@" + st.Profile.Username + ")"
		} else if displayName != "" {
			name = "Telegram (" + displayName + ")"
		} else {
			name = "Telegram 个人号"
		}
	}

	var saved *channel.Channel
	err := database.RunInTx(ctx, m.db, nil, func(ctx context.Context, tx database.Tx) error {
		updateExisting := func(target *channel.Channel) error {
			cfg, err := settingsOf(target)
			if err != nil {
				return err
			}
			cfg.Phone = st.Profile.Phone
			if st.Profile.UserID != 0 {
				cfg.TelegramUserID = st.Profile.UserID
			}
			cfg.Username = st.Profile.Username
			cfg.DisplayName = displayName
			cfg.SessionCipher = cipher
			cfg.Status = StatusConnected
			cfg.LastError = ""
			sm, err := cfg.toMap()
			if err != nil {
				return err
			}
			if entry.name != "" {
				target.Name = entry.name
			}
			target.Enabled = true
			target.Settings = sm
			if _, err := tx.NewUpdate().Model(target).Column("name", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update reconnected telegram account")
			}
			saved = target
			return nil
		}

		// 1. 若指定了原地重新授权的目标账号 ID，优先更新该渠道行
		if entry.targetAccountID != "" {
			target, err := findOwnedAccount(ctx, tx, entry.tenantID, entry.memberID, entry.targetAccountID, true)
			if err != nil {
				return err
			}
			return updateExisting(target)
		}

		// 2. 若该成员在当前工作区已绑定同一个 Telegram UserID，则原地更新加密会话与状态
		if st.Profile.UserID != 0 {
			var existing []channel.Channel
			if err := tx.NewSelect().Model(&existing).
				Where("tenant_id = ? AND kind = ? AND owner_member_id = ?", entry.tenantID, channel.TelegramAccount, entry.memberID).
				For("UPDATE").Scan(ctx); err != nil {
				return errors.Wrap(err, "find existing telegram account")
			}
			for i := range existing {
				cfg, err := settingsOf(&existing[i])
				if err != nil {
					return err
				}
				if cfg.TelegramUserID == st.Profile.UserID {
					return updateExisting(&existing[i])
				}
			}
		}

		syncDef := DefaultSyncConfig()
		cfg := Settings{
			Phone:          st.Profile.Phone,
			TelegramUserID: st.Profile.UserID,
			Username:       st.Profile.Username,
			DisplayName:    displayName,
			SessionCipher:  cipher,
			Status:         StatusConnected,
			SyncedPeers:    []SyncedPeer{},
			Sync:           &syncDef,
		}
		sm, err := cfg.toMap()
		if err != nil {
			return err
		}
		ownerID := entry.memberID
		ch := &channel.Channel{
			ID:            newID(),
			TenantID:      entry.tenantID,
			Kind:          channel.TelegramAccount,
			Audience:      channel.AudiencePartner,
			Name:          name,
			Settings:      sm,
			OwnerMemberID: &ownerID,
			Enabled:       true,
		}
		if _, err := tx.NewInsert().Model(ch).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert telegram account channel")
		}
		saved = ch
		return nil
	})
	if err != nil {
		return nil, err
	}
	dto, err := toAccountDTO(saved)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// ListAccounts 返回成员 sub 在当前工作区绑定的全部 Telegram 个人号。
func (m *Manager) ListAccounts(ctx context.Context, sub identity.Subject) ([]AccountDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	var rows []channel.Channel
	err := m.db.NewSelect().Model(&rows).
		Where("tenant_id = ? AND kind = ? AND owner_member_id = ?", sub.Tenant, channel.TelegramAccount, sub.ID).
		OrderExpr("created_at ASC, id ASC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list telegram accounts")
	}
	out := make([]AccountDTO, len(rows))
	for i := range rows {
		dto, err := toAccountDTO(&rows[i])
		if err != nil {
			return nil, err
		}
		out[i] = dto
	}
	return out, nil
}

// GetAccount 返回成员 sub 拥有的单个 Telegram 个人号完整配置，供个人号配置工作台加载。
func (m *Manager) GetAccount(ctx context.Context, sub identity.Subject, id string) (*AccountDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	ch, err := findOwnedAccount(ctx, m.db, sub.Tenant, sub.ID, id, false)
	if err != nil {
		return nil, err
	}
	dto, err := toAccountDTO(ch)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// UpdateAccount 修改成员个人 Telegram 账号的名称、启用状态、同步策略与通用商务策略，并立即刷新常驻连接器。
func (m *Manager) UpdateAccount(ctx context.Context, sub identity.Subject, id string, params UpdateAccountParams) (*AccountDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	var updated *channel.Channel
	var resolvedSync SyncConfig
	err := database.RunInTx(ctx, m.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findOwnedAccount(ctx, tx, sub.Tenant, sub.ID, id, true)
		if err != nil {
			return err
		}
		cfg, err := settingsOf(ch)
		if err != nil {
			return err
		}
		if !params.Enabled {
			cfg.Status = StatusDisconnected
		} else if cfg.SessionCipher != "" && cfg.Status == StatusDisconnected {
			cfg.Status = StatusConnected
			cfg.LastError = ""
		}
		if params.Sync != nil {
			norm := NormalizeSyncConfig(*params.Sync)
			cfg.Sync = &norm
		}
		if params.Business != nil {
			bp, err := channel.NormalizeBusinessPolicy(*params.Business)
			if err != nil {
				return err
			}
			cfg.Business = bp
		}
		resolvedSync = cfg.resolvedSync()
		sm, err := cfg.toMap()
		if err != nil {
			return err
		}
		if trimmed := strings.TrimSpace(params.Name); trimmed != "" {
			ch.Name = trimmed
		}
		ch.Enabled = params.Enabled
		ch.Settings = sm
		if _, err := tx.NewUpdate().Model(ch).Column("name", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update telegram account")
		}
		updated = ch
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 立即更新内存中的同步策略并广播控制信号给长连接持有实例
	m.runnersMu.RLock()
	if r := m.runners[id]; r != nil {
		r.setSync(resolvedSync)
	}
	m.runnersMu.RUnlock()

	// 若开启了按文件夹同步且选定了文件夹，尽力立即将文件夹内的会话合并进同步列表
	if params.Sync != nil && resolvedSync.Mode == SyncModeFolders && len(resolvedSync.FolderIDs) > 0 && updated.Enabled {
		if synced, syncErr := m.syncFolderPeers(ctx, updated, resolvedSync); syncErr != nil {
			slog.WarnContext(ctx, "sync telegram folder peers on update failed", "channel_id", id, "error", syncErr)
		} else if synced != nil {
			updated = synced
		}
	}

	if updated.Enabled {
		m.publishAccountCtrl(id, ctrlReload)
	} else {
		m.publishAccountCtrl(id, ctrlDisconnect)
	}
	m.wake()
	dto, err := toAccountDTO(updated)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// syncFolderPeers 拉取选中的 Telegram 文件夹中的对话并合并到 SyncedPeers（保留已有会话的暂停标记）。
func (m *Manager) syncFolderPeers(ctx context.Context, ch *channel.Channel, syncCfg SyncConfig) (*channel.Channel, error) {
	cfg, err := settingsOf(ch)
	if err != nil || cfg.SessionCipher == "" {
		return nil, err
	}
	folders, err := m.fetchFolders(ctx, ch)
	if err != nil {
		return nil, err
	}
	wanted := make(map[int]bool, len(syncCfg.FolderIDs))
	for _, fid := range syncCfg.FolderIDs {
		wanted[fid] = true
	}
	existingPaused := make(map[string]bool, len(cfg.SyncedPeers))
	for _, p := range cfg.SyncedPeers {
		existingPaused[p.ExternalChatID] = p.Paused
	}
	seen := map[string]bool{}
	merged := make([]SyncedPeer, 0)
	for _, f := range folders {
		if !wanted[f.ID] {
			continue
		}
		for _, p := range f.Peers {
			if seen[p.ExternalChatID] {
				continue
			}
			seen[p.ExternalChatID] = true
			p.Paused = existingPaused[p.ExternalChatID]
			merged = append(merged, p)
		}
	}
	var saved *channel.Channel
	err = database.RunInTx(ctx, m.db, nil, func(ctx context.Context, tx database.Tx) error {
		row, err := channel.Find(ctx, tx, ch.TenantID, ch.ID)
		if err != nil {
			return err
		}
		cur, err := settingsOf(row)
		if err != nil {
			return err
		}
		cur.SyncedPeers = merged
		sm, err := cur.toMap()
		if err != nil {
			return err
		}
		row.Settings = sm
		if _, err := tx.NewUpdate().Model(row).Column("settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save folder synced peers")
		}
		saved = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	m.runnersMu.RLock()
	if r := m.runners[ch.ID]; r != nil {
		r.setPeers(merged)
	}
	m.runnersMu.RUnlock()
	m.publishAccountCtrl(ch.ID, ctrlReload)
	return saved, nil
}

// ReconnectAccount 清除错误状态并强制重建成员个人 Telegram 账号的 MTProto 长连接。
func (m *Manager) ReconnectAccount(ctx context.Context, sub identity.Subject, id string) (*AccountDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	var updated *channel.Channel
	err := database.RunInTx(ctx, m.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findOwnedAccount(ctx, tx, sub.Tenant, sub.ID, id, true)
		if err != nil {
			return err
		}
		cfg, err := settingsOf(ch)
		if err != nil {
			return err
		}
		if cfg.SessionCipher == "" {
			return ErrNotConnected.New()
		}
		cfg.Status = StatusDisconnected
		cfg.LastError = ""
		sm, err := cfg.toMap()
		if err != nil {
			return err
		}
		ch.Enabled = true
		ch.Settings = sm
		if _, err := tx.NewUpdate().Model(ch).Column("enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "reconnect telegram account")
		}
		updated = ch
		return nil
	})
	if err != nil {
		return nil, err
	}
	m.stopRunner(id)
	m.publishAccountCtrl(id, ctrlRestart)
	m.wake()
	slog.InfoContext(ctx, "telegram personal account manual reconnect requested", "channel_id", id)
	dto, err := toAccountDTO(updated)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// DisconnectAccount 断开并清除成员个人 Telegram 账号的加密会话凭据；若尚未产生历史聊天则直接删除渠道行。
func (m *Manager) DisconnectAccount(ctx context.Context, sub identity.Subject, id string) error {
	if sub.Type != member.Type {
		return channel.ErrNotFound.New()
	}
	err := database.RunInTx(ctx, m.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findOwnedAccount(ctx, tx, sub.Tenant, sub.ID, id, true)
		if err != nil {
			return err
		}
		hasRef, err := tx.NewSelect().Table("conversations").
			Where("tenant_id = ? AND (channel_id = ? OR reply_channel_id = ?)", sub.Tenant, id, id).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check telegram account conversations")
		}
		if !hasRef {
			hasIdent, err := tx.NewSelect().Table("contact_identities").
				Where("tenant_id = ? AND channel_id = ?", sub.Tenant, id).Exists(ctx)
			if err != nil {
				return errors.Wrap(err, "check telegram account identities")
			}
			hasRef = hasIdent
		}
		if !hasRef {
			_, err = tx.NewDelete().Model((*channel.Channel)(nil)).Where("id = ? AND tenant_id = ?", id, sub.Tenant).Exec(ctx)
			return errors.Wrap(err, "delete unused telegram account")
		}
		cfg, err := settingsOf(ch)
		if err != nil {
			return err
		}
		cfg.SessionCipher = ""
		cfg.Status = StatusDisconnected
		cfg.LastError = ""
		sm, err := cfg.toMap()
		if err != nil {
			return err
		}
		ch.Enabled = false
		ch.Settings = sm
		_, err = tx.NewUpdate().Model(ch).Column("enabled", "settings", "updated_at").WherePK().Exec(ctx)
		return errors.Wrap(err, "disconnect telegram account")
	})
	if err != nil {
		return err
	}
	m.stopRunner(id)
	m.publishAccountCtrl(id, ctrlDisconnect)
	m.wake()
	return nil
}

// ListDialogs 读取已绑定 Telegram 个人号当前最近的 1:1 单聊与群聊候选列表（分页最多 300 条），并标出哪些已在同步白名单中。
func (m *Manager) ListDialogs(ctx context.Context, sub identity.Subject, id string) ([]DialogDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	ch, err := findOwnedAccount(ctx, m.db, sub.Tenant, sub.ID, id, false)
	if err != nil {
		return nil, err
	}
	cfg, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	if cfg.SessionCipher == "" {
		return nil, ErrNotConnected.New()
	}
	syncedSet := make(map[string]bool, len(cfg.SyncedPeers))
	for _, p := range cfg.SyncedPeers {
		syncedSet[p.ExternalChatID] = true
	}

	dialogs, err := m.fetchDialogs(ctx, ch, 300)
	if err != nil {
		return nil, err
	}
	for i := range dialogs {
		dialogs[i].Synced = syncedSet[dialogs[i].ExternalChatID]
	}
	return dialogs, nil
}

// ListFolders 读取已绑定 Telegram 个人号的客户端文件夹（Dialog Filter）列表，并标出哪些已勾选自动同步。
func (m *Manager) ListFolders(ctx context.Context, sub identity.Subject, id string) ([]FolderDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	ch, err := findOwnedAccount(ctx, m.db, sub.Tenant, sub.ID, id, false)
	if err != nil {
		return nil, err
	}
	cfg, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	if cfg.SessionCipher == "" {
		return nil, ErrNotConnected.New()
	}
	selected := map[int]bool{}
	for _, fid := range cfg.resolvedSync().FolderIDs {
		selected[fid] = true
	}
	folders, err := m.fetchFolders(ctx, ch)
	if err != nil {
		return nil, err
	}
	for i := range folders {
		folders[i].Synced = selected[folders[i].ID]
		if folders[i].Peers == nil {
			folders[i].Peers = []SyncedPeer{}
		}
	}
	return folders, nil
}

// UpdateSyncedPeers 更新成员勾选同步的 Telegram 1:1 单聊与群聊白名单（含暂停状态），并可选对新增会话拉取最近 N 条历史消息。
func (m *Manager) UpdateSyncedPeers(ctx context.Context, sub identity.Subject, id string, peers []SyncedPeer, backfillLimit int) (*AccountDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	if backfillLimit < 0 {
		backfillLimit = 0
	}
	if backfillLimit > 50 {
		backfillLimit = 50
	}

	// 去重并规范化外部会话键
	seen := map[string]bool{}
	clean := make([]SyncedPeer, 0, len(peers))
	for _, p := range peers {
		if p.PeerType == "" || p.PeerID == 0 {
			continue
		}
		p.ExternalChatID = formatPeerKey(p.PeerType, p.PeerID)
		if seen[p.ExternalChatID] {
			continue
		}
		seen[p.ExternalChatID] = true
		p.IsGroup = p.PeerType == "chat" || p.PeerType == "channel"
		clean = append(clean, p)
	}

	var updated *channel.Channel
	var added []SyncedPeer
	var syncCfg SyncConfig
	err := database.RunInTx(ctx, m.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findOwnedAccount(ctx, tx, sub.Tenant, sub.ID, id, true)
		if err != nil {
			return err
		}
		cfg, err := settingsOf(ch)
		if err != nil {
			return err
		}
		prev := make(map[string]bool, len(cfg.SyncedPeers))
		for _, p := range cfg.SyncedPeers {
			prev[p.ExternalChatID] = true
		}
		for _, p := range clean {
			if !prev[p.ExternalChatID] {
				added = append(added, p)
			}
		}
		cfg.SyncedPeers = clean
		syncCfg = cfg.resolvedSync()
		sm, err := cfg.toMap()
		if err != nil {
			return err
		}
		ch.Settings = sm
		if _, err := tx.NewUpdate().Model(ch).Column("settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update telegram synced peers")
		}
		updated = ch
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 立即更新内存中的同步白名单并广播到持有长连接的实例
	m.runnersMu.RLock()
	if r := m.runners[id]; r != nil {
		r.setPeers(clean)
	}
	m.runnersMu.RUnlock()
	m.publishAccountCtrl(id, ctrlReload)

	// 对新勾选的会话按需拉取最近历史消息（单条失败记警告日志，不阻塞配置保存）
	if backfillLimit > 0 && len(added) > 0 && m.receive != nil {
		if err := m.backfillPeers(ctx, updated, syncCfg, added, backfillLimit); err != nil {
			slog.WarnContext(ctx, "backfill telegram synced peers failed", "channel_id", id, "error", err)
		}
	}

	dto, err := toAccountDTO(updated)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// BackfillPeer 为单个已同步会话手动拉取最近 limit 条历史消息并返回实际新增入库的条数。
func (m *Manager) BackfillPeer(ctx context.Context, sub identity.Subject, id, externalChatID string, limit int) (int, error) {
	if sub.Type != member.Type {
		return 0, channel.ErrNotFound.New()
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	ch, err := findOwnedAccount(ctx, m.db, sub.Tenant, sub.ID, id, false)
	if err != nil {
		return 0, err
	}
	cfg, err := settingsOf(ch)
	if err != nil {
		return 0, err
	}
	if cfg.SessionCipher == "" {
		return 0, ErrNotConnected.New()
	}
	var target *SyncedPeer
	for i := range cfg.SyncedPeers {
		if cfg.SyncedPeers[i].ExternalChatID == externalChatID {
			target = &cfg.SyncedPeers[i]
			break
		}
	}
	if target == nil {
		return 0, ErrPeerNotFound.New()
	}
	if m.receive == nil {
		return 0, nil
	}
	syncCfg := cfg.resolvedSync()
	msgs, err := m.fetchHistory(ctx, ch, *target, limit)
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, msg := range msgs {
		added, err := m.ingestMessage(ctx, ch, syncCfg, nil, *target, msg, true)
		if err != nil {
			slog.WarnContext(ctx, "manual backfill telegram message failed", "channel_id", ch.ID, "peer", target.ExternalChatID, "error", err)
			continue
		}
		if added {
			imported++
		}
	}
	return imported, nil
}

func (m *Manager) backfillPeers(ctx context.Context, ch *channel.Channel, syncCfg SyncConfig, peers []SyncedPeer, limit int) error {
	for _, peer := range peers {
		msgs, err := m.fetchHistory(ctx, ch, peer, limit)
		if err != nil {
			slog.WarnContext(ctx, "fetch telegram peer history failed", "channel_id", ch.ID, "peer", peer.ExternalChatID, "error", err)
			continue
		}
		for _, msg := range msgs {
			if _, err := m.ingestMessage(ctx, ch, syncCfg, nil, peer, msg, true); err != nil {
				slog.WarnContext(ctx, "ingest telegram history message failed", "channel_id", ch.ID, "peer", peer.ExternalChatID, "error", err)
			}
		}
	}
	return nil
}

func (m *Manager) localClient(channelID string) LiveClient {
	m.runnersMu.RLock()
	r := m.runners[channelID]
	m.runnersMu.RUnlock()
	if r == nil {
		return nil
	}
	return r.getClient()
}

// withOneShotClient 仅在无 NATS 的单进程场景下使用已加密会话临时连接 Telegram。
func (m *Manager) withOneShotClient(ctx context.Context, ch *channel.Channel, fn func(context.Context, LiveClient) error) error {
	creds, err := m.credentials(ctx, ch.TenantID)
	if err != nil {
		return err
	}
	storage := &dbSessionStorage{db: m.db, crypt: m.crypt, tenantID: ch.TenantID, channelID: ch.ID}
	return m.backend.RunOneShot(ctx, mtprotoConfig(creds), storage, fn)
}

func (m *Manager) fetchDialogs(ctx context.Context, ch *channel.Channel, limit int) ([]DialogDTO, error) {
	if cli := m.localClient(ch.ID); cli != nil {
		return cli.ListDialogs(ctx, limit)
	}
	if m.nc != nil {
		resp, err := m.requestAccountRPC(ctx, ch.ID, accountRPCRequest{Op: "list_dialogs", Limit: limit}, rpcCallTimeout)
		if err != nil {
			return nil, err
		}
		return resp.Dialogs, nil
	}
	var out []DialogDTO
	err := m.withOneShotClient(ctx, ch, func(ctx context.Context, cli LiveClient) error {
		var err error
		out, err = cli.ListDialogs(ctx, limit)
		return err
	})
	return out, err
}

func (m *Manager) fetchFolders(ctx context.Context, ch *channel.Channel) ([]FolderDTO, error) {
	if cli := m.localClient(ch.ID); cli != nil {
		return cli.ListFolders(ctx)
	}
	if m.nc != nil {
		resp, err := m.requestAccountRPC(ctx, ch.ID, accountRPCRequest{Op: "list_folders"}, rpcCallTimeout)
		if err != nil {
			return nil, err
		}
		return resp.Folders, nil
	}
	var out []FolderDTO
	err := m.withOneShotClient(ctx, ch, func(ctx context.Context, cli LiveClient) error {
		var err error
		out, err = cli.ListFolders(ctx)
		return err
	})
	return out, err
}

func (m *Manager) fetchHistory(ctx context.Context, ch *channel.Channel, peer SyncedPeer, limit int) ([]IncomingMessage, error) {
	if cli := m.localClient(ch.ID); cli != nil {
		return cli.FetchHistory(ctx, peer, limit)
	}
	if m.nc != nil {
		resp, err := m.requestAccountRPC(ctx, ch.ID, accountRPCRequest{Op: "fetch_history", Peer: peer, Limit: limit}, rpcCallTimeout)
		if err != nil {
			return nil, err
		}
		return resp.History, nil
	}
	var out []IncomingMessage
	err := m.withOneShotClient(ctx, ch, func(ctx context.Context, cli LiveClient) error {
		var err error
		out, err = cli.FetchHistory(ctx, peer, limit)
		return err
	})
	return out, err
}

// Run 常驻同步循环：按周期或变更通知自动为每个启用且已授权的 Telegram 个人号维护 MTProto 长连接。
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer m.stopAllRunners()
	defer m.closeAllLogins()

	m.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.reconcile(ctx)
		case <-m.wakeCh:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	if m.tgCreds == nil {
		m.stopAllRunners()
		return
	}
	var rows []channel.Channel
	err := m.db.NewSelect().Model(&rows).
		Where("kind = ? AND enabled = TRUE AND owner_member_id IS NOT NULL", channel.TelegramAccount).
		Where("EXISTS (SELECT 1 FROM tenants t WHERE t.id = channel.tenant_id AND t.disabled_at IS NULL)").
		Scan(ctx)
	if err != nil {
		slog.WarnContext(ctx, "load active telegram accounts for sync", "error", err)
		return
	}

	// 按工作区独立加载 MTProto 凭据与代理，单个工作区未配置或读取失败不影响其他工作区
	tenantCreds := map[string]MTProtoConfig{}
	tenantLoaded := map[string]bool{}
	desired := map[string]channel.Channel{}
	for _, ch := range rows {
		cfg, err := settingsOf(&ch)
		if err != nil || cfg.SessionCipher == "" || cfg.Status == StatusAuthExpired {
			continue
		}
		if !tenantLoaded[ch.TenantID] {
			tenantLoaded[ch.TenantID] = true
			creds, err := m.credentials(ctx, ch.TenantID)
			if err == nil {
				tenantCreds[ch.TenantID] = mtprotoConfig(creds)
			} else if !errors.Is(err, ErrNotConfigured) {
				slog.WarnContext(ctx, "load tenant telegram credentials during reconcile", "tenant_id", ch.TenantID, "error", err)
			}
		}
		if _, ok := tenantCreds[ch.TenantID]; !ok {
			continue
		}
		desired[ch.ID] = ch
	}

	// 停止已停用、已删除、授权失效、凭据已清空或凭据/代理发生变更的连接
	m.runnersMu.Lock()
	for id, r := range m.runners {
		ch, ok := desired[id]
		if !ok || r.creds != tenantCreds[ch.TenantID] {
			r.cancel()
			delete(m.runners, id)
		}
	}
	for id, ch := range desired {
		cfg, _ := settingsOf(&ch)
		mc := tenantCreds[ch.TenantID]
		if existing := m.runners[id]; existing != nil {
			existing.setPeers(cfg.SyncedPeers)
			existing.setSync(cfg.resolvedSync())
			continue
		}
		rCtx, cancel := context.WithCancel(ctx)
		runner := &accountRunner{channelID: id, tenantID: ch.TenantID, creds: mc, cancel: cancel}
		runner.setPeers(cfg.SyncedPeers)
		runner.setSync(cfg.resolvedSync())
		m.runners[id] = runner
		chCopy := ch
		go m.runLoop(rCtx, mc, &chCopy, runner)
	}
	m.runnersMu.Unlock()
}

func (m *Manager) runLoop(ctx context.Context, mc MTProtoConfig, ch *channel.Channel, runner *accountRunner) {
	backoff := 3 * time.Second
	storage := &dbSessionStorage{db: m.db, crypt: m.crypt, tenantID: ch.TenantID, channelID: ch.ID}
	for {
		if ctx.Err() != nil {
			return
		}
		runOnce := func(runCtx context.Context) error {
			runCtx, cancelRun := context.WithCancel(runCtx)
			defer cancelRun()

			var restartRequested atomic.Bool
			var subsMu sync.Mutex
			var subs []*nats.Subscription
			cleanupSubs := func() {
				subsMu.Lock()
				list := subs
				subs = nil
				subsMu.Unlock()
				for _, s := range list {
					_ = s.Unsubscribe()
				}
			}
			defer cleanupSubs()

			runErr := m.backend.RunAccount(runCtx, mc, storage,
				func(msgCtx context.Context, msg IncomingMessage) error {
					peer, ok := runner.lookupPeer(msg.ExternalChatID)
					if !ok {
						// 未在同步白名单中的个人隐私聊天直接忽略
						return nil
					}
					_, ingestErr := m.ingestMessage(msgCtx, ch, runner.getSync(), runner, peer, msg, false)
					return ingestErr
				},
				func(cli LiveClient) {
					runner.setClient(cli)
					backoff = 3 * time.Second
					if m.nc != nil {
						if rpcSub, subErr := m.nc.Subscribe(natsRPCPrefix+ch.ID, func(msg *nats.Msg) {
							go m.handleAccountRPC(runCtx, ch, cli, msg)
						}); subErr != nil {
							slog.WarnContext(runCtx, "subscribe tgaccount nats rpc failed", "channel_id", ch.ID, "error", subErr)
						} else {
							subsMu.Lock()
							subs = append(subs, rpcSub)
							subsMu.Unlock()
						}
						if ctrlSub, subErr := m.nc.Subscribe(natsCtrlPrefix+ch.ID, func(msg *nats.Msg) {
							m.handleAccountCtrl(runCtx, ch.TenantID, ch.ID, runner, &restartRequested, cancelRun, msg.Data)
						}); subErr != nil {
							slog.WarnContext(runCtx, "subscribe tgaccount nats ctrl failed", "channel_id", ch.ID, "error", subErr)
						} else {
							subsMu.Lock()
							subs = append(subs, ctrlSub)
							subsMu.Unlock()
						}
						_ = m.nc.Flush()
					}
					m.recordStatus(runCtx, ch.TenantID, ch.ID, StatusConnected, "")
					slog.InfoContext(runCtx, "telegram personal account connected", "channel_id", ch.ID)
				},
			)
			if restartRequested.Load() && ctx.Err() == nil {
				return errRestartRequested
			}
			return runErr
		}
		var err error
		if m.locker != nil {
			err = m.locker.Hold(ctx, "tgaccount:conn:"+ch.ID, 45*time.Second, runOnce)
		} else {
			err = runOnce(ctx)
		}
		runner.setClient(nil)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, lock.ErrNotAcquired) {
			// 其他实例持有该账号的连接租约，静默退避后重试抢占，不覆盖在线状态
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}
		if errors.Is(err, lock.ErrNotHeld) {
			// 本实例持有的连接租约过期丢失，不再写回 disconnected 以免覆盖新接管实例状态
			slog.WarnContext(ctx, "telegram personal account lease lost", "channel_id", ch.ID, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if errors.Is(err, errRestartRequested) {
			slog.InfoContext(ctx, "telegram personal account restart requested", "channel_id", ch.ID)
			m.runnersMu.Lock()
			if m.runners[ch.ID] == runner {
				delete(m.runners, ch.ID)
			}
			m.runnersMu.Unlock()
			m.wake()
			return
		}
		if errors.Is(err, ErrNotConnected) {
			m.recordStatus(context.Background(), ch.TenantID, ch.ID, StatusAuthExpired, err.Error())
			slog.WarnContext(ctx, "telegram personal account auth expired", "channel_id", ch.ID)
			return
		}
		slog.WarnContext(ctx, "telegram personal account disconnected, reconnecting", "channel_id", ch.ID, "backoff", backoff, "error", err)
		m.recordStatus(context.Background(), ch.TenantID, ch.ID, StatusDisconnected, err.Error())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			if backoff < time.Minute {
				backoff *= 2
			}
		}
	}
}

func (m *Manager) recordStatus(ctx context.Context, tenantID, channelID string, status AccountStatus, lastErr string) {
	_, err := m.db.NewRaw(`UPDATE channels
		SET settings = jsonb_set(jsonb_set(settings, '{status}', to_jsonb(?::text), true), '{last_error}', to_jsonb(?::text), true),
		    updated_at = ?
		WHERE id = ? AND tenant_id = ? AND enabled = TRUE AND COALESCE(settings->>'session_cipher', '') <> ''`,
		string(status), lastErr, time.Now().UTC(), channelID, tenantID).Exec(ctx)
	if err != nil {
		slog.WarnContext(ctx, "update telegram account status", "channel_id", channelID, "error", err)
	}
}

// touchLastSynced 节流刷新渠道 settings.last_synced_at，展示最近同步活跃时间。
func (m *Manager) touchLastSynced(ctx context.Context, tenantID, channelID string, runner *accountRunner) {
	at := time.Now().UTC()
	if !runner.shouldTouchSync(at) {
		return
	}
	_, err := m.db.NewRaw(`UPDATE channels
		SET settings = jsonb_set(settings, '{last_synced_at}', to_jsonb(?::text), true)
		WHERE id = ? AND tenant_id = ?`,
		at.Format(time.RFC3339Nano), channelID, tenantID).Exec(ctx)
	if err != nil {
		slog.WarnContext(ctx, "update telegram account last_synced_at", "channel_id", channelID, "error", err)
	}
}

func (m *Manager) stopRunner(channelID string) {
	m.runnersMu.Lock()
	if r := m.runners[channelID]; r != nil {
		r.cancel()
		delete(m.runners, channelID)
	}
	m.runnersMu.Unlock()
}

func (m *Manager) stopAllRunners() {
	m.runnersMu.Lock()
	for id, r := range m.runners {
		r.cancel()
		delete(m.runners, id)
	}
	m.runnersMu.Unlock()
}

// ingestMessage 按同步策略将白名单内的一条 Telegram 单聊或群聊消息写入外部商务聊天，并过滤暂停会话、未提及群消息及本平台回声。
// 返回值为 true 表示成功写入了一条新消息（非去重或过滤）。
func (m *Manager) ingestMessage(ctx context.Context, ch *channel.Channel, syncCfg SyncConfig, runner *accountRunner, peer SyncedPeer, msg IncomingMessage, manualBackfill bool) (bool, error) {
	if m.receive == nil {
		return false, nil
	}
	if peer.Paused && !manualBackfill {
		return false, nil
	}
	if msg.FromOwner && !syncCfg.SyncOutgoing {
		return false, nil
	}
	if peer.IsGroup && syncCfg.GroupMode == GroupSyncMentionsOnly && !msg.FromOwner && !msg.Mentioned {
		return false, nil
	}

	var incomingAtts []conversation.IncomingAttachment
	if syncCfg.SyncMedia && len(msg.Media) > 0 {
		incomingAtts = make([]conversation.IncomingAttachment, 0, len(msg.Media))
		for _, item := range msg.Media {
			if item.SourceRef == "" {
				continue
			}
			incomingAtts = append(incomingAtts, conversation.IncomingAttachment{
				Kind:        item.Kind,
				Filename:    item.Filename,
				ContentType: item.ContentType,
				Size:        item.Size,
				Width:       item.Width,
				Height:      item.Height,
				DurationMs:  item.DurationMs,
				IsVoice:     item.IsVoice,
				SourceRef:   item.SourceRef,
				AutoFetch:   item.AutoFetch,
			})
		}
	}
	if strings.TrimSpace(msg.Text) == "" && len(incomingAtts) == 0 {
		return false, nil
	}

	extID := formatExternalMessageID(peer.ExternalChatID, msg.MessageID)
	// 若本平台刚通过 Connector.Send 发出该消息，message_deliveries 已记录相同 external_id，跳过回声避免重复
	if msg.FromOwner {
		alreadySent, err := m.db.NewSelect().TableExpr("message_deliveries AS d").
			Join("JOIN timeline_items AS ti ON ti.id = d.item_id").
			Where("ti.tenant_id = ? AND ti.channel_id = ? AND d.external_id = ?", ch.TenantID, ch.ID, extID).
			Exists(ctx)
		if err != nil {
			return false, errors.Wrap(err, "check outgoing telegram echo")
		}
		if alreadySent {
			return false, nil
		}
	}
	from := msg.SenderID
	fromName := msg.SenderName
	if !peer.IsGroup {
		// 1:1 单聊无论收发均锚定对方联系人
		from = peer.ExternalChatID
		if fromName == "" {
			fromName = peer.Title
		}
	} else if from == "" && !msg.FromOwner {
		from = peer.ExternalChatID + ":unknown"
		fromName = peer.Title
	}
	createdAt := msg.CreatedAt
	sys := identity.Subject{Type: identity.System, ID: "tgaccount:" + ch.ID, Tenant: ch.TenantID}
	rec, err := m.receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:           ch.ID,
		From:                from,
		FromName:            fromName,
		ExternalID:          extID,
		ExternalChatID:      peer.ExternalChatID,
		IsGroup:             peer.IsGroup,
		ChatTitle:           peer.Title,
		FromOwner:           msg.FromOwner,
		CreatedAt:           &createdAt,
		Text:                msg.Text,
		IncomingAttachments: incomingAtts,
	})
	if err != nil {
		return false, err
	}
	if !rec.Duplicate {
		m.touchLastSynced(ctx, ch.TenantID, ch.ID, runner)
	}
	return !rec.Duplicate, nil
}

// Connector 返回实现 channel.Connector 与 channel.MediaFetcher 的 Telegram 个人号连接器。
func (m *Manager) Connector() channel.Connector { return connector{mgr: m} }

type connector struct {
	mgr *Manager
}

var _ channel.MediaFetcher = connector{}

func (connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		Transport:      channel.TransportLongConnection,
		MessageTypes:   []string{"text", "image", "video", "audio", "file"},
		SupportsDirect: true,
		SupportsGroup:  true,
		ReadReceipts:   false,
	}
}

// FetchMedia 实现 channel.MediaFetcher：优先通过本地或跨实例 NATS RPC 拉取媒体；无 NATS 时在连接保持到流关闭的前提下临时拉取。
func (c connector) FetchMedia(ctx context.Context, ch *channel.Channel, ref string, maxBytes int64) (*channel.FetchedMedia, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, channel.ErrMediaUnavailable.New()
	}
	cfg, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	if cfg.SessionCipher == "" {
		return nil, ErrNotConnected.New()
	}
	if cli := c.mgr.localClient(ch.ID); cli != nil {
		return cli.DownloadMedia(ctx, ref, maxBytes)
	}
	if c.mgr.nc != nil {
		return c.mgr.rpcFetchMedia(ctx, ch, ref, maxBytes)
	}
	return c.fetchMediaOneShot(ctx, ch, ref, maxBytes)
}

func (c connector) fetchMediaOneShot(ctx context.Context, ch *channel.Channel, ref string, maxBytes int64) (*channel.FetchedMedia, error) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	readyCh := make(chan *channel.FetchedMedia, 1)
	errCh := make(chan error, 1)
	doneCh := make(chan struct{})
	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			close(doneCh)
			cancel()
		})
	}

	go func() {
		err := c.mgr.withOneShotClient(runCtx, ch, func(fnCtx context.Context, cli LiveClient) error {
			fetched, err := cli.DownloadMedia(fnCtx, ref, maxBytes)
			if err != nil {
				return err
			}
			origBody := fetched.Body
			fetched.Body = &cleanupReadCloser{
				ReadCloser: origBody,
				onClose:    finish,
			}
			select {
			case readyCh <- fetched:
			case <-fnCtx.Done():
				_ = origBody.Close()
				return fnCtx.Err()
			}
			select {
			case <-doneCh:
				return nil
			case <-runCtx.Done():
				return runCtx.Err()
			}
		})
		if err != nil {
			errCh <- err
		}
	}()

	select {
	case fetched := <-readyCh:
		return fetched, nil
	case err := <-errCh:
		finish()
		return nil, err
	case <-ctx.Done():
		finish()
		return nil, ctx.Err()
	}
}

// Send 将时间线中的商务回复（文字及可选附件）经成员绑定的 Telegram 个人号发往目标 1:1 单聊或外部群聊。
func (c connector) Send(ctx context.Context, m channel.Outgoing) (string, bool, error) {
	if strings.TrimSpace(m.ExternalTo) == "" {
		return "", false, errors.New("tgaccount: empty external recipient")
	}
	cfg, err := settingsOf(m.Channel)
	if err != nil {
		return "", false, err
	}
	if cfg.SessionCipher == "" {
		return "", false, ErrNotConnected.New()
	}
	var target *SyncedPeer
	for i := range cfg.SyncedPeers {
		if cfg.SyncedPeers[i].ExternalChatID == m.ExternalTo {
			target = &cfg.SyncedPeers[i]
			break
		}
	}
	if target == nil {
		return "", false, ErrPeerNotFound.New()
	}

	var lastMsgID int
	switch {
	case c.mgr.localClient(m.Channel.ID) != nil:
		lastMsgID, err = c.sendWithClient(ctx, c.mgr.localClient(m.Channel.ID), *target, m)
	case c.mgr.nc != nil:
		lastMsgID, err = c.sendViaRPC(ctx, m.Channel.ID, *target, m)
	default:
		err = c.mgr.withOneShotClient(ctx, m.Channel, func(ctx context.Context, cli LiveClient) error {
			var sendErr error
			lastMsgID, sendErr = c.sendWithClient(ctx, cli, *target, m)
			return sendErr
		})
	}
	if err != nil {
		return "", false, err
	}
	if lastMsgID <= 0 {
		return fmt.Sprintf("%s:%s", target.ExternalChatID, m.ItemID), true, nil
	}
	return formatExternalMessageID(target.ExternalChatID, lastMsgID), true, nil
}

func (c connector) sendWithClient(ctx context.Context, cli LiveClient, target SyncedPeer, m channel.Outgoing) (int, error) {
	if len(m.Attachments) == 0 {
		return cli.SendText(ctx, target, m.Text, deterministicRandomID(m.ItemID, 0))
	}
	if c.mgr.files == nil {
		return 0, errors.New("tgaccount: filesystem service is required to send attachments")
	}
	var lastMsgID int
	for i, att := range m.Attachments {
		fileUUID, err := uuid.Parse(att.FileID)
		if err != nil {
			return 0, errors.Wrap(err, "parse attachment file id")
		}
		rc, _, err := c.mgr.files.Open(ctx, fileUUID)
		if err != nil {
			return 0, errors.Wrap(err, "open attachment file")
		}
		caption := ""
		if i == 0 {
			caption = m.Text
		}
		msgID, sendErr := cli.SendMedia(ctx, target, att, rc, caption, deterministicRandomID(m.ItemID, i))
		_ = rc.Close()
		if sendErr != nil {
			return 0, sendErr
		}
		lastMsgID = msgID
	}
	return lastMsgID, nil
}

func (c connector) sendViaRPC(ctx context.Context, channelID string, target SyncedPeer, m channel.Outgoing) (int, error) {
	if len(m.Attachments) == 0 {
		resp, err := c.mgr.requestAccountRPC(ctx, channelID, accountRPCRequest{
			Op:       "send_text",
			Peer:     target,
			Text:     m.Text,
			RandomID: deterministicRandomID(m.ItemID, 0),
		}, rpcCallTimeout)
		if err != nil {
			return 0, err
		}
		return resp.MessageID, nil
	}
	if c.mgr.files == nil {
		return 0, errors.New("tgaccount: filesystem service is required to send attachments")
	}
	var lastMsgID int
	for i, att := range m.Attachments {
		attCopy := att
		caption := ""
		if i == 0 {
			caption = m.Text
		}
		resp, err := c.mgr.requestAccountRPC(ctx, channelID, accountRPCRequest{
			Op:         "send_media",
			Peer:       target,
			Attachment: &attCopy,
			Caption:    caption,
			RandomID:   deterministicRandomID(m.ItemID, i),
		}, mediaRPCTimeout)
		if err != nil {
			return 0, err
		}
		lastMsgID = resp.MessageID
	}
	return lastMsgID, nil
}
