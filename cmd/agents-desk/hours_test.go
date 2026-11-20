package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"

	"agents-desk/internal/apptest"
	"agents-desk/internal/hours"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestBusinessHours 在完整的路由上检查工作时间：默认值、三部分分别保存互不影响、校验的错误指向字段、坐席能读不能改、
// 团队收件箱单独设置和恢复使用工作区的、hours.For 取到实际使用的那一套，以及工作区之间隔离。
func TestBusinessHours(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
	}
	h := a.routes(nil)
	tenants := map[string]string{}
	for _, slug := range []string{"alpha", "beta"} {
		tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: slug + " 主管理员", OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		tenants[slug] = tn.ID
	}
	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")
	call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"坐席","password":"password","role_id":"`+roleID(t, h, "alpha", "坐席", owner)+`"}`, owner)
	agent := login("agent@alpha.com")
	type dto struct {
		TimeZone     string `json:"time_zone"`
		Week         hours.Week
		SpecialDates []hours.SpecialDate `json:"special_dates"`
		ReplyTime    string              `json:"reply_time"`
		WarnAfter    int                 `json:"warn_after"`
		AlertAfter   int                 `json:"alert_after"`
	}
	get := func(slug string, cookie *http.Cookie) dto {
		t.Helper()
		r := call(h, "GET", "/t/"+slug+"/api/settings/business-hours", "", cookie)
		var v dto
		if err := json.Unmarshal([]byte(r.body), &v); err != nil || r.status != http.StatusOK {
			t.Fatalf("读取：%d %s", r.status, r.body)
		}
		return v
	}
	const nineToSix = `[{"start":"09:00","end":"18:00"}]`
	week := func(monday string) string {
		return `{"monday":` + monday + `,"tuesday":` + nineToSix + `,"wednesday":` + nineToSix + `,"thursday":` + nineToSix +
			`,"friday":` + nineToSix + `,"saturday":[],"sunday":[]}`
	}

	// 默认值：上海时间周一到周五 09:00–18:00，几小时内回复；坐席也能读
	if got := get("alpha", agent); got.TimeZone != "Asia/Shanghai" || len(got.Week.Monday) != 1 || len(got.Week.Sunday) != 0 ||
		got.ReplyTime != "hours" || got.WarnAfter != 4*3600 || got.AlertAfter != 12*3600 || got.SpecialDates == nil {
		t.Fatalf("默认值：%+v", got)
	}

	// 时区和每周的工作时间：时间段按开始排列
	r := call(h, "PUT", "/t/alpha/api/settings/business-hours/schedule",
		`{"time_zone":"Asia/Kolkata","week":`+week(`[{"start":"14:00","end":"20:00"},{"start":"10:00","end":"13:00"}]`)+`}`, owner)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"monday":[{"start":"10:00","end":"13:00"},{"start":"14:00","end":"20:00"}]`) {
		t.Fatalf("保存时间表：%d %s", r.status, r.body)
	}
	// 特殊日期和回复时间分别保存，互不影响
	if r := call(h, "PUT", "/t/alpha/api/settings/business-hours/special-dates",
		`{"special_dates":[{"date":"2026-10-10","name":"调休","intervals":`+nineToSix+`},{"date":"2026-10-01","name":"国庆节","intervals":[]}]}`, owner); r.status != http.StatusOK {
		t.Fatalf("保存特殊日期：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/settings/business-hours/reply-time", `{"reply_time":"minutes"}`, owner); r.status != http.StatusOK {
		t.Fatalf("保存回复时间：%d %s", r.status, r.body)
	}
	got := get("alpha", agent)
	if got.TimeZone != "Asia/Kolkata" || len(got.SpecialDates) != 2 || got.SpecialDates[0].Date != "2026-10-01" || got.ReplyTime != "minutes" || got.WarnAfter != 300 || got.AlertAfter != 900 {
		t.Errorf("三部分保存后：%+v", got)
	}
	var logged int
	if err := env.DB.NewRaw("SELECT count(*) FROM activity_log WHERE event = 'tenant_settings.changed' AND tenant = ?", tenants["alpha"]).Scan(ctx, &logged); err != nil || logged != 3 {
		t.Errorf("操作日志：%d %v", logged, err)
	}
	// 没有变化时不记操作日志
	call(h, "PUT", "/t/alpha/api/settings/business-hours/reply-time", `{"reply_time":"minutes"}`, owner)
	if err := env.DB.NewRaw("SELECT count(*) FROM activity_log WHERE event = 'tenant_settings.changed' AND tenant = ?", tenants["alpha"]).Scan(ctx, &logged); err != nil || logged != 3 {
		t.Errorf("没有变化时的操作日志：%d %v", logged, err)
	}

	// 校验：错误指向字段
	for _, c := range []struct{ path, body, code, field string }{
		{"schedule", `{"time_zone":"Mars/Base","week":` + week(nineToSix) + `}`, "hours.invalid_time_zone", "body.time_zone"},
		{"schedule", `{"time_zone":"UTC","week":` + week(`[{"start":"09:00","end":"12:00"},{"start":"11:00","end":"13:00"}]`) + `}`, "hours.overlap", "body.week.monday[1].start"},
		{"schedule", `{"time_zone":"UTC","week":` + week(`[{"start":"18:00","end":"09:00"}]`) + `}`, "hours.invalid_interval", "body.week.monday[0].end"},
		{"schedule", `{"time_zone":"UTC","week":` + week(`[{"start":"9:00","end":"18:00"}]`) + `}`, "", "body.week.monday[0].start"},
		{"special-dates", `{"special_dates":[{"date":"2026-10-01","name":"a","intervals":[]},{"date":"2026-10-01","name":"b","intervals":[]}]}`, "hours.duplicate_date", "body.special_dates[1].date"},
		{"reply-time", `{"reply_time":"week"}`, "", "body.reply_time"},
	} {
		r := call(h, "PUT", "/t/alpha/api/settings/business-hours/"+c.path, c.body, owner)
		if r.status != http.StatusUnprocessableEntity || (c.code != "" && r.code != c.code) || fieldOf(r.body) != c.field {
			t.Errorf("%s %s：%d %s", c.path, c.field, r.status, r.body)
		}
	}
	// 坐席能读不能改
	if r := call(h, "PUT", "/t/alpha/api/settings/business-hours/reply-time", `{"reply_time":"day"}`, agent); r.status != http.StatusForbidden {
		t.Errorf("坐席修改：%d", r.status)
	}

	// 团队收件箱单独设置时区和每周的工作时间；hours.For 取到它，特殊日期仍是工作区的
	var team struct{ ID string }
	_ = json.Unmarshal([]byte(call(h, "POST", "/t/alpha/api/teams", `{"name":"夜班"}`, owner).body), &team)
	r = call(h, "PUT", "/t/alpha/api/teams/"+team.ID+"/business-hours", `{"schedule":{"time_zone":"UTC","week":`+week(`[{"start":"00:00","end":"24:00"}]`)+`}}`, owner)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"business_hours":{"time_zone":"UTC"`) {
		t.Fatalf("团队收件箱单独设置：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/teams/"+team.ID+"/business-hours", `{"schedule":{"time_zone":"UTC","week":`+week(`[{"start":"10:00","end":"09:00"}]`)+`}}`, owner); fieldOf(r.body) != "body.schedule.week.monday[0].end" {
		t.Errorf("团队收件箱的校验：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/teams/"+team.ID+"/business-hours", `{}`, agent); r.status != http.StatusForbidden {
		t.Errorf("坐席设置团队收件箱：%d", r.status)
	}
	monday3am := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC) // 加尔各答 08:30，还没上班
	ws, err := hours.For(ctx, env.DB, tenants["alpha"], nil)
	if err != nil || ws.OpenAt(monday3am) {
		t.Errorf("工作区的工作时间：%v", err)
	}
	own, err := hours.For(ctx, env.DB, tenants["alpha"], &team.ID)
	if err != nil || !own.OpenAt(monday3am) || own.OpenAt(time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("团队收件箱的工作时间（国庆节仍不上班）：%v", err)
	}
	// 恢复使用工作区的
	if r := call(h, "PUT", "/t/alpha/api/teams/"+team.ID+"/business-hours", `{}`, owner); r.status != http.StatusOK || strings.Contains(r.body, "business_hours") {
		t.Errorf("恢复使用工作区的：%d %s", r.status, r.body)
	}
	if own, err := hours.For(ctx, env.DB, tenants["alpha"], &team.ID); err != nil || own.OpenAt(monday3am) {
		t.Errorf("恢复后的工作时间：%v", err)
	}

	// 工作区之间隔离：beta 仍是默认值；别的工作区的团队收件箱 404，hours.For 用工作区的
	if got := get("beta", betaOwner); got.TimeZone != "Asia/Shanghai" || got.ReplyTime != "hours" {
		t.Errorf("别的工作区：%+v", got)
	}
	if r := call(h, "PUT", "/t/beta/api/teams/"+team.ID+"/business-hours", `{}`, betaOwner); r.code != "team.not_found" {
		t.Errorf("别的工作区的团队收件箱：%d %s", r.status, r.body)
	}
}
