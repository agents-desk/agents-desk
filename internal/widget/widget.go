// Package widget 是网页聊天插件：嵌入产品网站的聊天窗口，渠道的种类是 channel.Widget。
//
// 管理员在工作区的设置中新建聊天插件，拿到嵌入代码和身份验证密钥。客户打开聊天窗口时经 /w/{channel}/api 建立会话
// （StartSession），得到联系人的令牌：没有登录产品的客户是线索，用浏览器保存的访客编号识别；产品后端用身份验证密钥
// 签名传来用户身份时是用户。之后客户以联系人的身份读取自己的会话和时间线、发消息、标记已读；成员的回复由连接器
// （Connector）推送到客户的实时频道 w:{channel}:contact:{contact}。
package widget

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/channel"
	"agents-desk/internal/team"
)

var (
	// ErrInvalidUserToken 是产品后端签名的用户身份无效：签名不对、已过期或者缺少用户编号。
	ErrInvalidUserToken = errorx.Define(errorx.Unauthenticated, "widget.invalid_user_token")
	// ErrTeamNotFound 是新会话进入的团队收件箱不存在，或者属于别的工作区。
	ErrTeamNotFound = errorx.Define(errorx.InvalidArgument, "widget.team_not_found")
	// ErrInvalidOrigin 是允许嵌入的网站写得不对：要写成 http:// 或 https:// 开头、只有域名（和端口）的地址。
	ErrInvalidOrigin = errorx.Define(errorx.InvalidArgument, "widget.invalid_origin")
	// ErrInvalidColor 是主题色格式无效：应为 #rrggbb 十六进制颜色值。
	ErrInvalidColor = errorx.Define(errorx.InvalidArgument, "widget.invalid_color")
	// ErrInvalidLanguage 是语言标签不是合法的 BCP 47 写法。
	ErrInvalidLanguage = errorx.Define(errorx.InvalidArgument, "widget.invalid_language")
	// ErrForbidden 是调用者无权执行该网页渠道操作。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "widget.forbidden")
	// ErrBusinessLinkInUse 是个人商务沟通链接已有聊天或联系人记录，不能删除，只能停用。
	ErrBusinessLinkInUse = errorx.Define(errorx.Conflict, "widget.business_link_in_use")
	// ErrInvalidPasscode 是打开受口令保护的商务沟通链接时输入的访问口令不正确。
	ErrInvalidPasscode = errorx.Define(errorx.PermissionDenied, "widget.invalid_passcode")
	// ErrBusinessLinkExpired 是商务沟通链接已超过有效期，不再接受新访客进入。
	ErrBusinessLinkExpired = errorx.Define(errorx.Conflict, "widget.business_link_expired")
	// ErrIntakeRequired 是开场留资表单中有必填项尚未填写。
	ErrIntakeRequired = errorx.Define(errorx.InvalidArgument, "widget.intake_required")
	// ErrUploadsDisabled 是管理员已关闭当前工作区的访客附件上传能力。
	ErrUploadsDisabled = errorx.Define(errorx.PermissionDenied, "widget.uploads_disabled")
)

// Appearance 是聊天窗口的纯视觉外观配置，持久化在渠道设置中并暴露给公开配置与预览协议。
type Appearance struct {
	Color Color `json:"color,omitempty" doc:"聊天窗口的主题色（#rrggbb 十六进制颜色值），缺省为 #0063ff"`
}

// IntakeFieldMode 是开场留资表单中单个字段的展示与校验模式：off（隐藏）、optional（选填）、required（必填）。
type IntakeFieldMode string

const (
	IntakeOff      IntakeFieldMode = "off"
	IntakeOptional IntakeFieldMode = "optional"
	IntakeRequired IntakeFieldMode = "required"
)

// BusinessCardConfig 是网页商务沟通链接的对外名片展示开关；名片上的多语言文本覆写位于 Localization.Translations。
type BusinessCardConfig struct {
	ShowOwner bool `json:"show_owner" doc:"是否在聊天窗口顶部展示归属成员的头像、姓名、头衔与简介"`
}

// BusinessIntakeConfig 是网页商务沟通链接的开场留资表单配置。
type BusinessIntakeConfig struct {
	Enabled bool            `json:"enabled" doc:"是否开启开场留资表单"`
	Name    IntakeFieldMode `json:"name,omitempty" enum:"off,optional,required" doc:"称呼/姓名字段模式"`
	Company IntakeFieldMode `json:"company,omitempty" enum:"off,optional,required" doc:"公司/组织字段模式"`
	Email   IntakeFieldMode `json:"email,omitempty" enum:"off,optional,required" doc:"邮箱字段模式"`
	Note    IntakeFieldMode `json:"note,omitempty" enum:"off,optional,required" doc:"合作意向/备注字段模式"`
}

// BusinessAccessStored 是保存在 channels.settings 中的网页商务链接访问控制配置（含口令哈希，不直接对外暴露）。
type BusinessAccessStored struct {
	Mode         string     `json:"mode,omitempty" enum:"public,passcode"`
	PasscodeHash string     `json:"passcode_hash,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// Settings 是聊天插件与网页商务沟通链接保存在渠道设置（channels.settings）中的内容。
type Settings struct {
	Appearance     Appearance             `json:"appearance"`         // 聊天窗口的纯视觉外观配置
	Localization   Localization           `json:"localization"`       // 渠道语言策略与多语言自定义文案
	AllowedOrigins []string               `json:"allowed_origins"`    // 允许嵌入聊天窗口的网站，形如 https://shop.example.com；为空表示所有网站
	IdentitySecret string                 `json:"identity_secret"`    // 身份验证密钥，用应用密钥加密保存；产品后端用明文签名用户身份
	Business       channel.BusinessPolicy `json:"business,omitempty"` // 商务沟通链接通用的协作、标签与翻译策略
	Card           *BusinessCardConfig    `json:"card,omitempty"`     // 网页商务沟通链接的名片展示开关
	Intake         *BusinessIntakeConfig  `json:"intake,omitempty"`   // 网页商务沟通链接的开场留资表单配置
	Access         *BusinessAccessStored  `json:"access,omitempty"`   // 网页商务沟通链接的访问口令与有效期配置
}

// settingsOf 读出渠道 ch 中聊天插件的设置。
func settingsOf(ch *channel.Channel) (Settings, error) {
	var s Settings
	raw, err := json.Marshal(ch.Settings)
	if err != nil {
		return s, errors.Wrap(err, "encode widget settings")
	}
	return s, errors.Wrap(json.Unmarshal(raw, &s), "decode widget settings")
}

// toMap 把聊天插件的设置转换为渠道设置的形式。
func (s Settings) toMap() (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, errors.Wrap(err, "encode widget settings")
	}
	var m map[string]any
	return m, errors.Wrap(json.Unmarshal(raw, &m), "decode widget settings")
}

// newSecret 返回新的身份验证密钥：32 字节随机数的 base64url，产品后端把它作为 HS256 的密钥。
func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read 不会失败
	return base64.RawURLEncoding.EncodeToString(b)
}

// newVisitorID 返回新的访客编号：128 位随机数的 base64url。浏览器保存它，下次打开聊天窗口时带上；
// 知道编号就能读到这位访客的会话，所以它和令牌一样不可猜测。
func newVisitorID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// find 读取工作区 tenant 中的聊天插件 id，不存在、属于别的工作区或者不是聊天插件时返回 channel.ErrNotFound。
// lock 为 true 时锁住这一行。
func find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*channel.Channel, error) {
	ch := new(channel.Channel)
	q := db.NewSelect().Model(ch).Where("id = ? AND tenant_id = ? AND kind = ? AND archived_at IS NULL", id, tenant, channel.Widget)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load widget")
}

// findPublic 按 ID 读取客户或外部商户在用的网页聊天渠道（公共聊天插件或成员个人商务沟通链接）：渠道启用、所在工作区未停用。
// 否则返回 channel.ErrNotFound，不透露渠道是否存在。
func findPublic(ctx context.Context, db bun.IDB, id string) (*channel.Channel, error) {
	ch := new(channel.Channel)
	err := db.NewSelect().Model(ch).
		Where("channel.id = ? AND channel.kind IN (?) AND channel.enabled", id, bun.In([]channel.Kind{channel.Widget, channel.BusinessLink})).
		Where("EXISTS (SELECT 1 FROM tenants t WHERE t.id = channel.tenant_id AND t.disabled_at IS NULL)").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load widget")
}

// checkTeam 检查团队收件箱 id 属于工作区 tenant 且未停用：不存在时返回 ErrTeamNotFound，已停用时返回 team.ErrDisabled；id 为空时不检查。
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

// normalizeOrigins 把管理员填写的允许嵌入的网站整理成 scheme://host[:port] 的小写形式，去掉重复的，按填写的顺序返回。
// 有一项不是 http 或 https 的地址、或者带了路径、查询参数时返回 ErrInvalidOrigin，带上这一项。
func normalizeOrigins(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range in {
		u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, ErrInvalidOrigin.New(errorx.Field("allowed_origins"), errorx.Public("origin", raw))
		}
		origin := strings.ToLower(u.Scheme + "://" + u.Host)
		if !seen[origin] {
			seen[origin] = true
			out = append(out, origin)
		}
	}
	return out, nil
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }
