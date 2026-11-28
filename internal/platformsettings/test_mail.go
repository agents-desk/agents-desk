package platformsettings

import (
	"context"
	"slices"
	"strings"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/emails"
)

// TestMailInput 是发送测试邮件的请求。
type TestMailInput struct {
	Body struct {
		To string `json:"to,omitempty" format:"email" maxLength:"254" doc:"收件箱地址；留空时发送给当前管理员登录邮箱"`
	}
}

// TestMailDTO 是测试邮件发送成功的响应。
type TestMailDTO struct {
	To string `json:"to"`
}

// TestMailOutput 是测试邮件接口响应。
type TestMailOutput struct {
	Body TestMailDTO
}

// TestMail 使用平台当前已保存的 Resend 设置向指定邮箱发送测试邮件。
type TestMail struct {
	general   *settings.Group[General]
	mailGroup *settings.Group[Mail]
	logins    *login.Realm
	templates *mail.Templates
}

// NewTestMail 返回测试邮件连通性的动作。
func NewTestMail(general *settings.Group[General], mailGroup *settings.Group[Mail], logins *login.Realm, templates *mail.Templates) *TestMail {
	return &TestMail{general: general, mailGroup: mailGroup, logins: logins, templates: templates}
}

// Handle 读取当前已保存的邮件配置并向 to（为空时取当前管理员邮箱）发送测试邮件。
// 未配置 API Key 或发件邮箱时返回 ErrMailNotConfigured；邮箱格式非法返回 ErrInvalidEmail。
func (a *TestMail) Handle(ctx context.Context, sub identity.Subject, to string) (TestMailDTO, error) {
	recipient := strings.TrimSpace(to)
	if recipient == "" && a.logins != nil {
		ids, err := a.logins.Identifiers(ctx, sub.Ref())
		if err != nil {
			return TestMailDTO{}, err
		}
		if i := slices.IndexFunc(ids, func(id login.Attached) bool { return id.Kind == login.KindEmail }); i >= 0 {
			recipient = ids[i].Value
		}
	}
	if recipient == "" || !validEmail(recipient) {
		return TestMailDTO{}, ErrInvalidEmail.New(errorx.Field("to"))
	}

	cfg, err := a.mailGroup.Get(ctx)
	if err != nil {
		return TestMailDTO{}, err
	}
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.FromEmail) == "" {
		return TestMailDTO{}, ErrMailNotConfigured.New()
	}

	gen, err := a.general.Get(ctx)
	if err != nil {
		return TestMailDTO{}, err
	}
	sender, err := BuildMailer(cfg)
	if err != nil {
		return TestMailDTO{}, err
	}
	renderCtx := emails.EnsureLocale(ctx, string(gen.DefaultLanguage))
	msg, err := a.templates.Render(renderCtx, "mail_test", map[string]any{
		"SiteName":  gen.SiteName,
		"FromEmail": cfg.FromEmail,
	})
	if err != nil {
		return TestMailDTO{}, err
	}
	msg.To = []mail.Address{{Email: recipient}}
	if err := sender.Send(ctx, msg); err != nil {
		return TestMailDTO{}, err
	}
	return TestMailDTO{To: recipient}, nil
}

// HTTP 处理 POST /admin/api/settings/mail/test。
func (a *TestMail) HTTP(ctx context.Context, in *TestMailInput) (*TestMailOutput, error) {
	sub, _ := identity.FromContext(ctx)
	out, err := a.Handle(ctx, sub, in.Body.To)
	if err != nil {
		return nil, err
	}
	return &TestMailOutput{Body: out}, nil
}
