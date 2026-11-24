package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestPreferences 检查成员的偏好设置：新账号的默认值，保存后 /me 返回新值，不认识的时区和不在列表中的语言被拒绝，
// 时区为空表示跟随浏览器。
func TestPreferences(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk,
	}
	h := a.routes(nil)
	if _, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "demo", Slug: "demo", OwnerEmail: "owner@example.com", OwnerName: "owner", OwnerPassword: "password",
	}); err != nil {
		t.Fatal(err)
	}
	session := call(h, "POST", "/api/auth/login", `{"email":"owner@example.com","password":"password"}`, nil).cookie
	prefs := func() (language, zone string) {
		r := call(h, "GET", "/api/me", "", session)
		var p struct {
			Language string `json:"language"`
			TimeZone string `json:"time_zone"`
		}
		if err := json.Unmarshal([]byte(r.body), &p); err != nil || r.status != http.StatusOK {
			t.Fatalf("/me：%d %s", r.status, r.body)
		}
		return p.Language, p.TimeZone
	}

	if lang, zone := prefs(); lang != "zh-CN" || zone != "" {
		t.Fatalf("新账号：%q %q", lang, zone)
	}
	if r := call(h, "PUT", "/api/me/preferences", `{"language":"en","time_zone":"Asia/Kolkata"}`, session); r.status != http.StatusOK {
		t.Fatalf("保存：%d %s", r.status, r.body)
	}
	if lang, zone := prefs(); lang != "en" || zone != "Asia/Kolkata" {
		t.Fatalf("保存后：%q %q", lang, zone)
	}
	for _, body := range []string{`{"language":"en","time_zone":"Mars/Olympus"}`, `{"language":"en","time_zone":"Local"}`} {
		if r := call(h, "PUT", "/api/me/preferences", body, session); r.status != http.StatusUnprocessableEntity || r.code != "member.invalid_time_zone" || fieldOf(r.body) != "body.time_zone" {
			t.Errorf("%s：%d %s", body, r.status, r.body)
		}
	}
	if r := call(h, "PUT", "/api/me/preferences", `{"language":"fr"}`, session); r.status != http.StatusUnprocessableEntity || fieldOf(r.body) != "body.language" {
		t.Errorf("不在列表中的语言：%d %s", r.status, r.body)
	}
	// 被拒绝的修改不改变已保存的值；不传时区表示跟随浏览器
	if lang, zone := prefs(); lang != "en" || zone != "Asia/Kolkata" {
		t.Fatalf("被拒绝后：%q %q", lang, zone)
	}
	if r := call(h, "PUT", "/api/me/preferences", `{"language":"zh-CN"}`, session); r.status != http.StatusOK {
		t.Fatalf("跟随浏览器：%d %s", r.status, r.body)
	}
	if lang, zone := prefs(); lang != "zh-CN" || zone != "" {
		t.Fatalf("跟随浏览器后：%q %q", lang, zone)
	}
}
