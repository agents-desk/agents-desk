// Package telegram 是 Telegram 机器人渠道（HTTP Webhook 接入），渠道种类为 channel.Telegram。
// 同时支持外部客户服务（audience=customer）、商务服务（audience=partner）与内部员工服务（audience=internal），以及 1:1 私聊与群内 @ 机器人/命令。
//
// 管理员在工作区设置中填入 Bot Token 创建渠道，支持两种接入模式：
//   - 直连模式（默认）：创建、启用或轮换密钥时自动调用 Telegram setWebhook 指向本系统的 /webhooks/telegram/{id}，停用时调用 deleteWebhook；
//   - 外部网关转发模式（gateway_managed = true）：适用于业务系统与客服共用同一个 Bot 的场景，本系统不调用 setWebhook，
//     由业务网关按标准 X-Telegram-Bot-Api-Secret-Token 鉴权后将 Telegram Update 原样转发至 /webhooks/telegram/{id}，
//     并可通过 X-Agents-Desk-External-Id / Email / Name 头透传客户业务身份以自动关联或合并联系人。
package telegram

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/channel"
	"agents-desk/internal/team"
)

const (
	// SecretHeader 是 Telegram 官方 Webhook 鉴权请求头。
	SecretHeader = "X-Telegram-Bot-Api-Secret-Token"
	// ExternalIDHeader 是外部网关透传的产品用户编号请求头，用于将 Telegram 联系人关联到同一业务用户。
	ExternalIDHeader = "X-Agents-Desk-External-Id"
	// ExternalEmailHeader 是外部网关透传的客户邮箱请求头。
	ExternalEmailHeader = "X-Agents-Desk-External-Email"
	// ExternalNameHeader 是外部网关透传的客户姓名请求头。
	ExternalNameHeader = "X-Agents-Desk-External-Name"
)

var (
	// ErrTeamNotFound 是新会话进入的团队收件箱不存在，或者属于别的工作区。
	ErrTeamNotFound = errorx.Define(errorx.InvalidArgument, "telegram.team_not_found")
	// ErrInvalidBotToken 是 Telegram Bot Token 校验失败（无效或已被吊销）。
	ErrInvalidBotToken = errorx.Define(errorx.InvalidArgument, "telegram.invalid_bot_token")
	// ErrWebhookFailed 是调用 Telegram Bot API 设置或移除 Webhook 失败。
	ErrWebhookFailed = errorx.Define(errorx.Unavailable, "telegram.webhook_failed")
)

// Settings 是 Telegram 机器人保存在 channels.settings 中的配置。
type Settings struct {
	BotID          int64  `json:"bot_id,omitempty"`        // Bot 在 Telegram 中的数字 ID，由 getMe 获取
	BotToken       string `json:"bot_token"`               // Bot Token，用应用密钥加密保存
	BotUsername    string `json:"bot_username"`            // Bot 的 @username（不含 @），由 getMe 获取
	WebhookSecret  string `json:"webhook_secret"`          // Webhook 鉴权密钥，用应用密钥加密保存
	GatewayManaged bool   `json:"gateway_managed"`         // true 表示由外部业务网关转发 Webhook，本系统不调用 setWebhook / deleteWebhook
	EnableGroups   *bool  `json:"enable_groups,omitempty"` // 是否接收群内 @ 机器人与命令消息；为空默认启用
}

// GroupsEnabled 报告该 Telegram 机器人是否开启群内 @ 机器人接待（缺省为 true）。
func (s Settings) GroupsEnabled() bool {
	if s.EnableGroups == nil {
		return true
	}
	return *s.EnableGroups
}

// normalizeBotAudience 规范化公共机器人的服务对象域：允许 customer、partner 或 internal，空值回退为 fallback。
func normalizeBotAudience(in, fallback channel.Audience) channel.Audience {
	switch in {
	case channel.AudienceCustomer, channel.AudiencePartner, channel.AudienceInternal:
		return in
	default:
		switch fallback {
		case channel.AudiencePartner, channel.AudienceInternal:
			return fallback
		default:
			return channel.AudienceCustomer
		}
	}
}

// settingsOf 读出渠道 ch 中 Telegram 机器人的配置。
func settingsOf(ch *channel.Channel) (Settings, error) {
	var s Settings
	raw, err := json.Marshal(ch.Settings)
	if err != nil {
		return s, errors.Wrap(err, "encode telegram settings")
	}
	return s, errors.Wrap(json.Unmarshal(raw, &s), "decode telegram settings")
}

// toMap 把 Telegram 机器人的配置转为渠道设置 map。
func (s Settings) toMap() (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, errors.Wrap(err, "encode telegram settings")
	}
	var m map[string]any
	return m, errors.Wrap(json.Unmarshal(raw, &m), "decode telegram settings")
}

// newSecret 返回新的 Webhook 鉴权密钥：32 字节随机数的 base64url（满足 Telegram secret_token 1-256 字符且仅含字母数字下划线连字符的要求）。
func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// webhookURL 拼出渠道 id 的公开 Webhook 地址。
func webhookURL(publicURL, id string) string {
	return strings.TrimRight(publicURL, "/") + "/webhooks/telegram/" + id
}

// find 读取工作区 tenant 中的 Telegram 渠道 id，不存在、属于别的工作区或不是 Telegram 渠道时返回 channel.ErrNotFound。
func find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*channel.Channel, error) {
	ch := new(channel.Channel)
	q := db.NewSelect().Model(ch).Where("id = ? AND tenant_id = ? AND kind = ?", id, tenant, channel.Telegram)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load telegram channel")
}

// findPublic 按 ID 读取接收 Webhook 的 Telegram 渠道：渠道启用且所在工作区未停用。
func findPublic(ctx context.Context, db bun.IDB, id string) (*channel.Channel, error) {
	ch := new(channel.Channel)
	err := db.NewSelect().Model(ch).
		Where("channel.id = ? AND channel.kind = ? AND channel.enabled", id, channel.Telegram).
		Where("EXISTS (SELECT 1 FROM tenants t WHERE t.id = channel.tenant_id AND t.disabled_at IS NULL)").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load public telegram channel")
}

// checkTeam 检查团队收件箱 id 属于工作区 tenant 且未停用；为空时不检查。
func checkTeam(ctx context.Context, db bun.IDB, tenant string, id *string) error {
	if id == nil {
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

// sameTeam 报告两个团队收件箱指针是否指向同一个 ID（同为空时返回 true）。
func sameTeam(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }
