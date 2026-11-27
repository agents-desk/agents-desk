// Package channel 是渠道：工作区中一个具体的接入点，例如某个网站上的聊天插件、某个 Telegram 机器人、某个企业微信机器人。
// 渠道的传输协议（Kind）与服务对象（Audience）相互正交：每一种协议由一个连接器实现，登记到 Registry；
// 需要后台常驻连接（WebSocket、MTProto 等）的渠道管理器实现 Runner，由入口统一托管生命周期。
package channel

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrNotFound 是渠道不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "channel.not_found")
	// ErrDisabled 是渠道已停用，不再收发消息。
	ErrDisabled = errorx.Define(errorx.Conflict, "channel.disabled")
)

// Kind 是渠道的传输协议种类，决定由哪个连接器收发消息。
type Kind string

const (
	Test            Kind = "test"             // 测试渠道：开发、演示和测试用，发出的消息直接记为已送达
	Widget          Kind = "widget"           // 网页聊天插件：嵌入产品网站的公共服务窗口，见 internal/widget
	Telegram        Kind = "telegram"         // Telegram 机器人（HTTP Webhook，支持单聊与群内 @）
	WeCom           Kind = "wecom"            // 企业微信智能机器人（WebSocket 长连接，支持单聊与群内 @）
	BusinessLink    Kind = "business_link"    // 成员个人网页商务沟通链接：外部联系人打开即与该成员进入长期商务沟通
	TelegramAccount Kind = "telegram_account" // 成员个人 Telegram 账号（纯 Go MTProto，支持 1:1 单聊与群聊）
)

// Kinds 是全部渠道种类，显示文字是翻译文件中 channel.kind.<种类> 的消息。
var Kinds = enum.New("channel.kind",
	enum.Item(Test), enum.Item(Widget), enum.Item(Telegram), enum.Item(WeCom),
	enum.Item(BusinessLink), enum.Item(TelegramAccount))

// Schema 让 OpenAPI 中的 Kind 只接受上面的取值，并带有显示文字。
func (Kind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Kinds) }

// Audience 是渠道的服务对象域，与传输协议 Kind 正交：决定单聊联系人归类、跨渠道会话隔离边界与默认自动化策略。
type Audience string

const (
	AudienceCustomer Audience = "customer" // 外部客户服务
	AudiencePartner  Audience = "partner"  // 商务服务与个人商务沟通
	AudienceInternal Audience = "internal" // 内部员工服务（IT、人事、行政等）
)

// Audiences 是全部渠道服务对象域，显示文字是翻译文件中 channel.audience.<域> 的消息。
var Audiences = enum.New("channel.audience",
	enum.Item(AudienceCustomer), enum.Item(AudiencePartner), enum.Item(AudienceInternal))

// Schema 让 OpenAPI 中的 Audience 只接受上面的取值，并带有显示文字。
func (Audience) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Audiences) }

// EffectiveAudience 返回规范化后的服务对象域；空值时按是否个人渠道回退为 partner 或 customer。
func (c *Channel) EffectiveAudience() Audience {
	if c == nil {
		return AudienceCustomer
	}
	if c.Audience != "" {
		return c.Audience
	}
	if c.IsPersonal() {
		return AudiencePartner
	}
	return AudienceCustomer
}

// Channel 是一个渠道，属于一个工作区。OwnerMemberID 为空时为工作区公共服务渠道，非空时为某位成员的个人商务渠道。
type Channel struct {
	bun.BaseModel `bun:"table:channels"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID      string         `bun:"tenant_id,notnull"`
	Kind          Kind           `bun:"kind,notnull"`
	Audience      Audience       `bun:"audience,notnull,default:'customer'"`
	Name          string         `bun:"name,notnull"`
	Settings      map[string]any `bun:"settings,type:jsonb,notnull"` // 连接器的设置，内容由连接器决定
	TeamID        *string        `bun:"team_id"`                     // 公共渠道新会话默认进入的团队收件箱；为空时进入未指派
	OwnerMemberID *string        `bun:"owner_member_id"`             // 个人商务渠道所属的成员 ID；公共渠道为空
	PublicCode    *string        `bun:"public_code"`                 // 网页商务链接的公开短码，用于 /c/{code} 访问；非商务链接为空
	Enabled       bool           `bun:"enabled,notnull"`
}

// IsPersonal 报告该渠道是否属于某位成员的个人商务渠道。
func (c *Channel) IsPersonal() bool {
	return c != nil && c.OwnerMemberID != nil && *c.OwnerMemberID != ""
}

// OnMemberRemoved 在成员离开工作区时停用其名下的全部个人商务渠道，避免离职成员渠道继续收发消息。
func OnMemberRemoved(ctx context.Context, tx database.Tx, tenantID, memberID string) error {
	_, err := tx.NewUpdate().Table("channels").
		Set("enabled = FALSE").
		Set("updated_at = ?", time.Now().UTC()).
		Where("tenant_id = ? AND owner_member_id = ? AND enabled = TRUE", tenantID, memberID).
		Exec(ctx)
	return errors.Wrap(err, "disable removed member channels")
}

// Summary 是接口中渠道的概要，显示在会话中。
type Summary struct {
	ID       string   `json:"id"`
	Kind     Kind     `json:"kind"`
	Audience Audience `json:"audience"`
	Name     string   `json:"name"`
}

// Summary 返回渠道 c 的概要。
func (c *Channel) Summary() Summary {
	return Summary{ID: c.ID, Kind: c.Kind, Audience: c.EffectiveAudience(), Name: c.Name}
}

// Find 读取工作区 tenant 中的渠道 id，不存在或属于别的工作区时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, tenant, id string) (*Channel, error) {
	c := new(Channel)
	err := db.NewSelect().Model(c).Where("id = ? AND tenant_id = ?", id, tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return c, errors.Wrap(err, "load channel")
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }
