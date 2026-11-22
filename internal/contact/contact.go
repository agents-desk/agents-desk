// Package contact 是联系人：和工作区沟通的外部的人，以及他们在各个渠道中的身份。
package contact

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
	"github.com/go-tenon/tenon/identity"
)

// Type 是联系人这套账号体系：客户在聊天插件等渠道中以联系人的身份调用接口，令牌绑定联系人所在的工作区。
const Type identity.SubjectType = "contact"

// ErrNotFound 是联系人不存在，或者属于别的工作区。
var ErrNotFound = errorx.Define(errorx.NotFound, "contact.not_found")

// Kind 是联系人的类型。
type Kind string

const (
	User Kind = "user" // 产品传来了身份的已登录客户
	Lead Kind = "lead" // 以访客身份发起会话的人
)

// Kinds 是联系人的全部类型，显示文字是翻译文件中 contact.kind.<类型> 的消息。
var Kinds = enum.New("contact.kind", User, Lead)

// Schema 让 OpenAPI 中的 Kind 只接受上面的取值，并带有显示文字。
func (Kind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Kinds) }

// Contact 是一个联系人，属于一个工作区。
type Contact struct {
	bun.BaseModel `bun:"table:contacts"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID       string         `bun:"tenant_id,notnull"`
	Kind           Kind           `bun:"kind,notnull"`
	Name           string         `bun:"name,notnull"`
	Email          string         `bun:"email,notnull"`
	ExternalUserID string         `bun:"external_user_id,notnull"` // 产品后端传来的用户编号；线索为空
	Language       string         `bun:"language,notnull"`         // 客户的语言：按最近几条消息判断（conversation.CustomerLanguage），或者成员手动设置的
	LanguageManual bool           `bun:"language_manual,notnull"`  // 成员手动设置了 Language：收到的消息不再改它
	Attributes     map[string]any `bun:"attributes,type:jsonb,notnull"`
}

// Summary 是接口中联系人的概要，显示在会话中。
type Summary struct {
	ID             string `json:"id"`
	Kind           Kind   `json:"kind"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	Language       string `json:"language" doc:"客户的语言：按最近几条确定了语言的消息判断，或者成员手动设置的；还没有时为空"`
	LanguageManual bool   `json:"language_manual" doc:"对方的语言是成员手动设置的：收到的消息不再改它"`
}

// Summary 返回联系人 c 的概要。
func (c *Contact) Summary() Summary {
	return Summary{ID: c.ID, Kind: c.Kind, Name: c.Name, Email: c.Email, Language: c.Language, LanguageManual: c.LanguageManual}
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

// Profile 是渠道告诉我们的联系人资料，新建联系人时使用。
type Profile struct {
	Name  string
	Email string
}

// ResolveIdentity 在事务 tx 中按渠道 channelID 和外部编号 externalID 找到联系人；还没有时新建一个线索和他的渠道身份。
// 新建时 created 为 true。
func ResolveIdentity(ctx context.Context, tx bun.IDB, tenant, channelID, externalID string, p Profile) (c *Contact, created bool, err error) {
	var id Identity
	err = tx.NewSelect().Model(&id).Where("channel_id = ? AND external_id = ?", channelID, externalID).Scan(ctx)
	switch {
	case err == nil:
		c, err = Find(ctx, tx, tenant, id.ContactID)
		return c, false, err
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, errors.Wrap(err, "load contact identity")
	}
	c = &Contact{ID: newID(), TenantID: tenant, Kind: Lead, Name: p.Name, Email: p.Email, Attributes: map[string]any{}}
	if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
		return nil, false, errors.Wrap(err, "insert contact")
	}
	id = Identity{TenantID: tenant, ChannelID: channelID, ExternalID: externalID, ContactID: c.ID, CreatedAt: now()}
	if _, err := tx.NewInsert().Model(&id).Exec(ctx); err != nil {
		return nil, false, errors.Wrap(err, "insert contact identity")
	}
	return c, true, nil
}

// ResolveUser 在事务 tx 中按产品后端传来的用户编号 userID 找到工作区 tenant 中的用户，还没有时新建一个，并确保他在渠道
// channelID 中有外部编号为 externalID 的身份。p 是产品传来的最新资料，非空的项写入联系人。
// 渠道身份已经存在时用它对应的联系人。
func ResolveUser(ctx context.Context, tx bun.IDB, tenant, channelID, externalID, userID string, p Profile) (*Contact, error) {
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
			c = &Contact{ID: newID(), TenantID: tenant, Kind: User, ExternalUserID: userID, Attributes: map[string]any{}}
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
		id = Identity{TenantID: tenant, ChannelID: channelID, ExternalID: externalID, ContactID: c.ID, CreatedAt: now()}
		if _, err := tx.NewInsert().Model(&id).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "insert contact identity")
		}
	}
	// 产品传来的资料是最新的
	var changed []string
	if p.Name != "" && p.Name != c.Name {
		c.Name, changed = p.Name, append(changed, "name")
	}
	if p.Email != "" && p.Email != c.Email {
		c.Email, changed = p.Email, append(changed, "email")
	}
	if len(changed) > 0 {
		if _, err := tx.NewUpdate().Model(c).Column(append(changed, "updated_at")...).WherePK().Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "update user profile")
		}
	}
	return c, nil
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

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// now 返回当前时间，精度与 PostgreSQL 的 timestamptz 一致。
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
