// Package business 管理工作区级的商务沟通默认策略、配额检查以及管理员视角的全员商务渠道治理。
package business

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/channel"
	"agents-desk/internal/flags"
	"agents-desk/internal/tgaccount"
)

var (
	// ErrFeatureDisabled 表示该商务渠道功能在平台或工作区层面已关闭。
	ErrFeatureDisabled = errorx.Define(errorx.Conflict, "business.feature_disabled")
	// ErrQuotaExceeded 表示当前成员的商务链接或 Telegram 个人号数量已达到工作区配额上限。
	ErrQuotaExceeded = errorx.Define(errorx.Conflict, "business.quota_exceeded")
	// ErrInvalidTone 表示商务默认翻译语气取值无效。
	ErrInvalidTone = errorx.Define(errorx.InvalidArgument, "business.invalid_tone")
	// ErrInvalidMember 表示指定的协作成员不存在或不属于当前工作区。
	ErrInvalidMember = errorx.Define(errorx.InvalidArgument, "business.invalid_member")
)

// Policy 是工作区级的商务沟通策略与配额设置，保存在 Tenon settings 表中（按租户隔离）。
// 字段不带 omitempty，确保读取和保存完整覆盖。
type Policy struct {
	LinksEnabled                 bool   `json:"links_enabled" doc:"是否允许成员创建个人网页商务沟通链接"`
	TelegramAccountsEnabled      bool   `json:"telegram_accounts_enabled" doc:"是否允许成员绑定 Telegram 个人号开展商务沟通"`
	MaxLinksPerMember            int    `json:"max_links_per_member" minimum:"0" maximum:"100" doc:"每位成员最多可创建的商务链接数，0 表示不限制"`
	MaxTelegramAccountsPerMember int    `json:"max_telegram_accounts_per_member" minimum:"0" maximum:"20" doc:"每位成员最多可绑定的 Telegram 个人号数，0 表示不限制"`
	DefaultRequireIntake         bool   `json:"default_require_intake" doc:"新建网页商务链接时是否默认开启开场留资表单"`
	DefaultCompanyName           string `json:"default_company_name" maxLength:"80" doc:"商务名片对外展示的公司或品牌名称；留空时默认取工作区名称"`
	DefaultTranslationTone       string `json:"default_translation_tone" enum:",friendly,neutral,formal" doc:"商务沟通默认翻译语气；留空表示继承工作区通用翻译语气"`
}

// PolicyDefaults 是工作区商务沟通策略的默认值。
var PolicyDefaults = Policy{
	LinksEnabled:                 true,
	TelegramAccountsEnabled:      true,
	MaxLinksPerMember:            10,
	MaxTelegramAccountsPerMember: 5,
	DefaultRequireIntake:         false,
	DefaultCompanyName:           "",
	DefaultTranslationTone:       "formal",
}

// NewPolicyGroup 返回工作区商务策略存储组（键名 business.policy，发布后保持固定）。
func NewPolicyGroup(db bun.IDB) *settings.Group[Policy] {
	return settings.New(db, "business.policy", PolicyDefaults)
}

// PolicyViewDTO 是返回给工作区前端的商务策略视图，同时附带平台开关状态、本工作区 Telegram API 配置状态与当前成员用量统计。
type PolicyViewDTO struct {
	Policy
	PlatformLinksEnabled    bool `json:"platform_links_enabled" doc:"平台级功能开关是否开启网页商务链接"`
	PlatformTelegramEnabled bool `json:"platform_telegram_enabled" doc:"平台级功能开关是否开启 Telegram 个人号"`
	TelegramAPIConfigured   bool `json:"telegram_api_configured" doc:"当前工作区管理员是否已配置 Telegram MTProto API 凭据"`
	MyLinksCount            int  `json:"my_links_count" doc:"当前成员已创建的网页商务链接数量"`
	MyTelegramCount         int  `json:"my_telegram_count" doc:"当前成员已绑定的 Telegram 个人号数量"`
}

// PolicyOutput 是商务策略的 HTTP 响应。
type PolicyOutput struct {
	Body PolicyViewDTO
}

// SavePolicyInput 是保存工作区商务策略的 HTTP 请求。
type SavePolicyInput struct {
	Body Policy
}

// PolicyGuard 封装平台开关、工作区策略与单成员配额校验，供网页商务链接与 Telegram 个人号创建入口复用。
type PolicyGuard struct {
	db       *bun.DB
	policy   *settings.Group[Policy]
	features *feature.Store
	tgCreds  *settings.Group[tgaccount.Credentials]
}

// NewPolicyGuard 返回工作区商务策略校验器。
func NewPolicyGuard(db *bun.DB, policy *settings.Group[Policy], features *feature.Store, tgCreds *settings.Group[tgaccount.Credentials]) *PolicyGuard {
	return &PolicyGuard{db: db, policy: policy, features: features, tgCreds: tgCreds}
}

// CheckCreateLink 校验成员 sub 是否允许在当前工作区继续创建网页商务沟通链接。
func (g *PolicyGuard) CheckCreateLink(ctx context.Context, sub identity.Subject) error {
	if g == nil {
		return nil
	}
	if g.features != nil && !g.features.Active(ctx, sub, flags.BusinessLinks) {
		return ErrFeatureDisabled.New()
	}
	if g.policy == nil {
		return nil
	}
	p, err := g.policy.Tenant(sub.Tenant).Get(ctx)
	if err != nil {
		return errors.Wrap(err, "load business policy")
	}
	if !p.LinksEnabled {
		return ErrFeatureDisabled.New()
	}
	if p.MaxLinksPerMember > 0 {
		count, err := g.db.NewSelect().Model((*channel.Channel)(nil)).
			Where("tenant_id = ? AND kind = ? AND owner_member_id = ?", sub.Tenant, channel.BusinessLink, sub.ID).
			Count(ctx)
		if err != nil {
			return errors.Wrap(err, "count member business links")
		}
		if int(count) >= p.MaxLinksPerMember {
			return ErrQuotaExceeded.New(errorx.Public("limit", p.MaxLinksPerMember))
		}
	}
	return nil
}

// CheckConnectTelegram 校验成员 sub 是否允许在当前工作区新绑定 Telegram 个人号（原地重登已有账号不占新配额）。
func (g *PolicyGuard) CheckConnectTelegram(ctx context.Context, sub identity.Subject, isReconnect bool) error {
	if g == nil {
		return nil
	}
	if g.features != nil && !g.features.Active(ctx, sub, flags.BusinessTelegramAccount) {
		return ErrFeatureDisabled.New()
	}
	if g.policy == nil {
		return nil
	}
	p, err := g.policy.Tenant(sub.Tenant).Get(ctx)
	if err != nil {
		return errors.Wrap(err, "load business policy")
	}
	if !p.TelegramAccountsEnabled {
		return ErrFeatureDisabled.New()
	}
	if !isReconnect && p.MaxTelegramAccountsPerMember > 0 {
		count, err := g.db.NewSelect().Model((*channel.Channel)(nil)).
			Where("tenant_id = ? AND kind = ? AND owner_member_id = ?", sub.Tenant, channel.TelegramAccount, sub.ID).
			Count(ctx)
		if err != nil {
			return errors.Wrap(err, "count member telegram accounts")
		}
		if int(count) >= p.MaxTelegramAccountsPerMember {
			return ErrQuotaExceeded.New(errorx.Public("limit", p.MaxTelegramAccountsPerMember))
		}
	}
	return nil
}

// PolicyActions 实现工作区商务策略的读取与保存。
type PolicyActions struct {
	db       *bun.DB
	policy   *settings.Group[Policy]
	features *feature.Store
	tgCreds  *settings.Group[tgaccount.Credentials]
}

// NewPolicyActions 返回工作区商务策略的读取与保存动作。
func NewPolicyActions(db *bun.DB, policy *settings.Group[Policy], features *feature.Store, tgCreds *settings.Group[tgaccount.Credentials]) *PolicyActions {
	return &PolicyActions{db: db, policy: policy, features: features, tgCreds: tgCreds}
}

// Get 读取 sub 所在工作区的商务策略、平台功能开关状态与 sub 当前个人用量。
func (a *PolicyActions) Get(ctx context.Context, sub identity.Subject) (*PolicyViewDTO, error) {
	p, err := a.policy.Tenant(sub.Tenant).Get(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load business policy")
	}
	platformLinks := true
	platformTG := true
	if a.features != nil {
		platformLinks = a.features.Active(ctx, sub, flags.BusinessLinks)
		platformTG = a.features.Active(ctx, sub, flags.BusinessTelegramAccount)
	}
	tgConfigured := false
	if a.tgCreds != nil {
		cfg, err := a.tgCreds.Tenant(sub.Tenant).Get(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "load tenant telegram credentials")
		}
		tgConfigured = cfg.Configured()
	}
	var counts []struct {
		Kind  channel.Kind `bun:"kind"`
		Count int          `bun:"count"`
	}
	if err := a.db.NewSelect().Table("channels").
		Column("kind").ColumnExpr("count(*) AS count").
		Where("tenant_id = ? AND owner_member_id = ? AND kind IN (?)", sub.Tenant, sub.ID, bun.In([]channel.Kind{channel.BusinessLink, channel.TelegramAccount})).
		Group("kind").Scan(ctx, &counts); err != nil {
		return nil, errors.Wrap(err, "count member business channels")
	}
	var myLinks, myTG int
	for _, r := range counts {
		switch r.Kind {
		case channel.BusinessLink:
			myLinks = r.Count
		case channel.TelegramAccount:
			myTG = r.Count
		}
	}
	return &PolicyViewDTO{
		Policy:                  p,
		PlatformLinksEnabled:    platformLinks,
		PlatformTelegramEnabled: platformTG,
		TelegramAPIConfigured:   tgConfigured,
		MyLinksCount:            myLinks,
		MyTelegramCount:         myTG,
	}, nil
}

// Save 校验并保存 sub 所在工作区的商务策略，并在同一事务中写入操作日志。
func (a *PolicyActions) Save(ctx context.Context, sub identity.Subject, next Policy) (*PolicyViewDTO, error) {
	next.DefaultCompanyName = strings.TrimSpace(next.DefaultCompanyName)
	next.DefaultTranslationTone = strings.TrimSpace(next.DefaultTranslationTone)
	switch next.DefaultTranslationTone {
	case "", "friendly", "neutral", "formal":
	default:
		return nil, ErrInvalidTone.New(errorx.Field("default_translation_tone"))
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := a.policy.WithDB(tx).Tenant(sub.Tenant).Update(ctx, func(cur *Policy) error {
			*cur = next
			return nil
		}); err != nil {
			return errors.Wrap(err, "save business policy")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "business_policy.updated", TargetType: "tenant", TargetID: sub.Tenant,
			Properties: map[string]any{
				"links_enabled":             next.LinksEnabled,
				"telegram_accounts_enabled": next.TelegramAccountsEnabled,
				"default_translation_tone":  next.DefaultTranslationTone,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return a.Get(ctx, sub)
}

// GetHTTP 处理 GET /t/{slug}/api/settings/business-policy。
func (a *PolicyActions) GetHTTP(ctx context.Context, _ *struct{}) (*PolicyOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Get(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &PolicyOutput{Body: *dto}, nil
}

// SaveHTTP 处理 PUT /t/{slug}/api/settings/business-policy。
func (a *PolicyActions) SaveHTTP(ctx context.Context, in *SavePolicyInput) (*PolicyOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Save(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &PolicyOutput{Body: *dto}, nil
}
