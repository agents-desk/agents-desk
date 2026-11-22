package platformsettings_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/activity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/platformsettings"
)

func TestGeneralAndMailSettings(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	generalGroup := platformsettings.NewGeneralGroup(env.DB)
	mailGroup := platformsettings.NewMailGroup(env.DB)

	// 默认值
	gotGeneral, err := generalGroup.Get(ctx)
	if err != nil || gotGeneral != platformsettings.GeneralDefaults {
		t.Fatalf("默认常规设置：%+v err=%v", gotGeneral, err)
	}
	gotMail, err := mailGroup.Get(ctx)
	if err != nil || gotMail != platformsettings.MailDefaults {
		t.Fatalf("默认邮件设置：%+v err=%v", gotMail, err)
	}

	saveGeneral := platformsettings.NewSaveGeneral(env.DB, generalGroup)
	// 非法邮箱被拒绝
	if _, err := saveGeneral.Handle(ctx, apptest.Admin, platformsettings.General{
		SiteName: "Desk", SupportEmail: "not-an-email", DefaultLanguage: platformsettings.LangZhCN,
	}); !errors.Is(err, platformsettings.ErrInvalidEmail) {
		t.Fatalf("非法邮箱应当返回 ErrInvalidEmail，实际 %v", err)
	}

	// 保存常规设置并写入操作日志
	if _, err := saveGeneral.Handle(ctx, apptest.Admin, platformsettings.General{
		SiteName: "Acme Desk", SupportEmail: "support@acme.example", DefaultLanguage: platformsettings.LangEN,
	}); err != nil {
		t.Fatal(err)
	}
	gotGeneral, err = generalGroup.Get(ctx)
	if err != nil || gotGeneral.SiteName != "Acme Desk" || gotGeneral.DefaultLanguage != platformsettings.LangEN {
		t.Fatalf("保存后的常规设置：%+v err=%v", gotGeneral, err)
	}

	saveMail := platformsettings.NewSaveMail(env.DB, mailGroup)
	// 填写用户名时关闭 TLS 被拒绝
	if _, err := saveMail.Handle(ctx, apptest.Admin, platformsettings.Mail{
		Host: "smtp.acme.example", Port: 25, TLS: platformsettings.TLSNone, Username: "mailer",
	}); !errors.Is(err, platformsettings.ErrTLSRequired) {
		t.Fatalf("有用户名关闭 TLS 应当返回 ErrTLSRequired，实际 %v", err)
	}

	// 正常保存邮件设置并记录操作日志（不记录密码值）
	if _, err := saveMail.Handle(ctx, apptest.Admin, platformsettings.Mail{
		Host: "smtp.acme.example", Port: 587, TLS: platformsettings.TLSStartTLS,
		Username: "mailer", Password: "super-secret-password", FromName: "Acme", FromEmail: "noreply@acme.example",
	}); err != nil {
		t.Fatal(err)
	}
	logs, err := activity.List(ctx, env.DB, activity.Query{Tenant: "", TargetType: "settings"})
	if err != nil || len(logs) != 2 {
		t.Fatalf("期望 2 条设置日志，实际 %d err=%v", len(logs), err)
	}
	if logs[0].Event != "platform_settings.mail_updated" || logs[1].Event != "platform_settings.general_updated" {
		t.Fatalf("设置日志事件不符合预期：%+v", logs)
	}
}
