package widget

import (
	"context"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/filesystem"

	"agents-desk/internal/account"
	"agents-desk/internal/channel"
	"agents-desk/internal/member"
)

// PublicOwnerCard 是商务沟通链接页面顶部展示的对接人公开名片信息。
type PublicOwnerCard struct {
	Name      string        `json:"name"`
	Title     string        `json:"title,omitempty"`
	Company   string        `json:"company,omitempty"`
	Bio       string        `json:"bio,omitempty"`
	AvatarURL string        `json:"avatar_url,omitempty"`
	Status    member.Status `json:"status" doc:"对接人当前在线状态：online 或 away"`
}

// PublicBusinessConfig 是网页商务沟通链接暴露给外部合作方的公开配置。
type PublicBusinessConfig struct {
	Owner            *PublicOwnerCard     `json:"owner,omitempty" doc:"开启名片展示时的对接人名片"`
	AwayMessage      string               `json:"away_message,omitempty" doc:"对接人处于离开状态时的提示语"`
	Intake           BusinessIntakeConfig `json:"intake" doc:"开场留资表单配置"`
	Starters         []string             `json:"starters" nullable:"false" doc:"快捷开场话题列表"`
	RequiresPasscode bool                 `json:"requires_passcode" doc:"新建访客会话是否需要输入访问口令"`
	Expired          bool                 `json:"expired" doc:"链接是否已超过有效期"`
}

// ConfigDTO 是聊天窗口打开之前读取的公开设置。
type ConfigDTO struct {
	Kind       channel.Kind          `json:"kind" doc:"渠道种类：widget 或 business_link"`
	Name       string                `json:"name" doc:"聊天插件或商务链接的名称，显示在聊天窗口的标题中"`
	Appearance Appearance            `json:"appearance" doc:"聊天窗口的外观配置"`
	Business   *PublicBusinessConfig `json:"business,omitempty" doc:"网页商务沟通链接的公开名片、留资与访问配置"`
}

// ConfigOutput 是返回公开设置的响应。
type ConfigOutput struct {
	Body ConfigDTO
}

// GetConfig 读取客户或外部合作方在用的网页聊天渠道的公开设置，公开接口无需登录。
type GetConfig struct {
	db             *bun.DB
	files          *filesystem.Service
	defaultCompany func(ctx context.Context, tenantID string) string
}

// NewGetConfig 返回读取公开设置的动作；db/files/defaultCompany 用于为商务沟通链接加载对接人名片与在线状态。
func NewGetConfig(db *bun.DB, files *filesystem.Service, defaultCompany ...func(ctx context.Context, tenantID string) string) *GetConfig {
	var fn func(ctx context.Context, tenantID string) string
	if len(defaultCompany) > 0 {
		fn = defaultCompany[0]
	}
	return &GetConfig{db: db, files: files, defaultCompany: fn}
}

// Handle 返回本次请求的网页聊天渠道（Middleware 查出的）的公开配置。
// 渠道不存在、已停用或者所在的工作区已停用时返回 channel.ErrNotFound。
func (a *GetConfig) Handle(ctx context.Context) (*ConfigDTO, error) {
	ch, err := channelFrom(ctx)
	if err != nil {
		return nil, err
	}
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	dto := &ConfigDTO{
		Kind:       ch.Kind,
		Name:       ch.Name,
		Appearance: s.appearance(),
	}
	if ch.Kind != channel.BusinessLink {
		return dto, nil
	}
	cardCfg := s.resolvedCard()
	access := s.resolvedAccess()
	starters := s.Starters
	if starters == nil {
		starters = []string{}
	}
	expired := access.ExpiresAt != nil && time.Now().UTC().After(*access.ExpiresAt)
	biz := &PublicBusinessConfig{
		AwayMessage:      cardCfg.AwayMessage,
		Intake:           s.resolvedIntake(),
		Starters:         starters,
		RequiresPasscode: access.Mode == "passcode" && access.PasscodeHash != "",
		Expired:          expired,
	}
	if cardCfg.ShowOwner && ch.OwnerMemberID != nil && *ch.OwnerMemberID != "" && a.db != nil {
		if m, err := member.Find(ctx, a.db, *ch.OwnerMemberID); err == nil {
			title := cardCfg.TitleOverride
			if title == "" {
				title = m.Title
			}
			bio := cardCfg.BioOverride
			if bio == "" {
				bio = m.Bio
			}
			company := cardCfg.CompanyOverride
			if company == "" && a.defaultCompany != nil {
				company = strings.TrimSpace(a.defaultCompany(ctx, ch.TenantID))
			}
			if company == "" {
				_ = a.db.NewSelect().Table("tenants").Column("name").Where("id = ?", ch.TenantID).Scan(ctx, &company)
			}
			avatarURL := ""
			if a.files != nil {
				if u, err := account.AvatarURL(ctx, a.files, m.AvatarFileID); err == nil {
					avatarURL = u
				}
			}
			status := member.Away
			if statuses, err := member.StatusesOf(ctx, a.db, ch.TenantID, []string{m.ID}); err == nil {
				status = statuses[m.ID]
			}
			biz.Owner = &PublicOwnerCard{
				Name:      m.Name,
				Title:     title,
				Company:   company,
				Bio:       bio,
				AvatarURL: avatarURL,
				Status:    status,
			}
		}
	}
	dto.Business = biz
	return dto, nil
}

// HTTP 处理 GET /w/{channel}/api/config。
func (a *GetConfig) HTTP(ctx context.Context, _ *struct{}) (*ConfigOutput, error) {
	dto, err := a.Handle(ctx)
	if err != nil {
		return nil, err
	}
	return &ConfigOutput{Body: *dto}, nil
}
