// Package platformsettings 管理平台级的常规设置与邮件服务（Resend）设置，
// 保存在 Tenon 的 settings 表中，并经 api.Settings 声明给管理员端的 SettingsPage 自动渲染表单与校验。
package platformsettings

import (
	"context"
	netmail "net/mail"
	"strings"

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
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/mail/logmail"
	"github.com/go-tenon/tenon/mail/resend"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/file"
	"agents-desk/internal/lang"
)

var (
	// ErrInvalidEmail 表示填写的邮箱格式不正确。
	ErrInvalidEmail = errorx.Define(errorx.InvalidArgument, "platform_settings.invalid_email")
	// ErrRequiredField 表示启用 Resend 时必填项未填写。
	ErrRequiredField = errorx.Define(errorx.InvalidArgument, "platform_settings.required_field")
)

// Language 是平台默认界面语言。
type Language string

const (
	LangZhCN Language = lang.ZhCN // 简体中文
	LangEN   Language = lang.En   // English
)

// Languages 是平台支持的默认界面语言。
var Languages = enum.New("platform_settings.language", enum.Item(LangZhCN), enum.Item(LangEN))

// Schema 让 OpenAPI 中的 Language 只接受目录中的取值并带显示文字。
func (Language) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Languages) }

// General 是平台常规设置。字段不带 omitempty，见 tenon/settings 文档。
type General struct {
	SiteName        string   `json:"site_name" minLength:"1" maxLength:"60"`
	LogoFileID      string   `json:"logo_file_id" maxLength:"36"`
	SupportEmail    string   `json:"support_email" maxLength:"120"`
	DefaultLanguage Language `json:"default_language"`
}

// GeneralDefaults 是平台常规设置的默认值。
var GeneralDefaults = General{
	SiteName:        "agents-desk",
	LogoFileID:      "",
	SupportEmail:    "",
	DefaultLanguage: LangZhCN,
}

// Mail 是平台邮件服务（Resend）设置。APIKey 为空表示暂不启用外发（仅写入日志），落库时自动加密保存。
// 字段不带 omitempty，见 tenon/settings 文档。
type Mail struct {
	FromName  string `json:"from_name" maxLength:"80"`
	FromEmail string `json:"from_email" maxLength:"120"`
	APIKey    string `json:"api_key" maxLength:"256" format:"password" settings:"secret"`
}

// MailDefaults 是平台邮件服务的默认值。
var MailDefaults = Mail{
	FromName:  "agents-desk",
	FromEmail: "",
	APIKey:    "",
}

// BuildMailer 按当前 Resend 设置构造 mail.Mailer；未填写 APIKey 时返回 logmail。
func BuildMailer(m Mail) (mail.Mailer, error) {
	if strings.TrimSpace(m.APIKey) == "" {
		return logmail.New(nil), nil
	}
	return resend.New(resend.Config{
		From:   mail.Address{Name: m.FromName, Email: m.FromEmail},
		APIKey: m.APIKey,
	})
}

// NewGeneralGroup 返回平台常规设置的存储组。
func NewGeneralGroup(db bun.IDB) *settings.Group[General] {
	return settings.New(db, "platform.general", GeneralDefaults)
}

// NewMailGroup 返回平台邮件设置的存储组，密钥字段经 keys 透明加密存储。
func NewMailGroup(db bun.IDB, keys *crypt.Crypt) *settings.Group[Mail] {
	return settings.New(db, "platform.mail", MailDefaults, settings.WithCrypt(keys))
}

// GeneralBody 是常规设置的请求与响应体。
type GeneralBody struct {
	Body General
}

// MailBody 是邮件设置的请求与响应体。
type MailBody struct {
	Body Mail
}

// PublicSite 是无需登录即可读取的平台公开信息，供管理端与工作区端的顶栏、登录页和浏览器标签页显示。
type PublicSite struct {
	SiteName string `json:"site_name"` // 平台名称
	LogoURL  string `json:"logo_url"`  // 平台 Logo 公开地址，未设置时为空
}

// PublicSiteBody 是 GET /site 的响应体。
type PublicSiteBody struct {
	Body PublicSite
}

// GetPublicSite 读取平台公开信息。
type GetPublicSite struct {
	group *settings.Group[General]
	files *filesystem.Service
}

// NewGetPublicSite 返回读取平台公开信息的动作。
func NewGetPublicSite(group *settings.Group[General], files *filesystem.Service) *GetPublicSite {
	return &GetPublicSite{group: group, files: files}
}

// HTTP 处理 GET /site，返回公开的平台名称与 Logo 地址。
func (a *GetPublicSite) HTTP(ctx context.Context, _ *struct{}) (*PublicSiteBody, error) {
	v, err := a.group.Get(ctx)
	if err != nil {
		return nil, err
	}
	var logoURL string
	if v.LogoFileID != "" {
		id, err := uuid.Parse(v.LogoFileID)
		if err != nil {
			return nil, err
		}
		logoURL, err = file.URL(ctx, a.files, &id)
		if err != nil {
			return nil, err
		}
	}
	return &PublicSiteBody{Body: PublicSite{SiteName: v.SiteName, LogoURL: logoURL}}, nil
}

// GetGeneral 读取平台常规设置。
type GetGeneral struct{ group *settings.Group[General] }

// NewGetGeneral 返回读取平台常规设置的动作。
func NewGetGeneral(group *settings.Group[General]) *GetGeneral { return &GetGeneral{group: group} }

// HTTP 处理 GET /settings/general。
func (a *GetGeneral) HTTP(ctx context.Context, _ *struct{}) (*GeneralBody, error) {
	v, err := a.group.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &GeneralBody{Body: v}, nil
}

// SaveGeneral 保存平台常规设置。
type SaveGeneral struct {
	db    *bun.DB
	group *settings.Group[General]
	files *filesystem.Service
}

// NewSaveGeneral 返回保存平台常规设置的动作。
func NewSaveGeneral(db *bun.DB, group *settings.Group[General], files *filesystem.Service) *SaveGeneral {
	return &SaveGeneral{db: db, group: group, files: files}
}

// Handle 校验并保存平台常规设置，确认或替换平台 Logo 文件，有修改时在同一事务中写入操作日志。
func (a *SaveGeneral) Handle(ctx context.Context, subject identity.Subject, next General) (General, error) {
	next.SiteName = strings.TrimSpace(next.SiteName)
	next.LogoFileID = strings.TrimSpace(next.LogoFileID)
	next.SupportEmail = strings.TrimSpace(next.SupportEmail)
	if next.SupportEmail != "" && !validEmail(next.SupportEmail) {
		return General{}, ErrInvalidEmail.New(errorx.Field("support_email"))
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Update(ctx, func(cur *General) error {
			if cur.SiteName != next.SiteName {
				changed = append(changed, "site_name")
			}
			if cur.LogoFileID != next.LogoFileID {
				var curID *uuid.UUID
				if cur.LogoFileID != "" {
					parsed, err := uuid.Parse(cur.LogoFileID)
					if err != nil {
						return err
					}
					curID = &parsed
				}
				newID, err := file.ReplaceImage(ctx, tx, a.files, subject, "logo_file_id", file.Logo, curID, next.LogoFileID)
				if err != nil {
					return err
				}
				if newID != nil {
					next.LogoFileID = newID.String()
				} else {
					next.LogoFileID = ""
				}
				changed = append(changed, "logo_file_id")
			}
			if cur.SupportEmail != next.SupportEmail {
				changed = append(changed, "support_email")
			}
			if cur.DefaultLanguage != next.DefaultLanguage {
				changed = append(changed, "default_language")
			}
			*cur = next
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "platform_settings.general_updated", TargetType: "settings", TargetID: "general",
			Properties: map[string]any{"changed": changed},
		})
	})
	return next, err
}

// HTTP 处理 PUT /settings/general。
func (a *SaveGeneral) HTTP(ctx context.Context, in *GeneralBody) (*GeneralBody, error) {
	sub, _ := identity.FromContext(ctx)
	saved, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &GeneralBody{Body: saved}, nil
}

// GetMail 读取平台邮件设置。
type GetMail struct{ group *settings.Group[Mail] }

// NewGetMail 返回读取平台邮件设置的动作。
func NewGetMail(group *settings.Group[Mail]) *GetMail { return &GetMail{group: group} }

// HTTP 处理 GET /settings/mail。
func (a *GetMail) HTTP(ctx context.Context, _ *struct{}) (*MailBody, error) {
	v, err := a.group.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &MailBody{Body: v}, nil
}

// SaveMail 保存平台邮件设置。
type SaveMail struct {
	db    *bun.DB
	group *settings.Group[Mail]
}

// NewSaveMail 返回保存平台邮件设置的动作。
func NewSaveMail(db *bun.DB, group *settings.Group[Mail]) *SaveMail {
	return &SaveMail{db: db, group: group}
}

// Handle 校验并保存 Resend 邮件设置，有修改时在同一事务中写入操作日志（API Key 只记字段名，不记值）。
func (a *SaveMail) Handle(ctx context.Context, subject identity.Subject, next Mail) (Mail, error) {
	next.FromName = strings.TrimSpace(next.FromName)
	next.FromEmail = strings.TrimSpace(next.FromEmail)
	next.APIKey = strings.TrimSpace(next.APIKey)

	if next.FromEmail != "" && !validEmail(next.FromEmail) {
		return Mail{}, ErrInvalidEmail.New(errorx.Field("from_email"))
	}
	if next.APIKey != "" && next.FromEmail == "" {
		return Mail{}, ErrRequiredField.New(errorx.Field("from_email"))
	}

	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Update(ctx, func(cur *Mail) error {
			if cur.FromName != next.FromName {
				changed = append(changed, "from_name")
			}
			if cur.FromEmail != next.FromEmail {
				changed = append(changed, "from_email")
			}
			if cur.APIKey != next.APIKey {
				changed = append(changed, "api_key")
			}
			*cur = next
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "platform_settings.mail_updated", TargetType: "settings", TargetID: "mail",
			Properties: map[string]any{"changed": changed},
		})
	})
	return next, err
}

// HTTP 处理 PUT /settings/mail。
func (a *SaveMail) HTTP(ctx context.Context, in *MailBody) (*MailBody, error) {
	sub, _ := identity.FromContext(ctx)
	saved, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &MailBody{Body: saved}, nil
}

// validEmail 检查字符串是不是不含显示名的纯邮箱地址。
func validEmail(s string) bool {
	addr, err := netmail.ParseAddress(s)
	return err == nil && addr.Name == "" && addr.Address == s
}
