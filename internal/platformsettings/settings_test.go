package platformsettings_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/filesystem"

	"agents-desk/internal/apptest"
	"agents-desk/internal/emails"
	"agents-desk/internal/platformsettings"
)

func TestGeneralAndMailSettings(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	generalGroup := platformsettings.NewGeneralGroup(env.DB)
	mailGroup := platformsettings.NewMailGroup(env.DB, env.Crypt)

	// 默认值
	gotGeneral, err := generalGroup.Get(ctx)
	if err != nil || gotGeneral != platformsettings.GeneralDefaults {
		t.Fatalf("默认常规设置：%+v err=%v", gotGeneral, err)
	}
	gotMail, err := mailGroup.Get(ctx)
	if err != nil || gotMail != platformsettings.MailDefaults {
		t.Fatalf("默认邮件设置：%+v err=%v", gotMail, err)
	}

	saveGeneral := platformsettings.NewSaveGeneral(env.DB, generalGroup, env.Files)
	if _, err := saveGeneral.Handle(ctx, apptest.Admin, platformsettings.General{
		SiteName: "Desk", SupportEmail: "not-an-email", DefaultLanguage: platformsettings.LangZhCN,
	}); !errors.Is(err, platformsettings.ErrInvalidEmail) {
		t.Fatalf("非法邮箱应当返回 ErrInvalidEmail，实际 %v", err)
	}

	// 上传并设置平台 Logo
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	uploaded, err := env.Files.Upload(ctx, apptest.Admin, filesystem.FileInput{
		Filename: "logo.png", ContentType: "image/png", Size: int64(pngBuf.Len()),
	}, bytes.NewReader(pngBuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := saveGeneral.Handle(ctx, apptest.Admin, platformsettings.General{
		SiteName: "Acme Desk", LogoFileID: uploaded.ID.String(), SupportEmail: "support@acme.example", DefaultLanguage: platformsettings.LangEN,
	}); err != nil {
		t.Fatal(err)
	}
	pub, err := platformsettings.NewGetPublicSite(generalGroup, env.Files).HTTP(ctx, &struct{}{})
	if err != nil || pub.Body.SiteName != "Acme Desk" || !strings.HasPrefix(pub.Body.LogoURL, "/storage/") {
		t.Fatalf("公开平台信息：%+v err=%v", pub, err)
	}

	// 清除平台 Logo 后公开地址为空
	if _, err := saveGeneral.Handle(ctx, apptest.Admin, platformsettings.General{
		SiteName: "Acme Desk", LogoFileID: "", SupportEmail: "support@acme.example", DefaultLanguage: platformsettings.LangEN,
	}); err != nil {
		t.Fatal(err)
	}
	pub, err = platformsettings.NewGetPublicSite(generalGroup, env.Files).HTTP(ctx, &struct{}{})
	if err != nil || pub.Body.LogoURL != "" {
		t.Fatalf("清除后公开 Logo 应为空：%+v err=%v", pub, err)
	}

	saveMail := platformsettings.NewSaveMail(env.DB, mailGroup)

	// 填写了 Resend API Key 但未填发件人邮箱被拒绝
	if _, err := saveMail.Handle(ctx, apptest.Admin, platformsettings.Mail{
		FromName: "Acme", APIKey: "re_test_123456",
	}); !errors.Is(err, platformsettings.ErrRequiredField) {
		t.Fatalf("缺少发件人邮箱应当返回 ErrRequiredField，实际 %v", err)
	}

	// 正常保存 Resend 设置并构造 Mailer
	savedMail, err := saveMail.Handle(ctx, apptest.Admin, platformsettings.Mail{
		FromName: "Acme", FromEmail: "noreply@acme.example", APIKey: "re_test_123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platformsettings.BuildMailer(savedMail); err != nil {
		t.Fatalf("构造 Resend Mailer 失败：%v", err)
	}
	// API Key 为空时退回 logmail
	if _, err := platformsettings.BuildMailer(platformsettings.MailDefaults); err != nil {
		t.Fatalf("构造默认 logmail 失败：%v", err)
	}

	// 检查操作日志已记录（常规设置保存 2 次 + 邮件设置保存 1 次）
	logs, err := activity.List(ctx, env.DB, activity.Query{Tenant: "", TargetType: "settings"})
	if err != nil || len(logs.Items) != 3 {
		t.Fatalf("期望 3 条设置日志，实际 %d err=%v", len(logs.Items), err)
	}
}

func TestMailTestActionAndTemplates(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	generalGroup := platformsettings.NewGeneralGroup(env.DB)
	mailGroup := platformsettings.NewMailGroup(env.DB, env.Crypt)
	dyn := platformsettings.NewDynamicMailer(mailGroup)
	templates, err := emails.NewTemplates()
	if err != nil {
		t.Fatal(err)
	}

	ok, err := dyn.Configured(ctx)
	if err != nil || ok {
		t.Fatalf("未保存 Resend 配置时 Configured 应为 false：ok=%v err=%v", ok, err)
	}

	act := platformsettings.NewTestMail(generalGroup, mailGroup, env.AdminLogins, templates)
	if _, err := act.Handle(ctx, apptest.Admin, "bad-email"); !errors.Is(err, platformsettings.ErrInvalidEmail) {
		t.Fatalf("非法收件邮箱应返回 ErrInvalidEmail，实际 %v", err)
	}
	if _, err := act.Handle(ctx, apptest.Admin, "ops@acme.example"); !errors.Is(err, platformsettings.ErrMailNotConfigured) {
		t.Fatalf("未配置邮件服务时应返回 ErrMailNotConfigured，实际 %v", err)
	}

	if _, err := platformsettings.NewSaveMail(env.DB, mailGroup).Handle(ctx, apptest.Admin, platformsettings.Mail{
		FromName: "Acme", FromEmail: "noreply@acme.example", APIKey: "re_test_123456",
	}); err != nil {
		t.Fatal(err)
	}
	ok, err = dyn.Configured(ctx)
	if err != nil || !ok {
		t.Fatalf("保存完整 Resend 配置后 Configured 应为 true：ok=%v err=%v", ok, err)
	}

	// 验证全部邮件模板在中英文语言下均可正常渲染并产出主题与正文
	dataByTemplate := map[string]map[string]any{
		"mail_test": {
			"SiteName": "Acme Desk", "FromEmail": "noreply@acme.example",
		},
		"code_reset": {
			"SiteName": "Acme Desk", "Code": "123456", "ExpiresMinutes": 15,
		},
		"member_invite": {
			"TenantName": "Acme Workspace", "RoleName": "坐席", "MemberName": "Alice", "LoginURL": "https://desk.example.com/app/acme/", "GeneratedPassword": true,
		},
		"offline_reply": {
			"TenantName": "Acme Workspace", "ChannelName": "官网客服", "ContactName": "Visitor", "ReplyPreview": "您的包裹已发出", "ReturnURL": "https://desk.example.com/widget/?channel=1",
		},
	}
	for _, locale := range []string{"zh-CN", "en"} {
		rctx := emails.WithLocale(ctx, locale)
		for name, data := range dataByTemplate {
			msg, err := templates.Render(rctx, name, data)
			if err != nil {
				t.Fatalf("render %s (%s): %v", name, locale, err)
			}
			if msg.Subject == "" || (msg.Text == "" && msg.HTML == "") {
				t.Fatalf("rendered %s (%s) missing content: %+v", name, locale, msg)
			}
		}
	}
}
