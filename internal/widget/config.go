package widget

import (
	"context"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/account"
	"agents-desk/internal/channel"
	"agents-desk/internal/flags"
	"agents-desk/internal/lang"
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

// PublicBusinessConfig 是网页商务沟通链接暴露给外部联系人的公开配置（所有文本字段已按请求语言解析完毕）。
type PublicBusinessConfig struct {
	Owner            *PublicOwnerCard     `json:"owner,omitempty" doc:"开启名片展示时的对接人名片"`
	AwayMessage      string               `json:"away_message,omitempty" doc:"对接人处于离开状态时的提示语"`
	Intake           BusinessIntakeConfig `json:"intake" doc:"开场留资表单配置"`
	Starters         []string             `json:"starters" nullable:"false" doc:"快捷开场话题列表"`
	RequiresPasscode bool                 `json:"requires_passcode" doc:"新建访客会话是否需要输入访问口令"`
	Expired          bool                 `json:"expired" doc:"链接是否已超过有效期"`
}

// ConfigDTO 是聊天窗口打开之前读取的公开设置（服务端已按访客请求语言解析好对外展示文案）。
type ConfigDTO struct {
	Kind            channel.Kind          `json:"kind" doc:"渠道种类：widget 或 business_link"`
	Name            string                `json:"name" doc:"已按语言解析的对外窗口标题"`
	Greeting        string                `json:"greeting,omitempty" doc:"已按语言解析的欢迎问候语；为空时显示界面默认问候语"`
	Appearance      Appearance            `json:"appearance" doc:"聊天窗口的视觉外观配置"`
	Language        string                `json:"language" doc:"本次配置解析使用的语言（BCP 47）"`
	DefaultLanguage string                `json:"default_language" doc:"渠道配置的默认语言（BCP 47）"`
	VisitorUploads  bool                  `json:"visitor_uploads" doc:"当前工作区是否允许访客在聊天窗口上传附件"`
	Business        *PublicBusinessConfig `json:"business,omitempty" doc:"网页商务沟通链接的公开名片、留资与访问配置"`
}

// ConfigInput 是读取公开设置的 HTTP 请求：显式 query lang 优先于 Accept-Language 请求头。
type ConfigInput struct {
	Lang           string `query:"lang" maxLength:"35" doc:"可选：显式指定配置解析语言（BCP 47），优先于 Accept-Language"`
	AcceptLanguage string `header:"Accept-Language"`
}

// ConfigOutput 是返回公开设置的响应，携带 Vary: Accept-Language 保证缓存正确按语言区分。
type ConfigOutput struct {
	Vary string `header:"Vary"`
	Body ConfigDTO
}

// GetConfig 读取客户或商务联系人在用的网页聊天渠道的公开设置，公开接口无需登录。
type GetConfig struct {
	db             *bun.DB
	files          *filesystem.Service
	features       *feature.Store
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

// SetFeatures 绑定平台功能开关存储，用于读取访客上传开关等平台级灰度策略。
func (a *GetConfig) SetFeatures(f *feature.Store) {
	a.features = f
}

// Handle 返回本次请求的网页聊天渠道（Middleware 查出的）在 requestedLang 下的公开配置。
// 渠道不存在、已停用或者所在的工作区已停用时返回 channel.ErrNotFound。
func (a *GetConfig) Handle(ctx context.Context, requestedLang ...string) (*ConfigDTO, error) {
	ch, err := channelFrom(ctx)
	if err != nil {
		return nil, err
	}
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	loc := s.localization()
	reqLang := ""
	if len(requestedLang) > 0 {
		reqLang = requestedLang[0]
	}
	resolvedLang, copy := resolveLocalizedCopy(loc, reqLang)

	title := copy.Title
	if title == "" {
		title = ch.Name
	}
	visitorUploads := true
	if a.features != nil {
		visitorUploads = a.features.Active(ctx, identity.Subject{Tenant: ch.TenantID}, flags.WidgetVisitorUploads)
	}
	dto := &ConfigDTO{
		Kind:            ch.Kind,
		Name:            title,
		Greeting:        copy.Greeting,
		Appearance:      s.appearance(),
		Language:        resolvedLang,
		DefaultLanguage: loc.DefaultLanguage,
		VisitorUploads:  visitorUploads,
	}
	if ch.Kind != channel.BusinessLink {
		return dto, nil
	}
	cardCfg := s.resolvedCard()
	access := s.resolvedAccess()
	starters := copy.Starters
	if starters == nil {
		starters = []string{}
	}
	expired := access.ExpiresAt != nil && time.Now().UTC().After(*access.ExpiresAt)
	biz := &PublicBusinessConfig{
		AwayMessage:      copy.AwayMessage,
		Intake:           s.resolvedIntake(),
		Starters:         starters,
		RequiresPasscode: access.Mode == "passcode" && access.PasscodeHash != "",
		Expired:          expired,
	}
	if cardCfg.ShowOwner && ch.OwnerMemberID != nil && *ch.OwnerMemberID != "" && a.db != nil {
		if m, err := member.Find(ctx, a.db, *ch.OwnerMemberID); err == nil {
			ownerTitle := copy.OwnerTitle
			if ownerTitle == "" {
				ownerTitle = m.Title
			}
			ownerBio := copy.OwnerBio
			if ownerBio == "" {
				ownerBio = m.Bio
			}
			ownerCompany := copy.OwnerCompany
			if ownerCompany == "" && a.defaultCompany != nil {
				ownerCompany = strings.TrimSpace(a.defaultCompany(ctx, ch.TenantID))
			}
			if ownerCompany == "" {
				_ = a.db.NewSelect().Table("tenants").Column("name").Where("id = ?", ch.TenantID).Scan(ctx, &ownerCompany)
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
				Title:     ownerTitle,
				Company:   ownerCompany,
				Bio:       ownerBio,
				AvatarURL: avatarURL,
				Status:    status,
			}
		}
	}
	dto.Business = biz
	return dto, nil
}

// HTTP 处理 GET /w/{channel}/api/config。
func (a *GetConfig) HTTP(ctx context.Context, in *ConfigInput) (*ConfigOutput, error) {
	requested := strings.TrimSpace(in.Lang)
	if requested == "" {
		requested = lang.FromAcceptLanguage(in.AcceptLanguage)
	}
	dto, err := a.Handle(ctx, requested)
	if err != nil {
		return nil, err
	}
	return &ConfigOutput{Vary: "Accept-Language", Body: *dto}, nil
}
