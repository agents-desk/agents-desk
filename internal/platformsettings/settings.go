// Package platformsettings 管理平台级的常规设置与邮件服务（Resend）设置，
// 保存在 Tenon 的 settings 表中，并经 api.Settings 声明给管理员端的 SettingsPage 自动渲染表单与校验。
package platformsettings

import (
	"context"
	"net"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"golang.org/x/net/proxy"

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
	// ErrTelegramCredentialsIncomplete 表示配置 Telegram API 凭据时 API ID 与 API Hash 必须同时填写。
	ErrTelegramCredentialsIncomplete = errorx.Define(errorx.InvalidArgument, "platform_settings.telegram_incomplete")
	// ErrInvalidProxyURL 表示填写的 Telegram 出口代理地址格式无效（仅支持 socks5:// 或 socks5h://）。
	ErrInvalidProxyURL = errorx.Define(errorx.InvalidArgument, "platform_settings.invalid_proxy_url")
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

// Telegram 是平台级 Telegram MTProto 应用凭据（用于成员绑定 Telegram 个人号商务渠道）。
// 字段不带 omitempty，见 tenon/settings 文档；APIHash 落库时自动加密保存。
type Telegram struct {
	APIID    int    `json:"api_id" minimum:"0" doc:"Telegram 应用 API ID（在 my.telegram.org 申请）；填 0 表示暂不启用个人号绑定"`
	APIHash  string `json:"api_hash" maxLength:"128" format:"password" settings:"secret" doc:"Telegram 应用 API Hash，落库时自动加密保存"`
	ProxyURL string `json:"proxy_url" maxLength:"256" doc:"可选：MTProto 出口代理地址，例如 socks5://user:pass@127.0.0.1:1080；留空直连"`
}

// TelegramDefaults 是平台 Telegram API 凭据的默认值。
var TelegramDefaults = Telegram{}

// Configured 报告平台是否已完整配置 Telegram MTProto 应用凭据。
func (t Telegram) Configured() bool { return t.APIID > 0 && strings.TrimSpace(t.APIHash) != "" }

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

// NewTelegramGroup 返回平台 Telegram API 凭据的存储组，APIHash 经 keys 透明加密存储。
func NewTelegramGroup(db bun.IDB, keys *crypt.Crypt) *settings.Group[Telegram] {
	return settings.New(db, "platform.telegram", TelegramDefaults, settings.WithCrypt(keys))
}

// GeneralBody 是常规设置的请求与响应体。
type GeneralBody struct {
	Body General
}

// MailBody 是邮件设置的请求与响应体。
type MailBody struct {
	Body Mail
}

// TelegramBody 是 Telegram API 凭据设置的请求与响应体。
type TelegramBody struct {
	Body Telegram
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

// GetTelegram 读取平台 Telegram API 凭据设置。
type GetTelegram struct{ group *settings.Group[Telegram] }

// NewGetTelegram 返回读取平台 Telegram API 凭据设置的动作。
func NewGetTelegram(group *settings.Group[Telegram]) *GetTelegram {
	return &GetTelegram{group: group}
}

// HTTP 处理 GET /settings/telegram。
func (a *GetTelegram) HTTP(ctx context.Context, _ *struct{}) (*TelegramBody, error) {
	v, err := a.group.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &TelegramBody{Body: v}, nil
}

// SaveTelegram 保存平台 Telegram API 凭据设置。
type SaveTelegram struct {
	db    *bun.DB
	group *settings.Group[Telegram]
}

// NewSaveTelegram 返回保存平台 Telegram API 凭据设置的动作。
func NewSaveTelegram(db *bun.DB, group *settings.Group[Telegram]) *SaveTelegram {
	return &SaveTelegram{db: db, group: group}
}

// Handle 校验并保存平台 Telegram MTProto 凭据（API ID 与 API Hash 须同时填写或同时留空），有修改时写入审计日志。
func (a *SaveTelegram) Handle(ctx context.Context, subject identity.Subject, next Telegram) (Telegram, error) {
	next.APIHash = strings.TrimSpace(next.APIHash)
	next.ProxyURL = strings.TrimSpace(next.ProxyURL)
	if (next.APIID > 0) != (next.APIHash != "") {
		field := "api_hash"
		if next.APIID <= 0 {
			field = "api_id"
		}
		return Telegram{}, ErrTelegramCredentialsIncomplete.New(errorx.Field(field))
	}
	if next.ProxyURL != "" && !validSOCKS5Proxy(next.ProxyURL) {
		return Telegram{}, ErrInvalidProxyURL.New(errorx.Field("proxy_url"))
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Update(ctx, func(cur *Telegram) error {
			if cur.APIID != next.APIID {
				changed = append(changed, "api_id")
			}
			if cur.APIHash != next.APIHash {
				changed = append(changed, "api_hash")
			}
			if cur.ProxyURL != next.ProxyURL {
				changed = append(changed, "proxy_url")
			}
			*cur = next
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event: "platform_settings.telegram_updated", TargetType: "settings", TargetID: "telegram",
			Properties: map[string]any{"changed": changed},
		})
	})
	return next, err
}

// HTTP 处理 PUT /settings/telegram。
func (a *SaveTelegram) HTTP(ctx context.Context, in *TelegramBody) (*TelegramBody, error) {
	sub, _ := identity.FromContext(ctx)
	saved, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TelegramBody{Body: saved}, nil
}

// TelegramTestInput 是测试 Telegram MTProto 连通性的请求（可传入尚未保存的代理地址进行预检）。
type TelegramTestInput struct {
	Body struct {
		ProxyURL string `json:"proxy_url,omitempty" maxLength:"256" doc:"可选：待测试的 SOCKS5 代理地址；留空使用已保存的代理设置"`
	}
}

// TelegramTestResult 是测试 Telegram MTProto 网络连通性的结果。
type TelegramTestResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// TelegramTestOutput 是测试连通性的 HTTP 响应。
type TelegramTestOutput struct {
	Body TelegramTestResult
}

// TestTelegram 检测当前服务器（直连或经 SOCKS5 代理）到 Telegram MTProto DC 的网络连通性与延迟。
type TestTelegram struct {
	group *settings.Group[Telegram]
}

// NewTestTelegram 返回测试 Telegram 连通性的动作。
func NewTestTelegram(group *settings.Group[Telegram]) *TestTelegram {
	return &TestTelegram{group: group}
}

// Handle 尝试在 6 秒超时内建立到 Telegram DC2 的 TCP/SOCKS5 连接并返回延迟结果。
func (a *TestTelegram) Handle(ctx context.Context, proxyOverride string) (*TelegramTestResult, error) {
	proxyURL := strings.TrimSpace(proxyOverride)
	if proxyURL == "" && a.group != nil {
		if saved, err := a.group.Get(ctx); err == nil {
			proxyURL = strings.TrimSpace(saved.ProxyURL)
		}
	}
	if proxyURL != "" && !validSOCKS5Proxy(proxyURL) {
		return nil, ErrInvalidProxyURL.New(errorx.Field("proxy_url"))
	}
	const telegramDC2 = "149.154.167.50:443"
	dialCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := DialTelegram(dialCtx, proxyURL, "tcp", telegramDC2)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &TelegramTestResult{OK: false, LatencyMS: latency, Error: err.Error()}, nil
	}
	_ = conn.Close()
	return &TelegramTestResult{OK: true, LatencyMS: latency}, nil
}

// HTTP 处理 POST /settings/telegram/test。
func (a *TestTelegram) HTTP(ctx context.Context, in *TelegramTestInput) (*TelegramTestOutput, error) {
	res, err := a.Handle(ctx, in.Body.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &TelegramTestOutput{Body: *res}, nil
}

// DialTelegram 按可选 SOCKS5 代理配置拨号到目标地址，供平台连通性测试与 tgaccount MTProto 客户端复用。
func DialTelegram(ctx context.Context, proxyURL, network, addr string) (net.Conn, error) {
	base := &net.Dialer{Timeout: 10 * time.Second}
	if strings.TrimSpace(proxyURL) == "" {
		return base.DialContext(ctx, network, addr)
	}
	u, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil {
		return nil, err
	}
	var auth *proxy.Auth
	if u.User != nil {
		pwd, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pwd}
	}
	d, err := proxy.SOCKS5("tcp", u.Host, auth, base)
	if err != nil {
		return nil, err
	}
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, network, addr)
	}
	return d.Dial(network, addr)
}

// validSOCKS5Proxy 校验 SOCKS5 代理地址格式。
func validSOCKS5Proxy(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "socks5" || scheme == "socks5h"
}

// validEmail 检查字符串是不是不含显示名的纯邮箱地址。
func validEmail(s string) bool {
	addr, err := netmail.ParseAddress(s)
	return err == nil && addr.Name == "" && addr.Address == s
}
