package member_test

import (
	"context"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestPreferencesAndNotifications 检查成员的回复发送偏好、发送后自动打开下一个待回复，以及实时提醒设置的默认值与修改持久化。
func TestPreferencesAndNotifications(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "prefs", Slug: "prefs", OwnerEmail: "owner@prefs.com", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID}

	// 新建成员默认：enter 发送、不自动打开下一个、三类通知全部开启
	prefs := member.NewUpdatePreferences(env.DB, env.MemberLogins, env.Files)
	got, err := prefs.Handle(ctx, sub, member.Preferences{
		Language:           member.SimplifiedChinese,
		TimeZone:           "Asia/Shanghai",
		SendShortcut:       member.SendCtrlEnter,
		OpenNextAfterReply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SendShortcut != member.SendCtrlEnter || !got.OpenNextAfterReply || got.TimeZone != "Asia/Shanghai" {
		t.Fatalf("更新偏好后结果不符：%+v", got)
	}
	if !got.Notifications.Assigned.Browser || !got.Notifications.Assigned.Sound ||
		!got.Notifications.NewMessage.Browser || !got.Notifications.NewMessage.Sound ||
		!got.Notifications.Mention.Browser || !got.Notifications.Mention.Sound {
		t.Fatalf("默认实时提醒应当全部开启：%+v", got.Notifications)
	}

	// 不传 send_shortcut 时默认回落为 enter
	got, err = prefs.Handle(ctx, sub, member.Preferences{
		Language:           member.English,
		OpenNextAfterReply: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SendShortcut != member.SendEnter || got.OpenNextAfterReply || got.Language != member.English {
		t.Fatalf("再次更新偏好后结果不符：%+v", got)
	}

	// 修改实时提醒开关：关闭部分项，验证准确持久化
	notifs := member.NewUpdateNotifications(env.DB, env.MemberLogins, env.Files)
	want := member.NotificationSettings{
		Assigned:   member.AlertChannel{Browser: true, Sound: false},
		NewMessage: member.AlertChannel{Browser: false, Sound: true},
		Mention:    member.AlertChannel{Browser: false, Sound: false},
	}
	got, err = notifs.Handle(ctx, sub, want)
	if err != nil {
		t.Fatal(err)
	}
	if got.Notifications != want {
		t.Fatalf("通知设置未生效：got %+v, want %+v", got.Notifications, want)
	}

	// 再次提交相同设置应保持幂等
	got2, err := notifs.Handle(ctx, sub, want)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Notifications != want {
		t.Fatalf("幂等提交后通知设置异常：%+v", got2.Notifications)
	}
}
