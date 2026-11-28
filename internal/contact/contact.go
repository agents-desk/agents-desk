// Package contact 是联系人：和工作区沟通的外部的人，以及他们在各个渠道中的身份。
package contact

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/attribute"
	"agents-desk/internal/lang"
	"agents-desk/internal/organization"
	"agents-desk/internal/tag"
)

// Type 是联系人这套账号体系：客户在聊天插件等渠道中以联系人的身份调用接口，令牌绑定联系人所在的工作区。
const Type identity.SubjectType = "contact"

var (
	// ErrNotFound 是联系人不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "contact.not_found")
	// ErrInvalidEmail 是联系人邮箱格式不正确。
	ErrInvalidEmail = errorx.Define(errorx.InvalidArgument, "contact.invalid_email")
	// ErrInvalidLanguage 是联系人语言不是合法的 BCP 47 标签。
	ErrInvalidLanguage = errorx.Define(errorx.InvalidArgument, "contact.invalid_language")
	// ErrInvalidTimeZone 是联系人时区不是认识的 IANA 时区名。
	ErrInvalidTimeZone = errorx.Define(errorx.InvalidArgument, "contact.invalid_time_zone")
	// ErrForbidden 是调用者不能修改这位联系人。
	ErrForbidden = errorx.Define(errorx.PermissionDenied, "contact.forbidden")
	// ErrNotLead 是已登录用户的邮箱由产品后端签名提供，不能通过访客表单修改。
	ErrNotLead = errorx.Define(errorx.PermissionDenied, "contact.not_lead")
	// ErrMergeSelf 是不能把联系人合并到他自己。
	ErrMergeSelf = errorx.Define(errorx.InvalidArgument, "contact.merge_self")
	// ErrMergeConflict 是两个联系人绑定了不同的业务用户编号，不能直接合并。
	ErrMergeConflict = errorx.Define(errorx.Conflict, "contact.merge_conflict")
	// ErrMemberTaken 是目标工作区成员已经绑定了另一个内部联系人。
	ErrMemberTaken = errorx.Define(errorx.Conflict, "contact.member_taken")
	// ErrBlocked 是该联系人已被拉黑，禁止再接入会话。
	ErrBlocked = errorx.Define(errorx.PermissionDenied, "contact.blocked")
)

// Kind 是联系人的类型。
type Kind string

const (
	User     Kind = "user"     // 产品传来了身份的已登录外部客户
	Lead     Kind = "lead"     // 以访客身份发起会话的外部客户
	Internal Kind = "internal" // 经内部服务机器人单聊发起请求的内部联系人（员工），可关联工作区成员
)

// Kinds 是联系人的全部类型，显示文字是翻译文件中 contact.kind.<类型> 的消息。
var Kinds = enum.New("contact.kind", enum.Item(User), enum.Item(Lead), enum.Item(Internal))

// Schema 让 OpenAPI 中的 Kind 只接受上面的取值，并带有显示文字。
func (Kind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Kinds) }

// Contact 是一个联系人，属于一个工作区。
type Contact struct {
	bun.BaseModel `bun:"table:contacts"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID       string         `bun:"tenant_id,notnull"`
	Number         int64          `bun:"number,notnull"` // 联系人在工作区内的递增编号，从 1 开始，创建后不变
	Kind           Kind           `bun:"kind,notnull"`
	MemberID       *string        `bun:"member_id"`       // 可选：内部联系人关联的工作区成员 ID
	OrganizationID *string        `bun:"organization_id"` // 可选：关联的客户公司/组织 ID
	Name           string         `bun:"name,notnull"`
	Company        string         `bun:"company,notnull"` // 联系人所属公司或组织名称
	Email          string         `bun:"email,notnull"`
	ExternalUserID string         `bun:"external_user_id,notnull"` // 产品后端传来的用户编号；线索为空
	Language       string         `bun:"language,notnull"`         // 客户的语言：按最近几条消息判断（conversation.PeerLanguage），或者成员手动设置的
	LanguageManual bool           `bun:"language_manual,notnull"`  // 成员手动设置了 Language：收到的消息不再改它
	Attributes     map[string]any `bun:"attributes,type:jsonb,notnull"`
	Notes          string         `bun:"notes,notnull"`                    // 成员填写的联系人备注，跨会话持久可见
	NotesDerived   map[string]any `bun:"notes_derived,type:jsonb,notnull"` // 联系人备注的派生翻译数据（源语言与按目标语言缓存的译文）
	Location       string         `bun:"location,notnull"`                 // 所在地，例如“上海”“Mumbai”
	TimeZone       string         `bun:"time_zone,notnull"`                // IANA 时区名，例如 Asia/Shanghai，用于显示对方当地时间
	Referrer       string         `bun:"referrer,notnull"`                 // 首次访问的来源页面地址
	LastSeenAt     *time.Time     `bun:"last_seen_at"`                     // 最近活跃或联系的时间
	BlockedAt      *time.Time     `bun:"blocked_at"`                       // 拉黑时间，非空表示已拉黑
	BlockedReason  string         `bun:"blocked_reason,notnull"`           // 拉黑原因说明
}

// Summary 是接口中联系人的概要，显示在会话中和聊天插件的会话响应里，包含可向客户展示的字段。
type Summary struct {
	ID             string                `json:"id"`
	Number         int64                 `json:"number" doc:"联系人在工作区内的递增编号，从 1 开始"`
	Kind           Kind                  `json:"kind"`
	MemberID       *string               `json:"member_id,omitempty" doc:"内部联系人关联的工作区成员 ID"`
	OrganizationID *string               `json:"organization_id,omitempty" doc:"关联的客户公司/组织 ID"`
	Organization   *organization.Summary `json:"organization,omitempty" doc:"关联的客户公司/组织概要"`
	Name           string                `json:"name"`
	Company        string                `json:"company,omitempty" doc:"所属公司或组织名称"`
	Email          string                `json:"email"`
	Language       string                `json:"language" doc:"对方的语言：按最近几条确定了语言的消息判断，或者成员手动设置的；还没有时为空"`
	LanguageManual bool                  `json:"language_manual" doc:"对方的语言是成员手动设置的：收到的消息不再改它"`
	Location       string                `json:"location,omitempty" doc:"所在地"`
	TimeZone       string                `json:"time_zone,omitempty" doc:"IANA 时区名，用于显示对方的当地时间"`
	BlockedAt      *time.Time            `json:"blocked_at,omitempty" doc:"拉黑时间；非空表示已拉黑"`
	BlockedReason  string                `json:"blocked_reason,omitempty" doc:"拉黑原因说明"`
	Tags           []tag.Summary         `json:"tags" doc:"联系人上的标签"`
	Attributes     map[string]any        `json:"attributes" doc:"联系人的自定义属性"`
}

// Summary 返回联系人 c 的概要。
func (c *Contact) Summary() Summary {
	attrs := c.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	return Summary{
		ID: c.ID, Number: c.Number, Kind: c.Kind, MemberID: c.MemberID, OrganizationID: c.OrganizationID, Name: c.Name, Company: c.Company, Email: c.Email,
		Language: c.Language, LanguageManual: c.LanguageManual,
		Location: c.Location, TimeZone: c.TimeZone,
		BlockedAt: c.BlockedAt, BlockedReason: c.BlockedReason,
		Tags: []tag.Summary{}, Attributes: attrs,
	}
}

// Identity 是联系人在一个渠道中的身份：同一个渠道中，一个外部编号对应一个联系人。
type Identity struct {
	bun.BaseModel `bun:"table:contact_identities"`

	TenantID   string    `bun:"tenant_id,notnull"`
	ChannelID  string    `bun:"channel_id,pk"`
	ExternalID string    `bun:"external_id,pk"`
	ContactID  string    `bun:"contact_id,notnull"`
	CreatedAt  time.Time `bun:"created_at,notnull"`
}

// Profile 是渠道告诉我们的联系人资料，新建或更新联系人时使用。Language、TimeZone 和 Referrer 由浏览器自动上报，不合法时忽略。
type Profile struct {
	Name     string
	Company  string
	Email    string
	Language string
	TimeZone string
	Referrer string
}

// ResolveIdentity 在事务 tx 中按渠道 channelID 和外部编号 externalID 找到联系人；还没有时新建一个线索和他的渠道身份。
// 新建时 created 为 true；已存在时按 p 补全时区和来源页面，并刷新最近活跃时间。
func ResolveIdentity(ctx context.Context, tx bun.IDB, tenant, channelID, externalID string, p Profile) (c *Contact, created bool, err error) {
	return ResolveIdentityWithKind(ctx, tx, tenant, channelID, externalID, Lead, p)
}

// ResolveIdentityWithKind 在事务 tx 中按渠道 channelID 和外部编号 externalID 找到联系人；
// 还没有时按 defaultKind（如 Lead 或 Internal）新建联系人和他的渠道身份。
func ResolveIdentityWithKind(ctx context.Context, tx bun.IDB, tenant, channelID, externalID string, defaultKind Kind, p Profile) (c *Contact, created bool, err error) {
	if defaultKind == "" {
		defaultKind = Lead
	}
	at := database.Now()
	var id Identity
	err = tx.NewSelect().Model(&id).Where("channel_id = ? AND external_id = ?", channelID, externalID).Scan(ctx)
	switch {
	case err == nil:
		c, err = Find(ctx, tx, tenant, id.ContactID)
		if err != nil {
			return nil, false, err
		}
		if err := applySessionMeta(ctx, tx, c, p, at); err != nil {
			return nil, false, err
		}
		return c, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, errors.Wrap(err, "load contact identity")
	}
	num, err := nextNumber(ctx, tx, tenant)
	if err != nil {
		return nil, false, err
	}
	c = &Contact{
		ID: database.NewID(), TenantID: tenant, Number: num, Kind: defaultKind,
		Name: strings.TrimSpace(p.Name), Company: strings.TrimSpace(p.Company), Email: strings.TrimSpace(p.Email),
		Language: sanitizeLanguage(p.Language),
		TimeZone: sanitizeTimeZone(p.TimeZone), Referrer: sanitizeReferrer(p.Referrer),
		LastSeenAt: &at, Attributes: map[string]any{}, NotesDerived: map[string]any{},
	}
	if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
		return nil, false, errors.Wrap(err, "insert contact")
	}
	id = Identity{TenantID: tenant, ChannelID: channelID, ExternalID: externalID, ContactID: c.ID, CreatedAt: at}
	if _, err := tx.NewInsert().Model(&id).Exec(ctx); err != nil {
		return nil, false, errors.Wrap(err, "insert contact identity")
	}
	return c, true, nil
}

// ResolveUser 在事务 tx 中按产品后端传来的用户编号 userID 找到工作区 tenant 中的用户，还没有时新建一个，并确保他在渠道
// channelID 中有外部编号为 externalID 的身份。p 是产品传来的最新资料，非空的项写入联系人；渠道身份已经存在时用它对应的联系人。
func ResolveUser(ctx context.Context, tx bun.IDB, tenant, channelID, externalID, userID string, p Profile) (*Contact, error) {
	at := database.Now()
	var id Identity
	err := tx.NewSelect().Model(&id).Where("channel_id = ? AND external_id = ?", channelID, externalID).Scan(ctx)
	var c *Contact
	switch {
	case err == nil:
		if c, err = Find(ctx, tx, tenant, id.ContactID); err != nil {
			return nil, err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, errors.Wrap(err, "load contact identity")
	default:
		c = new(Contact)
		err = tx.NewSelect().Model(c).Where("tenant_id = ? AND external_user_id = ?", tenant, userID).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			num, err := nextNumber(ctx, tx, tenant)
			if err != nil {
				return nil, err
			}
			c = &Contact{
				ID: database.NewID(), TenantID: tenant, Number: num, Kind: User, ExternalUserID: userID,
				Language: sanitizeLanguage(p.Language),
				TimeZone: sanitizeTimeZone(p.TimeZone), Referrer: sanitizeReferrer(p.Referrer),
				LastSeenAt: &at, Attributes: map[string]any{}, NotesDerived: map[string]any{},
			}
			if _, err := tx.NewInsert().Model(c).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
				return nil, errors.Wrap(err, "insert user")
			}
			// 同一个用户同时第一次进来时另一个请求先插入了，读出它
			if err := tx.NewSelect().Model(c).Where("tenant_id = ? AND external_user_id = ?", tenant, userID).Scan(ctx); err != nil {
				return nil, errors.Wrap(err, "load user")
			}
		} else if err != nil {
			return nil, errors.Wrap(err, "load user")
		}
		id = Identity{TenantID: tenant, ChannelID: channelID, ExternalID: externalID, ContactID: c.ID, CreatedAt: at}
		if _, err := tx.NewInsert().Model(&id).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "insert contact identity")
		}
	}
	// 产品传来的资料是最新的；时区按最新上报更新，来源页面只在空时补齐
	var changed []string
	if p.Name != "" && p.Name != c.Name {
		c.Name, changed = p.Name, append(changed, "name")
	}
	if p.Email != "" && p.Email != c.Email {
		c.Email, changed = p.Email, append(changed, "email")
	}
	if tz := sanitizeTimeZone(p.TimeZone); tz != "" && tz != c.TimeZone {
		c.TimeZone, changed = tz, append(changed, "time_zone")
	}
	if l := sanitizeLanguage(p.Language); l != "" && !c.LanguageManual && c.Language == "" {
		c.Language, changed = l, append(changed, "language")
	}
	if ref := sanitizeReferrer(p.Referrer); ref != "" && c.Referrer == "" {
		c.Referrer, changed = ref, append(changed, "referrer")
	}
	c.LastSeenAt, changed = &at, append(changed, "last_seen_at")
	if _, err := tx.NewUpdate().Model(c).Column(append(changed, "updated_at")...).WherePK().Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "update user profile")
	}
	return c, nil
}

// LinkUser 在事务 tx 中把渠道 channelID 里的外部身份 externalID 关联到工作区 tenant 的产品用户 userID：
//   - 渠道身份尚未存在时，按 userID 查找或新建用户并绑定该渠道身份；
//   - 渠道身份已存在且原为线索时，查找或新建目标用户，调用可选 beforeMerge 钩子归并双方活跃会话后，再调用 MergeLead 将线索及其历史会话、渠道身份一并迁到用户名下；
//   - 渠道身份已属于用户时直接更新资料。
//
// 返回关联后的用户，以及合并线索时 contact_id 发生变化的会话 ID 列表，供调用方通知成员刷新。
func LinkUser(ctx context.Context, tx database.Tx, tenant, channelID, externalID, userID string, p Profile, beforeMerge ...BeforeMergeHook) (*Contact, []string, error) {
	var id Identity
	err := tx.NewSelect().Model(&id).Where("channel_id = ? AND external_id = ?", channelID, externalID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		c, err := ResolveUser(ctx, tx, tenant, channelID, externalID, userID, p)
		return c, nil, err
	}
	if err != nil {
		return nil, nil, errors.Wrap(err, "load contact identity")
	}
	existing, err := Find(ctx, tx, tenant, id.ContactID)
	if err != nil {
		return nil, nil, err
	}
	if existing.Kind != Lead {
		c, err := ResolveUser(ctx, tx, tenant, channelID, externalID, userID, p)
		return c, nil, err
	}
	// 原渠道身份挂在线索下：先查找或新建对应的产品用户，再把线索（含渠道身份和历史会话）合并到用户名下。
	at := database.Now()
	u := new(Contact)
	err = tx.NewSelect().Model(u).Where("tenant_id = ? AND external_user_id = ?", tenant, userID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		num, err := nextNumber(ctx, tx, tenant)
		if err != nil {
			return nil, nil, err
		}
		u = &Contact{
			ID: database.NewID(), TenantID: tenant, Number: num, Kind: User, ExternalUserID: userID,
			Name: p.Name, Email: p.Email,
			Language: sanitizeLanguage(p.Language),
			TimeZone: sanitizeTimeZone(p.TimeZone), Referrer: sanitizeReferrer(p.Referrer),
			LastSeenAt: &at, Attributes: map[string]any{}, NotesDerived: map[string]any{},
		}
		if _, err := tx.NewInsert().Model(u).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "insert user")
		}
		if err := tx.NewSelect().Model(u).Where("tenant_id = ? AND external_user_id = ?", tenant, userID).Scan(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "load user")
		}
	} else if err != nil {
		return nil, nil, errors.Wrap(err, "load user")
	} else {
		var changed []string
		if p.Name != "" && p.Name != u.Name {
			u.Name, changed = p.Name, append(changed, "name")
		}
		if p.Email != "" && p.Email != u.Email {
			u.Email, changed = p.Email, append(changed, "email")
		}
		u.LastSeenAt, changed = &at, append(changed, "last_seen_at")
		if _, err := tx.NewUpdate().Model(u).Column(append(changed, "updated_at")...).WherePK().Exec(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "update user profile")
		}
	}
	// 先按固定锁序锁住两个联系人，再归并双活服务型会话，避免与会话模块形成 AB-BA 死锁。
	if err := LockPair(ctx, tx, tenant, u.ID, existing.ID); err != nil {
		return nil, nil, err
	}
	if len(beforeMerge) > 0 && beforeMerge[0] != nil {
		if err := beforeMerge[0](ctx, tx, u.ID, existing.ID); err != nil {
			return nil, nil, err
		}
	}
	return MergeLead(ctx, tx, tenant, existing.ID, u.ID, "", "")
}

// applySessionMeta 把聊天插件上报的时区、来源页面以及缺失的基础资料写入已有联系人 c，并刷新最近活跃时间。
func applySessionMeta(ctx context.Context, tx bun.IDB, c *Contact, p Profile, at time.Time) error {
	var changed []string
	if name := strings.TrimSpace(p.Name); name != "" && c.Name == "" {
		c.Name, changed = name, append(changed, "name")
	}
	if company := strings.TrimSpace(p.Company); company != "" && c.Company == "" {
		c.Company, changed = company, append(changed, "company")
	}
	if email := strings.TrimSpace(p.Email); email != "" && c.Email == "" {
		c.Email, changed = email, append(changed, "email")
	}
	if tz := sanitizeTimeZone(p.TimeZone); tz != "" && tz != c.TimeZone {
		c.TimeZone, changed = tz, append(changed, "time_zone")
	}
	if l := sanitizeLanguage(p.Language); l != "" && !c.LanguageManual && c.Language == "" {
		c.Language, changed = l, append(changed, "language")
	}
	if ref := sanitizeReferrer(p.Referrer); ref != "" && c.Referrer == "" {
		c.Referrer, changed = ref, append(changed, "referrer")
	}
	c.LastSeenAt, changed = &at, append(changed, "last_seen_at")
	_, err := tx.NewUpdate().Model(c).Column(append(changed, "updated_at")...).WherePK().Exec(ctx)
	return errors.Wrap(err, "update contact session meta")
}

// TouchSeen 在事务 tx 中把联系人 id 的最近活跃时间更新为 at（不倒退）。
func TouchSeen(ctx context.Context, tx bun.IDB, tenant, id string, at time.Time) error {
	_, err := tx.NewUpdate().Model((*Contact)(nil)).
		Set("last_seen_at = ?", at).
		Where("id = ? AND tenant_id = ? AND (last_seen_at IS NULL OR last_seen_at < ?)", id, tenant, at).
		Exec(ctx)
	return errors.Wrap(err, "touch contact last_seen_at")
}

// Find 读取工作区 tenant 中的联系人 id，不存在或属于别的工作区时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, tenant, id string) (*Contact, error) {
	c := new(Contact)
	err := db.NewSelect().Model(c).Where("id = ? AND tenant_id = ?", id, tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return c, errors.Wrap(err, "load contact")
}

// VisibleByIDs 返回 ids 中属于 sub 所在工作区且 sub 有权查看的联系人记录（供补译校验可见性）。
func VisibleByIDs(ctx context.Context, db bun.IDB, sub identity.Subject, ids []string) ([]Contact, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []Contact
	err := visible(
		db.NewSelect().Model(&out).Where("contact.tenant_id = ? AND contact.id IN (?)", sub.Tenant, bun.In(ids)),
		sub,
	).Scan(ctx)
	return out, errors.Wrap(err, "load visible contacts by ids")
}

// SaveNotesTranslation 合并保存联系人 contactID 长期备注的源语言与多语言译文到 contacts.notes_derived。
func SaveNotesTranslation(ctx context.Context, db *bun.DB, tenantID, contactID, source string, translations map[string]string) error {
	return database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		c := new(Contact)
		err := tx.NewSelect().Model(c).Where("id = ? AND tenant_id = ?", contactID, tenantID).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "load contact for notes translation")
		}
		if strings.TrimSpace(c.Notes) == "" {
			return nil
		}
		if c.NotesDerived == nil {
			c.NotesDerived = map[string]any{}
		}
		changed := false
		if cur, _ := c.NotesDerived["language"].(string); cur == "" && source != "" {
			c.NotesDerived["language"] = source
			changed = true
		}
		trMap, _ := c.NotesDerived["translations"].(map[string]any)
		if trMap == nil {
			trMap = map[string]any{}
		}
		for k, v := range translations {
			if prev, _ := trMap[k].(string); prev != v {
				trMap[k] = v
				changed = true
			}
		}
		if len(trMap) > 0 {
			c.NotesDerived["translations"] = trMap
		}
		if !changed {
			return nil
		}
		_, err = tx.NewUpdate().Model(c).Column("notes_derived").WherePK().Exec(ctx)
		return errors.Wrap(err, "save contact notes translation")
	})
}

// validTimeZone 检查 s 是否是有效的 IANA 时区名（不接受 Go 特有的 "Local"）。
func validTimeZone(s string) bool {
	if s == "" || s == "Local" || len(s) > 64 {
		return false
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

// sanitizeLanguage 校验浏览器或渠道上报的冷启动语言标签，合法时返回规范 BCP 47 写法，否则返回空字符串。
func sanitizeLanguage(s string) string {
	c, ok := lang.Canonical(s)
	if !ok {
		return ""
	}
	return c
}

// sanitizeTimeZone 返回合法的 IANA 时区名，不合法时返回空字符串。
func sanitizeTimeZone(s string) string {
	s = strings.TrimSpace(s)
	if !validTimeZone(s) {
		return ""
	}
	return s
}

// sanitizeReferrer 校验浏览器上报的来源页面地址：只接受 http 或 https 且长度不超过 2048，否则返回空字符串。
func sanitizeReferrer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 2048 {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

// AddTag 在事务 tx 中给工作区 tenant 的联系人 contactID 添加标签 tagID。
// 标签或联系人不存在时返回对应的 ErrNotFound；已经打过该标签时 added 为 false。
func AddTag(ctx context.Context, tx bun.IDB, tenant, contactID, tagID string) (*tag.Tag, bool, error) {
	if _, err := Find(ctx, tx, tenant, contactID); err != nil {
		return nil, false, err
	}
	t, err := tag.Find(ctx, tx, tenant, tagID, false)
	if err != nil {
		return nil, false, err
	}
	res, err := tx.NewRaw(`INSERT INTO contact_tags (tenant_id, contact_id, tag_id, created_at)
		VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, tenant, contactID, tagID, database.Now()).Exec(ctx)
	if err != nil {
		return nil, false, errors.Wrap(err, "add contact tag")
	}
	n, err := res.RowsAffected()
	return t, n > 0, errors.Wrap(err, "count added contact tag")
}

// RemoveTag 在事务 tx 中从工作区 tenant 的联系人 contactID 上移除标签 tagID。
// 标签或联系人不存在时返回对应的 ErrNotFound；联系人原本没有该标签时 removed 为 false。
func RemoveTag(ctx context.Context, tx bun.IDB, tenant, contactID, tagID string) (*tag.Tag, bool, error) {
	if _, err := Find(ctx, tx, tenant, contactID); err != nil {
		return nil, false, err
	}
	t, err := tag.Find(ctx, tx, tenant, tagID, false)
	if err != nil {
		return nil, false, err
	}
	res, err := tx.NewRaw(`DELETE FROM contact_tags WHERE tenant_id = ? AND contact_id = ? AND tag_id = ?`,
		tenant, contactID, tagID).Exec(ctx)
	if err != nil {
		return nil, false, errors.Wrap(err, "remove contact tag")
	}
	n, err := res.RowsAffected()
	return t, n > 0, errors.Wrap(err, "count removed contact tag")
}

// PatchAttributes 在事务 tx 中锁住工作区 tenant 的联系人 contactID，按属性定义校验并合并 patch 到联系人属性。
// 返回实际发生变化的属性列表；没有变化时不更新数据库。
func PatchAttributes(ctx context.Context, tx bun.IDB, tenant, contactID string, patch map[string]any) ([]attribute.Change, error) {
	c := new(Contact)
	err := tx.NewSelect().Model(c).Where("id = ? AND tenant_id = ?", contactID, tenant).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load contact")
	}
	next, changes, err := attribute.ApplyPatch(ctx, tx, tenant, attribute.TargetContact, c.Attributes, patch)
	if err != nil || len(changes) == 0 {
		return changes, err
	}
	c.Attributes = next
	if _, err := tx.NewUpdate().Model(c).Column("attributes", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "update contact attributes")
	}
	return changes, nil
}

// nextNumber 在事务 tx 中原子递增工作区 tenant 的联系人计数器，返回本次分配的递增编号。
func nextNumber(ctx context.Context, tx bun.IDB, tenant string) (int64, error) {
	var n int64
	err := tx.NewRaw(
		`UPDATE tenants SET next_contact_number = next_contact_number + 1 WHERE id = ? RETURNING next_contact_number - 1`,
		tenant,
	).Scan(ctx, &n)
	return n, errors.Wrap(err, "allocate contact number")
}
