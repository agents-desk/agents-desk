// Package tgaccount 实现基于纯 Go MTProto（github.com/gotd/td）的 Telegram 个人号商务渠道连接器：
// 支持成员自助通过手机号验证码或扫码（含 2FA 两步验证）绑定一个或多个 Telegram 个人号，
// 加密保存会话凭据，由成员按白名单勾选需要同步的 1:1 单聊与外部群聊，实现实时双向消息同步与出站投递。
package tgaccount

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/channel"
)

var (
	// ErrNotConfigured 是当前工作区管理员尚未配置 Telegram API ID 与 API Hash。
	ErrNotConfigured = errorx.Define(errorx.Conflict, "tgaccount.not_configured")
	// ErrLoginNotFound 是登录会话不存在、已过期或不属于当前成员。
	ErrLoginNotFound = errorx.Define(errorx.NotFound, "tgaccount.login_not_found")
	// ErrLoginFailed 是 Telegram 登录验证失败（验证码错误、密码错误或连接中断）。
	ErrLoginFailed = errorx.Define(errorx.InvalidArgument, "tgaccount.login_failed")
	// ErrNotConnected 是该 Telegram 个人号当前未连接或授权已失效，需要重新登录。
	ErrNotConnected = errorx.Define(errorx.Conflict, "tgaccount.not_connected")
	// ErrTemporarilyUnavailable 表示 Telegram 个人号长连接正在跨实例切换或重连中，队列任务应稍后重试。
	ErrTemporarilyUnavailable = errorx.Define(errorx.Unavailable, "tgaccount.temporarily_unavailable")
	// ErrPeerNotFound 是目标 Telegram 单聊或群聊不在已同步列表中或缺少访问凭据。
	ErrPeerNotFound = errorx.Define(errorx.NotFound, "tgaccount.peer_not_found")
	// ErrPhoneRequired 是选择手机号登录时未填写带国际区号的手机号。
	ErrPhoneRequired = errorx.Define(errorx.InvalidArgument, "tgaccount.phone_required")
)

// AccountStatus 是 Telegram 个人号的连接状态。
type AccountStatus string

const (
	StatusConnected    AccountStatus = "connected"    // 已连接并持续同步
	StatusDisconnected AccountStatus = "disconnected" // 已断开或暂停
	StatusAuthExpired  AccountStatus = "auth_expired" // 授权已过期，需重新登录
)

// AccountStatuses 是全部连接状态枚举。
var AccountStatuses = enum.New("tgaccount.status", enum.Item(StatusConnected), enum.Item(StatusDisconnected), enum.Item(StatusAuthExpired))

// Schema 让 OpenAPI 中的 AccountStatus 只接受目录取值。
func (AccountStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, AccountStatuses)
}

// LoginMode 是 Telegram 个人号登录方式：扫码或手机号验证码。
type LoginMode string

const (
	LoginModeQR    LoginMode = "qr"    // 扫码登录（手机 Telegram 扫描二维码）
	LoginModePhone LoginMode = "phone" // 手机号 + 验证码登录
)

// LoginModes 是全部登录方式枚举。
var LoginModes = enum.New("tgaccount.login_mode", enum.Item(LoginModeQR), enum.Item(LoginModePhone))

// Schema 让 OpenAPI 中的 LoginMode 只接受目录取值。
func (LoginMode) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, LoginModes) }

// LoginStep 是交互式登录向导所处的阶段。
type LoginStep string

const (
	StepAwaitingQR       LoginStep = "awaiting_qr"       // 等待手机扫码
	StepAwaitingCode     LoginStep = "awaiting_code"     // 等待输入短信或 Telegram App 验证码
	StepAwaitingPassword LoginStep = "awaiting_password" // 等待输入 2FA 两步验证云密码
	StepConnected        LoginStep = "connected"         // 登录成功并已保存为个人商务渠道
	StepFailed           LoginStep = "failed"            // 登录失败或已超时
)

// LoginSteps 是全部登录阶段枚举。
var LoginSteps = enum.New("tgaccount.login_step",
	enum.Item(StepAwaitingQR), enum.Item(StepAwaitingCode), enum.Item(StepAwaitingPassword),
	enum.Item(StepConnected), enum.Item(StepFailed))

// Schema 让 OpenAPI 中的 LoginStep 只接受目录取值。
func (LoginStep) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, LoginSteps) }

// SyncMode 是 Telegram 个人号的会话同步范围策略：whitelist（精细白名单）或 folders（按 Telegram 文件夹自动同步）。
type SyncMode string

const (
	SyncModeWhitelist SyncMode = "whitelist" // 手动勾选同步的单聊与群聊白名单
	SyncModeFolders   SyncMode = "folders"   // 按 Telegram 客户端文件夹自动纳入同步
)

// GroupSyncMode 是 Telegram 外部群聊的入站过滤策略。
type GroupSyncMode string

const (
	GroupSyncAll          GroupSyncMode = "all"           // 同步选定群聊中的全部消息
	GroupSyncMentionsOnly GroupSyncMode = "mentions_only" // 仅同步群聊中提及或回复自己的消息
)

// SyncConfig 是单个 Telegram 个人号的同步策略与行为偏好，保存在 channels.settings.sync 中。
type SyncConfig struct {
	Mode          SyncMode      `json:"mode" enum:"whitelist,folders" doc:"同步范围模式：whitelist（精细白名单）或 folders（按 Telegram 文件夹）"`
	FolderIDs     []int         `json:"folder_ids,omitempty" maxItems:"20" doc:"文件夹模式下选中的 Telegram 文件夹 ID 列表"`
	BackfillLimit int           `json:"backfill_limit" minimum:"0" maximum:"50" doc:"新加入同步的会话默认拉取的历史消息条数（0-50）"`
	SyncOutgoing  bool          `json:"sync_outgoing" doc:"是否同步自己在手机/桌面 Telegram 客户端发出的消息"`
	SyncMedia     bool          `json:"sync_media" doc:"是否同步对方发来的图片与文档附件"`
	GroupMode     GroupSyncMode `json:"group_mode" enum:"all,mentions_only" doc:"群聊消息同步策略：all 或 mentions_only"`
}

// DefaultSyncConfig 返回 Telegram 个人号的默认同步策略。
func DefaultSyncConfig() SyncConfig {
	return SyncConfig{
		Mode:          SyncModeWhitelist,
		FolderIDs:     []int{},
		BackfillLimit: 20,
		SyncOutgoing:  true,
		SyncMedia:     true,
		GroupMode:     GroupSyncAll,
	}
}

// NormalizeSyncConfig 规范化同步策略取值并填充缺省项。
func NormalizeSyncConfig(in SyncConfig) SyncConfig {
	out := in
	if out.Mode != SyncModeFolders {
		out.Mode = SyncModeWhitelist
	}
	if out.BackfillLimit < 0 {
		out.BackfillLimit = 0
	}
	if out.BackfillLimit > 50 {
		out.BackfillLimit = 50
	}
	if out.GroupMode != GroupSyncMentionsOnly {
		out.GroupMode = GroupSyncAll
	}
	seen := map[int]bool{}
	cleanFolders := make([]int, 0, len(out.FolderIDs))
	for _, id := range out.FolderIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			cleanFolders = append(cleanFolders, id)
		}
	}
	out.FolderIDs = cleanFolders
	return out
}

// FolderDTO 是从成员 Telegram 账号读取到的一个文件夹（Dialog Filter），供文件夹模式勾选同步。
type FolderDTO struct {
	ID        int          `json:"id" doc:"Telegram 文件夹 ID"`
	Title     string       `json:"title" doc:"文件夹名称"`
	PeerCount int          `json:"peer_count" doc:"当前匹配到的单聊/群聊数量"`
	Peers     []SyncedPeer `json:"peers" nullable:"false" doc:"该文件夹包含的可同步单聊与群聊"`
	Synced    bool         `json:"synced" doc:"当前是否已被勾选为同步文件夹"`
}

// SyncedPeer 是成员显式勾选同步的一个 Telegram 单聊或群聊白名单项。
type SyncedPeer struct {
	ExternalChatID string `json:"external_chat_id" doc:"外部会话唯一标识（tg:user:<id>、tg:chat:<id> 或 tg:channel:<id>）"`
	PeerType       string `json:"peer_type" doc:"Telegram 对端类型：user、chat 或 channel"`
	PeerID         int64  `json:"peer_id" doc:"Telegram 数字 ID"`
	AccessHash     int64  `json:"access_hash,omitempty" doc:"MTProto 访问所需的 access_hash"`
	Title          string `json:"title" doc:"对方姓名或群聊标题"`
	Username       string `json:"username,omitempty" doc:"对方或群聊的 @username"`
	IsGroup        bool   `json:"is_group" doc:"是否为 Telegram 群聊或超级群"`
	Paused         bool   `json:"paused,omitempty" doc:"是否暂时暂停同步该会话（保留在列表中，恢复后继续同步）"`
}

// Settings 是 Telegram 个人号渠道保存在 channels.settings 中的数据结构。
type Settings struct {
	Phone          string                 `json:"phone,omitempty"`
	TelegramUserID int64                  `json:"telegram_user_id,omitempty"`
	Username       string                 `json:"username,omitempty"`
	DisplayName    string                 `json:"display_name,omitempty"`
	SessionCipher  string                 `json:"session_cipher,omitempty"` // crypt 加密的 gotd/td MTProto Session JSON
	Status         AccountStatus          `json:"status"`
	LastError      string                 `json:"last_error,omitempty"`
	LastSyncedAt   *time.Time             `json:"last_synced_at,omitempty"`
	SyncedPeers    []SyncedPeer           `json:"synced_peers,omitempty"`
	Sync           *SyncConfig            `json:"sync,omitempty"`
	Business       channel.BusinessPolicy `json:"business,omitempty"`
}

// resolvedSync 返回归一化后的同步配置，兼容历史数据缺失 sync 字段的情况。
func (s Settings) resolvedSync() SyncConfig {
	if s.Sync == nil {
		return DefaultSyncConfig()
	}
	return NormalizeSyncConfig(*s.Sync)
}

// AccountDTO 是接口中展示给成员的 Telegram 个人号商务渠道视图（不含加密会话密文）。
type AccountDTO struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name" doc:"账号显示名称，便于在商务沟通列表中区分多个个人号"`
	Phone          string                 `json:"phone,omitempty" doc:"绑定的手机号（已脱敏展示）"`
	TelegramUserID int64                  `json:"telegram_user_id,omitempty"`
	Username       string                 `json:"username,omitempty" doc:"Telegram 用户名"`
	DisplayName    string                 `json:"display_name,omitempty" doc:"Telegram 昵称"`
	Status         AccountStatus          `json:"status" doc:"连接状态：connected、disconnected 或 auth_expired"`
	LastError      string                 `json:"last_error,omitempty" doc:"最近一次连接错误信息"`
	LastSyncedAt   *time.Time             `json:"last_synced_at,omitempty" doc:"最近一次收到或同步消息的时间"`
	SyncedPeers    []SyncedPeer           `json:"synced_peers" nullable:"false" doc:"已勾选同步的 1:1 单聊与群聊列表"`
	Sync           SyncConfig             `json:"sync" doc:"同步策略与行为偏好"`
	Business       channel.BusinessPolicy `json:"business" doc:"通用商务策略（默认协作成员、自动标签与翻译覆写）"`
	Enabled        bool                   `json:"enabled"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

// DialogDTO 是从已连接 Telegram 账号读取到的一个候选单聊或群聊，供成员勾选同步。
type DialogDTO struct {
	SyncedPeer
	Synced bool `json:"synced" doc:"是否已在同步白名单中"`
}

// LoginSessionDTO 是交互式登录向导当前状态的响应视图。
type LoginSessionDTO struct {
	LoginID      string      `json:"login_id"`
	Mode         LoginMode   `json:"mode"`
	Step         LoginStep   `json:"step"`
	QRDataURL    string      `json:"qr_data_url,omitempty" doc:"扫码登录的二维码图片（data:image/png;base64,...）"`
	QRExpiresAt  *time.Time  `json:"qr_expires_at,omitempty" doc:"当前二维码过期时间"`
	PasswordHint string      `json:"password_hint,omitempty" doc:"2FA 云密码提示"`
	Error        string      `json:"error,omitempty" doc:"失败原因"`
	Account      *AccountDTO `json:"account,omitempty" doc:"登录完成后生成的 Telegram 个人号渠道"`
}

// settingsOf 解析渠道 ch 中的 Telegram 个人号配置。
func settingsOf(ch *channel.Channel) (Settings, error) {
	var s Settings
	if ch == nil || len(ch.Settings) == 0 {
		s.Status = StatusDisconnected
		return s, nil
	}
	raw, err := json.Marshal(ch.Settings)
	if err != nil {
		return s, errors.Wrap(err, "encode tgaccount settings")
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, errors.Wrap(err, "decode tgaccount settings")
	}
	if s.Status == "" {
		s.Status = StatusDisconnected
	}
	return s, nil
}

// toMap 将 Settings 序列化为 channels.settings 所需的 map。
func (s Settings) toMap() (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, errors.Wrap(err, "encode tgaccount settings")
	}
	var m map[string]any
	return m, errors.Wrap(json.Unmarshal(raw, &m), "decode tgaccount settings map")
}

// toAccountDTO 将渠道行转换为对成员安全的视图。
func toAccountDTO(ch *channel.Channel) (AccountDTO, error) {
	s, err := settingsOf(ch)
	if err != nil {
		return AccountDTO{}, err
	}
	peers := s.SyncedPeers
	if peers == nil {
		peers = []SyncedPeer{}
	}
	return AccountDTO{
		ID:             ch.ID,
		Name:           ch.Name,
		Phone:          maskPhone(s.Phone),
		TelegramUserID: s.TelegramUserID,
		Username:       s.Username,
		DisplayName:    s.DisplayName,
		Status:         s.Status,
		LastError:      s.LastError,
		LastSyncedAt:   s.LastSyncedAt,
		SyncedPeers:    peers,
		Sync:           s.resolvedSync(),
		Business:       channel.BusinessPolicyOf(ch.Settings),
		Enabled:        ch.Enabled,
		CreatedAt:      ch.CreatedAt,
		UpdatedAt:      ch.UpdatedAt,
	}, nil
}

// maskPhone 对手机号保留前后若干位并隐藏中间段，防止敏感明文暴露在列表中。
func maskPhone(phone string) string {
	p := strings.TrimSpace(phone)
	if len(p) <= 6 {
		return p
	}
	return p[:3] + "****" + p[len(p)-4:]
}

// formatPeerKey 生成确定性的外部会话键（tg:user:<id>、tg:chat:<id> 或 tg:channel:<id>）。
func formatPeerKey(peerType string, peerID int64) string {
	return fmt.Sprintf("tg:%s:%d", peerType, peerID)
}

// peerKeyFromTG 将 tg.PeerClass 转换为外部会话键、类型与数字 ID。
func peerKeyFromTG(p tg.PeerClass) (key, peerType string, id int64, ok bool) {
	switch v := p.(type) {
	case *tg.PeerUser:
		return formatPeerKey("user", v.UserID), "user", v.UserID, true
	case *tg.PeerChat:
		return formatPeerKey("chat", v.ChatID), "chat", v.ChatID, true
	case *tg.PeerChannel:
		return formatPeerKey("channel", v.ChannelID), "channel", v.ChannelID, true
	default:
		return "", "", 0, false
	}
}

// toInputPeer 将 SyncedPeer 转换为 MTProto 请求使用的 tg.InputPeerClass。
func (p SyncedPeer) toInputPeer() (tg.InputPeerClass, error) {
	switch p.PeerType {
	case "user":
		return &tg.InputPeerUser{UserID: p.PeerID, AccessHash: p.AccessHash}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: p.PeerID}, nil
	case "channel":
		return &tg.InputPeerChannel{ChannelID: p.PeerID, AccessHash: p.AccessHash}, nil
	default:
		return nil, errors.Newf("tgaccount: unsupported peer type %q", p.PeerType)
	}
}

// formatExternalMessageID 生成与 Deliver 与 Receive 统一的消息去重外部编号。
func formatExternalMessageID(extChatID string, msgID int) string {
	return extChatID + ":" + strconv.Itoa(msgID)
}

// extractMessageID 从 MTProto 发送消息返回的 Updates 中提取真实消息编号。
func extractMessageID(u tg.UpdatesClass) int {
	switch v := u.(type) {
	case *tg.UpdateShortSentMessage:
		return v.ID
	case *tg.Updates:
		for _, upd := range v.Updates {
			if id := extractFromUpdate(upd); id > 0 {
				return id
			}
		}
	case *tg.UpdatesCombined:
		for _, upd := range v.Updates {
			if id := extractFromUpdate(upd); id > 0 {
				return id
			}
		}
	}
	return 0
}

func extractFromUpdate(upd tg.UpdateClass) int {
	switch u := upd.(type) {
	case *tg.UpdateMessageID:
		return u.ID
	case *tg.UpdateNewMessage:
		if m, ok := u.Message.(*tg.Message); ok {
			return m.ID
		}
	case *tg.UpdateNewChannelMessage:
		if m, ok := u.Message.(*tg.Message); ok {
			return m.ID
		}
	}
	return 0
}

// findOwnedAccount 读取工作区 tenant 中属于 memberID 的 Telegram 个人号渠道 id。
func findOwnedAccount(ctx context.Context, db bun.IDB, tenant, memberID, id string, lock bool) (*channel.Channel, error) {
	ch := new(channel.Channel)
	q := db.NewSelect().Model(ch).
		Where("id = ? AND tenant_id = ? AND kind = ? AND owner_member_id = ?", id, tenant, channel.TelegramAccount, memberID)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load telegram account")
}

// memSessionStorage 在交互式登录阶段暂存 MTProto 会话字节，登录成功后由 Manager 加密落库。
type memSessionStorage struct {
	mu   sync.RWMutex
	data []byte
}

func (s *memSessionStorage) LoadSession(_ context.Context) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.data) == 0 {
		return nil, session.ErrNotFound
	}
	cp := make([]byte, len(s.data))
	copy(cp, s.data)
	return cp, nil
}

func (s *memSessionStorage) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make([]byte, len(data))
	copy(s.data, data)
	return nil
}

func (s *memSessionStorage) Bytes() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]byte, len(s.data))
	copy(cp, s.data)
	return cp
}

// dbSessionStorage 将 gotd/td 的 MTProto Session 经 crypt.Crypt 加密保存在 channels.settings.session_cipher 中。
type dbSessionStorage struct {
	db        *bun.DB
	crypt     *crypt.Crypt
	tenantID  string
	channelID string
}

func (s *dbSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	ch, err := channel.Find(ctx, s.db, s.tenantID, s.channelID)
	if err != nil {
		return nil, err
	}
	cfg, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	if cfg.SessionCipher == "" {
		return nil, session.ErrNotFound
	}
	plain, err := s.crypt.Decrypt(cfg.SessionCipher)
	if err != nil {
		return nil, errors.Wrap(err, "decrypt telegram session")
	}
	raw, err := base64.StdEncoding.DecodeString(plain)
	if err != nil {
		return nil, errors.Wrap(err, "decode telegram session bytes")
	}
	return raw, nil
}

func (s *dbSessionStorage) StoreSession(ctx context.Context, data []byte) error {
	cipher := encryptSessionBytes(s.crypt, data)
	_, err := s.db.NewRaw(`UPDATE channels
		SET settings = jsonb_set(settings, '{session_cipher}', to_jsonb(?::text), true),
		    updated_at = ?
		WHERE id = ? AND tenant_id = ?`,
		cipher, time.Now().UTC(), s.channelID, s.tenantID).Exec(ctx)
	return errors.Wrap(err, "store encrypted telegram session")
}

// encryptSessionBytes 将 MTProto 会话字节编码并用应用密钥加密为密文字符串。
func encryptSessionBytes(c *crypt.Crypt, data []byte) string {
	return c.Encrypt(base64.StdEncoding.EncodeToString(data))
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }
