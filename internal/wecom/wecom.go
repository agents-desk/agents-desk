// Package wecom 是企业微信智能机器人渠道（渠道种类 channel.WeCom），仅通过官方 WebSocket 长连接接入。
// 无需配置公网 HTTPS 回调域名或消息加解密密钥；多实例部署时通过分布式租约（lock.Locker.Hold）保证每个机器人
// 仅由单个实例持有长连接，并通过 NATS 请求-响应转发跨实例出站投递。
package wecom

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"maps"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/channel"
	"agents-desk/internal/team"
)

const (
	// DefaultWSURL 是企业微信智能机器人官方 WebSocket 长连接入口。
	DefaultWSURL = "wss://openws.work.weixin.qq.com"
	// MaxDownloadSize 是企业微信机器人媒体下载单文件最大体积（20 MiB）。
	MaxDownloadSize = 20 * filesystem.MiB
	// ReplyWindow 是入站 req_id 允许使用 aibot_respond_msg 被动回复的有效窗口，超出后自动改用 aibot_send_msg 主动发送。
	ReplyWindow = 23 * time.Hour
)

var (
	// ErrTeamNotFound 是新会话进入的团队收件箱不存在，或者属于别的工作区。
	ErrTeamNotFound = errorx.Define(errorx.InvalidArgument, "wecom.team_not_found")
	// ErrInvalidWSURL 是自定义 WebSocket 接入地址格式不合法（必须为 wss:// 或 ws:// 开头）。
	ErrInvalidWSURL = errorx.Define(errorx.InvalidArgument, "wecom.invalid_ws_url")
	// ErrNotConnected 是企业微信机器人长连接当前未在线，无法发送消息。
	ErrNotConnected = errorx.Define(errorx.Unavailable, "wecom.not_connected")
)

// ConnectStatus 是企业微信机器人长连接的当前运行状态。
type ConnectStatus string

const (
	StatusConnected    ConnectStatus = "connected"    // 长连接在线并已完成鉴权订阅
	StatusConnecting   ConnectStatus = "connecting"   // 正在建立连接或重连中
	StatusDisconnected ConnectStatus = "disconnected" // 已断开或渠道已停用
	StatusAuthFailed   ConnectStatus = "auth_failed"  // BotID 或 Secret 鉴权失败，需要更新凭据后重连
)

// ConnectStatuses 是全部长连接状态枚举，显示文字在 wecom.status.<状态>。
var ConnectStatuses = enum.New("wecom.status",
	enum.Item(StatusConnected), enum.Item(StatusConnecting), enum.Item(StatusDisconnected), enum.Item(StatusAuthFailed))

// Schema 让 OpenAPI 中的 ConnectStatus 只接受目录取值。
func (ConnectStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, ConnectStatuses)
}

// Settings 是企业微信智能机器人保存在 channels.settings 中的配置与连接状态。
type Settings struct {
	BotID           string        `json:"bot_id"`                      // 企业微信智能机器人 BotID
	Secret          string        `json:"secret"`                      // 机器人 Secret（用应用密钥加密保存）
	WSURL           string        `json:"ws_url,omitempty"`            // 可选私有化接入地址；留空使用官方地址
	WelcomeText     string        `json:"welcome_text,omitempty"`      // 收到 enter_chat 事件时自动发送的欢迎语（留空不发送）
	Status          ConnectStatus `json:"status,omitempty"`            // 当前长连接状态
	LastError       string        `json:"last_error,omitempty"`        // 最近一次连接或鉴权错误
	LastConnectedAt *time.Time    `json:"last_connected_at,omitempty"` // 最近一次成功建立长连接的时间
}

// EffectiveWSURL 返回实际建立 WebSocket 连接的地址。
func (s Settings) EffectiveWSURL() string {
	if u := strings.TrimSpace(s.WSURL); u != "" {
		return u
	}
	return DefaultWSURL
}

// settingsOf 解析渠道 ch 中保存的企业微信机器人配置。
func settingsOf(ch *channel.Channel) (Settings, error) {
	var s Settings
	raw, err := json.Marshal(ch.Settings)
	if err != nil {
		return s, errors.Wrap(err, "encode wecom settings")
	}
	return s, errors.Wrap(json.Unmarshal(raw, &s), "decode wecom settings")
}

// toMap 将企业微信机器人配置编码为渠道 settings map。
func (s Settings) toMap() (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, errors.Wrap(err, "encode wecom settings")
	}
	var m map[string]any
	return m, errors.Wrap(json.Unmarshal(raw, &m), "decode wecom settings")
}

// BotDTO 是工作区内部服务设置中展示的企业微信智能机器人视图（不暴露加密 Secret 明文）。
type BotDTO struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Audience        channel.Audience `json:"audience" doc:"服务对象类型，企业微信智能机器人固定为 internal（内部员工）"`
	TeamID          *string          `json:"team_id,omitempty" doc:"新会话进入的团队收件箱；为空时进入未指派"`
	TeamName        string           `json:"team_name" doc:"新会话进入的团队收件箱名称；未指派时为空"`
	BotID           string           `json:"bot_id" doc:"企业微信智能机器人 BotID"`
	WSURL           string           `json:"ws_url,omitempty" doc:"自定义 WebSocket 接入地址；留空表示使用官方地址"`
	WelcomeText     string           `json:"welcome_text,omitempty" doc:"进入会话时的自动欢迎语"`
	Status          ConnectStatus    `json:"status" doc:"长连接当前状态"`
	LastError       string           `json:"last_error,omitempty" doc:"最近一次连接错误详情"`
	LastConnectedAt *time.Time       `json:"last_connected_at,omitempty" doc:"最近一次连接成功时间"`
	Enabled         bool             `json:"enabled" doc:"启用后自动建立并维持 WebSocket 长连接"`
	CreatedAt       time.Time        `json:"created_at"`
}

// BotOutput 是返回单个企业微信机器人的响应。
type BotOutput struct {
	Body BotDTO
}

// ListInput 是企业微信机器人列表的查询参数。
type ListInput struct {
	page.OffsetParams
}

// ListOutput 是一页企业微信机器人。
type ListOutput struct {
	Body page.Offset[BotDTO]
}

// IDInput 是指定单个企业微信机器人的路径参数。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// CreateBody 是新建企业微信智能机器人的请求体。
type CreateBody struct {
	Name        string  `json:"name" minLength:"1" maxLength:"60"`
	TeamID      *string `json:"team_id,omitempty" format:"uuid" doc:"新会话进入的团队收件箱；不填时进入未指派"`
	BotID       string  `json:"bot_id" minLength:"1" maxLength:"128" doc:"企业微信管理后台生成的智能机器人 BotID"`
	Secret      string  `json:"secret" minLength:"6" maxLength:"256" format:"password" doc:"企业微信管理后台生成的长连接 Secret"`
	WSURL       string  `json:"ws_url,omitempty" maxLength:"256" doc:"可选：私有化部署时的自定义 WebSocket 地址"`
	WelcomeText string  `json:"welcome_text,omitempty" maxLength:"1000" doc:"可选：员工首次进入单聊时的自动欢迎语"`
}

// CreateInput 是新建企业微信机器人的 HTTP 请求。
type CreateInput struct {
	Body CreateBody
}

// UpdateBody 是修改企业微信智能机器人的请求体；Secret 留空表示保持原有密钥不变。
type UpdateBody struct {
	Name        string  `json:"name" minLength:"1" maxLength:"60"`
	TeamID      *string `json:"team_id,omitempty" format:"uuid" doc:"新会话进入的团队收件箱；不填时进入未指派"`
	BotID       string  `json:"bot_id" minLength:"1" maxLength:"128" doc:"企业微信智能机器人 BotID"`
	Secret      string  `json:"secret,omitempty" maxLength:"256" format:"password" doc:"新的长连接 Secret；留空则保持原密钥不变"`
	WSURL       string  `json:"ws_url,omitempty" maxLength:"256" doc:"可选：私有化自定义 WebSocket 地址"`
	WelcomeText string  `json:"welcome_text,omitempty" maxLength:"1000" doc:"可选：进入会话时的自动欢迎语"`
	Enabled     bool    `json:"enabled" doc:"启用后自动维持长连接；停用后断开连接"`
}

// UpdateInput 是修改企业微信机器人的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBody
}

// List 列出当前工作区的企业微信机器人渠道。
type List struct {
	db *bun.DB
}

// NewList 返回企业微信机器人列表动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的全部企业微信机器人渠道。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[BotDTO], error) {
	if in == nil {
		in = &ListInput{}
	}
	var rows []channel.Channel
	total, err := a.db.NewSelect().Model(&rows).Where("tenant_id = ? AND kind = ?", sub.Tenant, channel.WeCom).
		OrderExpr("created_at, id").Limit(in.Limit()).Offset(in.Offset()).ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list wecom channels")
	}
	items, err := toDTOs(ctx, a.db, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/wecom-bots。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}

// Get 读取当前工作区的单个企业微信机器人渠道。
type Get struct {
	db *bun.DB
}

// NewGet 返回读取企业微信机器人的动作。
func NewGet(db *bun.DB) *Get { return &Get{db: db} }

// Handle 返回 sub 所在工作区中的企业微信机器人 id。
func (a *Get) Handle(ctx context.Context, sub identity.Subject, id string) (*BotDTO, error) {
	ch, err := find(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	dtos, err := toDTOs(ctx, a.db, []channel.Channel{*ch})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 GET /t/{slug}/api/wecom-bots/{id}。
func (a *Get) HTTP(ctx context.Context, in *IDInput) (*BotOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &BotOutput{Body: *dto}, nil
}

// Create 在当前工作区新建一个企业微信智能机器人，并唤醒长连接管理器建立连接。
type Create struct {
	db    *bun.DB
	crypt *crypt.Crypt
	wake  func()
}

// NewCreate 返回新建企业微信机器人的动作。
func NewCreate(db *bun.DB, c *crypt.Crypt, wake func()) *Create {
	return &Create{db: db, crypt: c, wake: wake}
}

// Handle 校验配置并持久化新建的企业微信机器人（固定归属内部员工服务），提交后唤醒后台长连接管理器立即连接。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, b CreateBody) (*BotDTO, error) {
	wsURL, err := normalizeWSURL(b.WSURL)
	if err != nil {
		return nil, err
	}
	settings, err := Settings{
		BotID:       strings.TrimSpace(b.BotID),
		Secret:      a.crypt.Encrypt(strings.TrimSpace(b.Secret)),
		WSURL:       wsURL,
		WelcomeText: strings.TrimSpace(b.WelcomeText),
		Status:      StatusConnecting,
	}.toMap()
	if err != nil {
		return nil, err
	}
	id := newID()
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := checkTeam(ctx, tx, sub.Tenant, b.TeamID); err != nil {
			return err
		}
		ch := &channel.Channel{
			ID:       id,
			TenantID: sub.Tenant,
			Kind:     channel.WeCom,
			Audience: channel.AudienceInternal,
			Name:     strings.TrimSpace(b.Name),
			Settings: settings,
			TeamID:   b.TeamID,
			Enabled:  true,
		}
		if _, err := tx.NewInsert().Model(ch).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert wecom channel")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.created", TargetType: "channel", TargetID: id,
			Properties: map[string]any{"kind": string(channel.WeCom), "audience": string(channel.AudienceInternal)},
		})
	})
	if err != nil {
		return nil, err
	}
	if a.wake != nil {
		a.wake()
	}
	slog.InfoContext(ctx, "wecom bot channel created", "channel_id", id, "bot_id", strings.TrimSpace(b.BotID))
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 POST /t/{slug}/api/wecom-bots。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*BotOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &BotOutput{Body: *dto}, nil
}

// Update 修改企业微信智能机器人配置并在凭据、地址或启停变化时触发长连接重建。
type Update struct {
	db      *bun.DB
	crypt   *crypt.Crypt
	restart func(channelID string)
}

// NewUpdate 返回修改企业微信机器人的动作。
func NewUpdate(db *bun.DB, c *crypt.Crypt, restart func(channelID string)) *Update {
	return &Update{db: db, crypt: c, restart: restart}
}

// Handle 修改 sub 所在工作区中的企业微信机器人 id 并记录操作日志。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, b UpdateBody) (*BotDTO, error) {
	wsURL, err := normalizeWSURL(b.WSURL)
	if err != nil {
		return nil, err
	}
	needRestart := false
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if !sameTeam(ch.TeamID, b.TeamID) {
			if err := checkTeam(ctx, tx, sub.Tenant, b.TeamID); err != nil {
				return err
			}
		}
		s, err := settingsOf(ch)
		if err != nil {
			return err
		}
		name := strings.TrimSpace(b.Name)
		botID := strings.TrimSpace(b.BotID)
		newSecret := strings.TrimSpace(b.Secret)
		welcome := strings.TrimSpace(b.WelcomeText)

		var changed []string
		if ch.Name != name {
			changed = append(changed, "name")
		}
		if !sameTeam(ch.TeamID, b.TeamID) {
			changed = append(changed, "team_id")
		}
		if ch.Enabled != b.Enabled {
			changed = append(changed, "enabled")
			needRestart = true
		}
		if s.BotID != botID || newSecret != "" || s.WSURL != wsURL || s.WelcomeText != welcome {
			changed = append(changed, "settings")
			if s.BotID != botID || newSecret != "" || s.WSURL != wsURL {
				needRestart = true
			}
		}
		if len(changed) == 0 {
			return nil
		}
		s.BotID = botID
		if newSecret != "" {
			s.Secret = a.crypt.Encrypt(newSecret)
		}
		s.WSURL = wsURL
		s.WelcomeText = welcome
		if !b.Enabled {
			s.Status = StatusDisconnected
			s.LastError = ""
		} else if needRestart {
			s.Status = StatusConnecting
			s.LastError = ""
		}
		sm, err := s.toMap()
		if err != nil {
			return err
		}
		maps.Copy(ch.Settings, sm)
		ch.Name, ch.Audience, ch.TeamID, ch.Enabled = name, channel.AudienceInternal, b.TeamID, b.Enabled
		if _, err := tx.NewUpdate().Model(ch).Column("name", "audience", "team_id", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update wecom channel")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "channel.updated", TargetType: "channel", TargetID: id, Properties: map[string]any{"changed": changed},
		})
	})
	if err != nil {
		return nil, err
	}
	if needRestart && a.restart != nil {
		a.restart(id)
	}
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/wecom-bots/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*BotOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &BotOutput{Body: *dto}, nil
}

// Reconnect 清除鉴权/连接错误并强制重建指定企业微信机器人的 WebSocket 长连接。
type Reconnect struct {
	db      *bun.DB
	restart func(channelID string)
}

// NewReconnect 返回手动重连企业微信机器人的动作。
func NewReconnect(db *bun.DB, restart func(channelID string)) *Reconnect {
	return &Reconnect{db: db, restart: restart}
}

// Handle 将机器人状态置为 connecting、启用渠道并触发重连。
func (a *Reconnect) Handle(ctx context.Context, sub identity.Subject, id string) (*BotDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := find(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		s, err := settingsOf(ch)
		if err != nil {
			return err
		}
		s.Status = StatusConnecting
		s.LastError = ""
		sm, err := s.toMap()
		if err != nil {
			return err
		}
		ch.Enabled = true
		ch.Settings = sm
		_, err = tx.NewUpdate().Model(ch).Column("enabled", "settings", "updated_at").WherePK().Exec(ctx)
		return errors.Wrap(err, "reconnect wecom channel")
	})
	if err != nil {
		return nil, err
	}
	if a.restart != nil {
		a.restart(id)
	}
	slog.InfoContext(ctx, "wecom bot manual reconnect requested", "channel_id", id)
	return NewGet(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 POST /t/{slug}/api/wecom-bots/{id}/reconnect。
func (a *Reconnect) HTTP(ctx context.Context, in *IDInput) (*BotOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &BotOutput{Body: *dto}, nil
}

func find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*channel.Channel, error) {
	ch := new(channel.Channel)
	q := db.NewSelect().Model(ch).Where("id = ? AND tenant_id = ? AND kind = ?", id, tenant, channel.WeCom)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load wecom channel")
}

func checkTeam(ctx context.Context, db bun.IDB, tenant string, id *string) error {
	if id == nil || *id == "" {
		return nil
	}
	var row struct {
		DisabledAt *time.Time `bun:"disabled_at"`
	}
	err := db.NewSelect().Table("teams").Column("disabled_at").Where("id = ? AND tenant_id = ?", *id, tenant).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTeamNotFound.New(errorx.Field("team_id"))
	}
	if err != nil {
		return errors.Wrap(err, "check team")
	}
	if row.DisabledAt != nil {
		return team.ErrDisabled.New(errorx.Field("team_id"))
	}
	return nil
}

func normalizeWSURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return "", ErrInvalidWSURL.New(errorx.Field("ws_url"))
	}
	return s, nil
}

func sameTeam(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func toDTOs(ctx context.Context, db bun.IDB, chs []channel.Channel) ([]BotDTO, error) {
	out := make([]BotDTO, len(chs))
	var teamIDs []string
	for _, ch := range chs {
		if ch.TeamID != nil && *ch.TeamID != "" {
			teamIDs = append(teamIDs, *ch.TeamID)
		}
	}
	teams := map[string]string{}
	if len(teamIDs) > 0 {
		var rows []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("teams").Column("id", "name").Where("id IN (?)", bun.In(teamIDs)).Scan(ctx, &rows); err != nil {
			return nil, errors.Wrap(err, "load wecom team names")
		}
		for _, r := range rows {
			teams[r.ID] = r.Name
		}
	}
	for i := range chs {
		ch := &chs[i]
		s, err := settingsOf(ch)
		if err != nil {
			return nil, err
		}
		st := s.Status
		if st == "" {
			if ch.Enabled {
				st = StatusConnecting
			} else {
				st = StatusDisconnected
			}
		}
		out[i] = BotDTO{
			ID:              ch.ID,
			Name:            ch.Name,
			Audience:        ch.EffectiveAudience(),
			TeamID:          ch.TeamID,
			BotID:           s.BotID,
			WSURL:           s.WSURL,
			WelcomeText:     s.WelcomeText,
			Status:          st,
			LastError:       s.LastError,
			LastConnectedAt: s.LastConnectedAt,
			Enabled:         ch.Enabled,
			CreatedAt:       ch.CreatedAt,
		}
		if ch.TeamID != nil {
			out[i].TeamName = teams[*ch.TeamID]
		}
	}
	return out, nil
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }
