// Package channel 是渠道：工作区中一个具体的接入点，例如某个网站上的聊天插件、某个 Telegram 机器人。
// 每种渠道由一个连接器收发消息，连接器实现 Connector，在入口中登记到 Registry。
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

// Kind 是渠道的种类，决定由哪个连接器收发消息。
type Kind string

const (
	Test            Kind = "test"             // 测试渠道：开发、演示和测试用，发出的消息直接记为已送达
	Widget          Kind = "widget"           // 网页聊天插件：嵌入产品网站的公共客服窗口，见 internal/widget
	Telegram        Kind = "telegram"         // Telegram 机器人公共客服私聊渠道，见 internal/telegram
	BusinessLink    Kind = "business_link"    // 成员个人网页商务沟通链接：外部商户打开即与该成员进入长期商务沟通
	TelegramAccount Kind = "telegram_account" // 成员个人 Telegram 账号（纯 Go MTProto，支持 1:1 单聊与群聊）
)

// Kinds 是全部渠道种类，显示文字是翻译文件中 channel.kind.<种类> 的消息。
var Kinds = enum.New("channel.kind", enum.Item(Test), enum.Item(Widget), enum.Item(Telegram), enum.Item(BusinessLink), enum.Item(TelegramAccount))

// Schema 让 OpenAPI 中的 Kind 只接受上面的取值，并带有显示文字。
func (Kind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Kinds) }

// Channel 是一个渠道，属于一个工作区。OwnerMemberID 为空时为工作区公共客服渠道，非空时为某位成员的个人商务渠道。
type Channel struct {
	bun.BaseModel `bun:"table:channels"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID      string         `bun:"tenant_id,notnull"`
	Kind          Kind           `bun:"kind,notnull"`
	Name          string         `bun:"name,notnull"`
	Settings      map[string]any `bun:"settings,type:jsonb,notnull"` // 连接器的设置，内容由连接器决定
	TeamID        *string        `bun:"team_id"`                     // 公共渠道新会话进入的团队收件箱；为空时进入未指派
	OwnerMemberID *string        `bun:"owner_member_id"`             // 个人商务渠道所属的成员 ID；公共客服渠道为空
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
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
}

// Summary 返回渠道 c 的概要。
func (c *Channel) Summary() Summary { return Summary{ID: c.ID, Kind: c.Kind, Name: c.Name} }

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
