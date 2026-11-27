package tgaccount

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/platformsettings"
)

// loginEntry 保存进行中的单次 Telegram 个人号交互式登录会话，归属隔离到 (tenantID, memberID)。
type loginEntry struct {
	id         string
	tenantID   string
	memberID   string
	name       string
	mode       LoginMode
	driver     LoginDriver
	createdAt  time.Time
	accountDTO *AccountDTO
	persisted  bool
	mu         sync.Mutex
}

// accountRunner 管理单个已绑定 Telegram 个人号的长连接协程与内存白名单。
type accountRunner struct {
	channelID string
	tenantID  string
	cancel    context.CancelFunc

	mu          sync.RWMutex
	client      LiveClient
	syncedPeers map[string]SyncedPeer
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

func (r *accountRunner) lookupPeer(extChatID string) (SyncedPeer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.syncedPeers[extChatID]
	return p, ok
}

// Manager 统一管理 Telegram 个人号交互式登录、长连接同步协程与出站消息投递连接器。
type Manager struct {
	db         *bun.DB
	crypt      *crypt.Crypt
	files      *filesystem.Service
	tgSettings *settings.Group[platformsettings.Telegram]
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
func NewManager(db *bun.DB, c *crypt.Crypt, files *filesystem.Service, tgSettings *settings.Group[platformsettings.Telegram], backend Backend) *Manager {
	if backend == nil {
		backend = NewGotdBackend()
	}
	return &Manager{
		db:         db,
		crypt:      c,
		files:      files,
		tgSettings: tgSettings,
		backend:    backend,
		logins:     map[string]*loginEntry{},
		runners:    map[string]*accountRunner{},
		wakeCh:     make(chan struct{}, 1),
	}
}

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

// credentials 读取平台配置的 Telegram API ID 与 API Hash，未配置时返回 ErrNotConfigured。
func (m *Manager) credentials(ctx context.Context) (platformsettings.Telegram, error) {
	if m.tgSettings == nil {
		return platformsettings.Telegram{}, ErrNotConfigured.New()
	}
	cfg, err := m.tgSettings.Get(ctx)
	if err != nil {
		return platformsettings.Telegram{}, errors.Wrap(err, "load platform telegram settings")
	}
	if !cfg.Configured() {
		return platformsettings.Telegram{}, ErrNotConfigured.New()
	}
	return cfg, nil
}

// Configured 报告平台是否已配置 Telegram API 凭据。
func (m *Manager) Configured(ctx context.Context) (bool, error) {
	if m.tgSettings == nil {
		return false, nil
	}
	cfg, err := m.tgSettings.Get(ctx)
	if err != nil {
		return false, errors.Wrap(err, "load platform telegram settings")
	}
	return cfg.Configured(), nil
}

// StartLogin 为成员 sub 启动一次交互式 Telegram 个人号登录（扫码或手机号验证码）。
func (m *Manager) StartLogin(ctx context.Context, sub identity.Subject, mode LoginMode, name, phone string) (*LoginSessionDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	if m.checkQuota != nil {
		if err := m.checkQuota(ctx, sub, false); err != nil {
			return nil, err
		}
	}
	creds, err := m.credentials(ctx)
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
	name = strings.TrimSpace(name)

	var drv LoginDriver
	if mode == LoginModePhone {
		drv, err = m.backend.StartPhoneLogin(ctx, creds.APIID, creds.APIHash, phone)
	} else {
		drv, err = m.backend.StartQRLogin(ctx, creds.APIID, creds.APIHash)
	}
	if err != nil {
		return nil, ErrLoginFailed.Wrap(err)
	}

	m.pruneExpiredLogins()
	entry := &loginEntry{
		id:        newID(),
		tenantID:  sub.Tenant,
		memberID:  sub.ID,
		name:      name,
		mode:      mode,
		driver:    drv,
		createdAt: time.Now().UTC(),
	}
	m.loginsMu.Lock()
	m.logins[entry.id] = entry
	m.loginsMu.Unlock()

	return m.buildLoginDTO(ctx, entry)
}

// GetLogin 查询进行中登录会话的当前状态；若扫码已完成则自动落库并启动长连接。
func (m *Manager) GetLogin(ctx context.Context, sub identity.Subject, loginID string) (*LoginSessionDTO, error) {
	entry, err := m.findLogin(sub, loginID)
	if err != nil {
		return nil, err
	}
	return m.buildLoginDTO(ctx, entry)
}

// SubmitCode 向手机号登录会话提交短信或 Telegram App 验证码。
func (m *Manager) SubmitCode(ctx context.Context, sub identity.Subject, loginID, code string) (*LoginSessionDTO, error) {
	entry, err := m.findLogin(sub, loginID)
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
			e.driver.Close()
			delete(m.logins, id)
		}
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
			m.wake()
		}
		dto.Account = entry.accountDTO
	}
	return dto, nil
}

// persistConnectedAccount 将登录成功的 Telegram 个人号加密持久化为成员个人商务渠道（同账号重新登录时原地刷新会话）。
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
		// 若该成员在当前工作区已绑定同一个 Telegram UserID，则原地更新加密会话与状态，避免重复建渠道
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
					cfg.Phone = st.Profile.Phone
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
						existing[i].Name = entry.name
					}
					existing[i].Enabled = true
					existing[i].Settings = sm
					if _, err := tx.NewUpdate().Model(&existing[i]).Column("name", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
						return errors.Wrap(err, "update reconnected telegram account")
					}
					saved = &existing[i]
					return nil
				}
			}
		}

		cfg := Settings{
			Phone:          st.Profile.Phone,
			TelegramUserID: st.Profile.UserID,
			Username:       st.Profile.Username,
			DisplayName:    displayName,
			SessionCipher:  cipher,
			Status:         StatusConnected,
			SyncedPeers:    []SyncedPeer{},
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

// UpdateAccount 修改成员个人 Telegram 账号的名称或启用状态，并同步通知常驻连接器。
func (m *Manager) UpdateAccount(ctx context.Context, sub identity.Subject, id, name string, enabled bool) (*AccountDTO, error) {
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
		if !enabled {
			cfg.Status = StatusDisconnected
		} else if cfg.SessionCipher != "" && cfg.Status == StatusDisconnected {
			cfg.Status = StatusConnected
			cfg.LastError = ""
		}
		sm, err := cfg.toMap()
		if err != nil {
			return err
		}
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			ch.Name = trimmed
		}
		ch.Enabled = enabled
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
	m.wake()
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
	m.wake()
	return nil
}

// ListDialogs 读取已绑定 Telegram 个人号当前最近的 1:1 单聊与群聊候选列表，并标出哪些已在同步白名单中。
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

	var dialogs []DialogDTO
	if err := m.withClient(ctx, ch, func(ctx context.Context, cli LiveClient) error {
		var err error
		dialogs, err = cli.ListDialogs(ctx, 100)
		return err
	}); err != nil {
		return nil, err
	}
	for i := range dialogs {
		dialogs[i].Synced = syncedSet[dialogs[i].ExternalChatID]
	}
	return dialogs, nil
}

// UpdateSyncedPeers 更新成员勾选同步的 Telegram 1:1 单聊与群聊白名单，并可选对新增会话拉取最近 N 条历史消息。
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

	// 立即更新内存中的同步白名单
	m.runnersMu.RLock()
	if r := m.runners[id]; r != nil {
		r.setPeers(clean)
	}
	m.runnersMu.RUnlock()

	// 对新勾选的会话按需拉取最近历史消息（单条失败记警告日志，不阻塞配置保存）
	if backfillLimit > 0 && len(added) > 0 && m.receive != nil {
		if err := m.backfillPeers(ctx, updated, added, backfillLimit); err != nil {
			slog.WarnContext(ctx, "backfill telegram synced peers failed", "channel_id", id, "error", err)
		}
	}

	dto, err := toAccountDTO(updated)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

func (m *Manager) backfillPeers(ctx context.Context, ch *channel.Channel, peers []SyncedPeer, limit int) error {
	return m.withClient(ctx, ch, func(ctx context.Context, cli LiveClient) error {
		for _, peer := range peers {
			msgs, err := cli.FetchHistory(ctx, peer, limit)
			if err != nil {
				slog.WarnContext(ctx, "fetch telegram peer history failed", "channel_id", ch.ID, "peer", peer.ExternalChatID, "error", err)
				continue
			}
			for _, msg := range msgs {
				if err := m.ingestMessage(ctx, ch, peer, msg); err != nil {
					slog.WarnContext(ctx, "ingest telegram history message failed", "channel_id", ch.ID, "peer", peer.ExternalChatID, "error", err)
				}
			}
		}
		return nil
	})
}

// withClient 优先复用已在线的 LiveClient；若长连接不在本进程（例如独立 worker 进程），则以加密会话临时建立一次连接执行。
func (m *Manager) withClient(ctx context.Context, ch *channel.Channel, fn func(context.Context, LiveClient) error) error {
	m.runnersMu.RLock()
	r := m.runners[ch.ID]
	m.runnersMu.RUnlock()
	if r != nil {
		if cli := r.getClient(); cli != nil {
			return fn(ctx, cli)
		}
	}
	creds, err := m.credentials(ctx)
	if err != nil {
		return err
	}
	storage := &dbSessionStorage{db: m.db, crypt: m.crypt, tenantID: ch.TenantID, channelID: ch.ID}
	return m.backend.RunOneShot(ctx, creds.APIID, creds.APIHash, storage, fn)
}

// Run 常驻同步循环：按周期或变更通知自动为每个启用且已授权的 Telegram 个人号维护 MTProto 长连接。
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer m.stopAllRunners()

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
	creds, err := m.credentials(ctx)
	if err != nil {
		m.stopAllRunners()
		return
	}
	var rows []channel.Channel
	err = m.db.NewSelect().Model(&rows).
		Where("kind = ? AND enabled = TRUE AND owner_member_id IS NOT NULL", channel.TelegramAccount).
		Where("EXISTS (SELECT 1 FROM tenants t WHERE t.id = channel.tenant_id AND t.disabled_at IS NULL)").
		Scan(ctx)
	if err != nil {
		slog.WarnContext(ctx, "load active telegram accounts for sync", "error", err)
		return
	}

	desired := map[string]channel.Channel{}
	for _, ch := range rows {
		cfg, err := settingsOf(&ch)
		if err != nil || cfg.SessionCipher == "" || cfg.Status == StatusAuthExpired {
			continue
		}
		desired[ch.ID] = ch
	}

	// 停止已停用、已删除或授权失效的连接
	m.runnersMu.Lock()
	for id, r := range m.runners {
		if _, ok := desired[id]; !ok {
			r.cancel()
			delete(m.runners, id)
		}
	}
	for id, ch := range desired {
		cfg, _ := settingsOf(&ch)
		if existing := m.runners[id]; existing != nil {
			existing.setPeers(cfg.SyncedPeers)
			continue
		}
		rCtx, cancel := context.WithCancel(ctx)
		runner := &accountRunner{channelID: id, tenantID: ch.TenantID, cancel: cancel}
		runner.setPeers(cfg.SyncedPeers)
		m.runners[id] = runner
		chCopy := ch
		go m.runLoop(rCtx, creds, &chCopy, runner)
	}
	m.runnersMu.Unlock()
}

func (m *Manager) runLoop(ctx context.Context, creds platformsettings.Telegram, ch *channel.Channel, runner *accountRunner) {
	backoff := 3 * time.Second
	storage := &dbSessionStorage{db: m.db, crypt: m.crypt, tenantID: ch.TenantID, channelID: ch.ID}
	for {
		if ctx.Err() != nil {
			return
		}
		err := m.backend.RunAccount(ctx, creds.APIID, creds.APIHash, storage,
			func(msgCtx context.Context, msg IncomingMessage) error {
				peer, ok := runner.lookupPeer(msg.ExternalChatID)
				if !ok {
					// 未在同步白名单中的个人隐私聊天直接忽略
					return nil
				}
				return m.ingestMessage(msgCtx, ch, peer, msg)
			},
			func(cli LiveClient) {
				runner.setClient(cli)
				backoff = 3 * time.Second
				m.recordStatus(ctx, ch.TenantID, ch.ID, StatusConnected, "")
				slog.InfoContext(ctx, "telegram personal account connected", "channel_id", ch.ID)
			},
		)
		runner.setClient(nil)
		if ctx.Err() != nil {
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
		WHERE id = ? AND tenant_id = ?`,
		string(status), lastErr, time.Now().UTC(), channelID, tenantID).Exec(ctx)
	if err != nil {
		slog.WarnContext(ctx, "update telegram account status", "channel_id", channelID, "error", err)
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

// ingestMessage 将白名单内的一条 Telegram 单聊或群聊消息写入外部商务聊天，并过滤本平台发出后的回声消息。
func (m *Manager) ingestMessage(ctx context.Context, ch *channel.Channel, peer SyncedPeer, msg IncomingMessage) error {
	if m.receive == nil || strings.TrimSpace(msg.Text) == "" {
		return nil
	}
	extID := formatExternalMessageID(peer.ExternalChatID, msg.MessageID)
	// 若本平台刚通过 Connector.Send 发出该消息，message_deliveries 已记录相同 external_id，跳过回声避免重复
	if msg.FromOwner {
		alreadySent, err := m.db.NewSelect().TableExpr("message_deliveries AS d").
			Join("JOIN timeline_items AS ti ON ti.id = d.item_id").
			Where("ti.tenant_id = ? AND ti.channel_id = ? AND d.external_id = ?", ch.TenantID, ch.ID, extID).
			Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check outgoing telegram echo")
		}
		if alreadySent {
			return nil
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
	_, err := m.receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID:      ch.ID,
		From:           from,
		FromName:       fromName,
		ExternalID:     extID,
		ExternalChatID: peer.ExternalChatID,
		IsGroup:        peer.IsGroup,
		ChatTitle:      peer.Title,
		FromOwner:      msg.FromOwner,
		CreatedAt:      &createdAt,
		Text:           msg.Text,
	})
	return err
}

// Connector 返回实现 channel.Connector 的 Telegram 个人号出站连接器。
func (m *Manager) Connector() channel.Connector { return connector{mgr: m} }

type connector struct {
	mgr *Manager
}

func (connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		MessageTypes: []string{"text", "image", "video", "audio", "file"},
		ReadReceipts: false,
		GroupChat:    true,
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
	err = c.mgr.withClient(ctx, m.Channel, func(ctx context.Context, cli LiveClient) error {
		if len(m.Attachments) == 0 {
			var err error
			lastMsgID, err = cli.SendText(ctx, *target, m.Text)
			return err
		}
		if c.mgr.files == nil {
			return errors.New("tgaccount: filesystem service is required to send attachments")
		}
		for i, att := range m.Attachments {
			fileUUID, err := uuid.Parse(att.FileID)
			if err != nil {
				return errors.Wrap(err, "parse attachment file id")
			}
			rc, _, err := c.mgr.files.Open(ctx, fileUUID)
			if err != nil {
				return errors.Wrap(err, "open attachment file")
			}
			caption := ""
			if i == 0 {
				caption = m.Text
			}
			msgID, sendErr := cli.SendMedia(ctx, *target, att, rc, caption)
			_ = rc.Close()
			if sendErr != nil {
				return sendErr
			}
			lastMsgID = msgID
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	if lastMsgID <= 0 {
		return fmt.Sprintf("%s:%s", target.ExternalChatID, m.ItemID), true, nil
	}
	return formatExternalMessageID(target.ExternalChatID, lastMsgID), true, nil
}
