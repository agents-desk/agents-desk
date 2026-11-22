package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestContactsAPI 在完整的路由上检查联系人接口：未登录 401、非成员 404、工作区坐席可以读取概览/列表/详情和编辑资料、
// 校验错误返回 422 和对应错误码，以及跨工作区隔离。
func TestContactsAPI(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		contactTokens: apitoken.NewStore(env.DB, tokens), crypt: env.Crypt,
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
		channels: registry(nil),
	}
	h := a.routes(nil)

	for _, slug := range []string{"alpha", "beta"} {
		if _, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: "owner", OwnerPassword: "password",
		}); err != nil {
			t.Fatal(err)
		}
	}
	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	ownerAlpha := login("owner@alpha.com")
	ownerBeta := login("owner@beta.com")
	agentRole := roleID(t, h, "alpha", "坐席", ownerAlpha)
	if r := call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"agent","password":"password","role_id":"`+agentRole+`"}`, ownerAlpha); r.status != http.StatusCreated {
		t.Fatalf("添加坐席：%d %s", r.status, r.body)
	}
	agentAlpha := login("agent@alpha.com")

	// 在 alpha 建测试渠道并收一条消息产生线索联系人
	alphaID := tenantOf(t, env, "alpha")
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: alphaID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewReceive(env.DB, a.channels, a.downstream()).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "v-1", FromName: "Rahul", Text: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	// 未登录 401，非成员 404
	if r := call(h, "GET", "/t/alpha/api/contacts", "", nil); r.status != http.StatusUnauthorized {
		t.Errorf("未登录：%d", r.status)
	}
	if r := call(h, "GET", "/t/alpha/api/contacts", "", ownerBeta); r.status != http.StatusNotFound {
		t.Errorf("非成员：%d", r.status)
	}

	// 坐席读取概览和列表
	sumRes := call(h, "GET", "/t/alpha/api/contacts/summary", "", agentAlpha)
	if sumRes.status != http.StatusOK {
		t.Fatalf("概览：%d %s", sumRes.status, sumRes.body)
	}
	var sum contact.SummaryDTO
	if err := json.Unmarshal([]byte(sumRes.body), &sum); err != nil || sum.All != 1 || sum.Leads != 1 || len(sum.Channels) != 1 {
		t.Fatalf("概览内容：%+v %v", sum, err)
	}

	listRes := call(h, "GET", "/t/alpha/api/contacts?kind=lead&q=Rahul", "", agentAlpha)
	if listRes.status != http.StatusOK {
		t.Fatalf("列表：%d %s", listRes.status, listRes.body)
	}
	var list page.Offset[contact.ContactDTO]
	if err := json.Unmarshal([]byte(listRes.body), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("列表内容：%+v %v", list, err)
	}
	cid := list.Items[0].ID

	// 坐席读取详情并修改资料
	getRes := call(h, "GET", "/t/alpha/api/contacts/"+cid, "", agentAlpha)
	if getRes.status != http.StatusOK {
		t.Fatalf("详情：%d %s", getRes.status, getRes.body)
	}
	updRes := call(h, "PUT", "/t/alpha/api/contacts/"+cid, `{"email":"rahul@example.com","location":"Mumbai","time_zone":"Asia/Kolkata","notes":"大客户"}`, agentAlpha)
	if updRes.status != http.StatusOK {
		t.Fatalf("修改：%d %s", updRes.status, updRes.body)
	}
	var updated contact.ContactDetailDTO
	if err := json.Unmarshal([]byte(updRes.body), &updated); err != nil || updated.Email != "rahul@example.com" || updated.Notes != "大客户" {
		t.Fatalf("修改结果：%+v %v", updated, err)
	}

	// 校验错误
	if r := call(h, "PUT", "/t/alpha/api/contacts/"+cid, `{"email":"bad"}`, agentAlpha); r.status != http.StatusUnprocessableEntity || r.code != "contact.invalid_email" {
		t.Errorf("无效邮箱：%d %s", r.status, r.code)
	}
	if r := call(h, "PUT", "/t/alpha/api/contacts/"+cid, `{"time_zone":"Bad/Zone"}`, agentAlpha); r.status != http.StatusUnprocessableEntity || r.code != "contact.invalid_time_zone" {
		t.Errorf("无效时区：%d %s", r.status, r.code)
	}
	if r := call(h, "PUT", "/t/alpha/api/contacts/"+cid, `{"language":"1234567890-bad"}`, agentAlpha); r.status != http.StatusUnprocessableEntity || r.code != "contact.invalid_language" {
		t.Errorf("无效语言：%d %s", r.status, r.code)
	}

	// 跨工作区读不到也改不了
	if r := call(h, "GET", "/t/beta/api/contacts/"+cid, "", ownerBeta); r.status != http.StatusNotFound || r.code != "contact.not_found" {
		t.Errorf("跨工作区读取：%d %s", r.status, r.code)
	}
	if r := call(h, "PUT", "/t/beta/api/contacts/"+cid, `{"notes":"x"}`, ownerBeta); r.status != http.StatusNotFound || r.code != "contact.not_found" {
		t.Errorf("跨工作区修改：%d %s", r.status, r.code)
	}
}
