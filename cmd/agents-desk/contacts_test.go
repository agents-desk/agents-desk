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
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// TestContactsAPI 在完整的路由上检查联系人接口：未登录 401、非成员 404、工作区坐席按可见范围读取概览/列表/详情和编辑资料、
// 跨团队隔离返回 404、校验错误返回 422 和对应错误码，以及跨工作区隔离。
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
	addRes := call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"agent","password":"password","role_id":"`+agentRole+`"}`, ownerAlpha)
	if addRes.status != http.StatusCreated {
		t.Fatalf("添加坐席：%d %s", addRes.status, addRes.body)
	}
	var addedMember struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(addRes.body), &addedMember); err != nil || addedMember.ID == "" {
		t.Fatalf("解析坐席：%v %s", err, addRes.body)
	}
	agentAlpha := login("agent@alpha.com")

	// 在 alpha 建两个团队收件箱，把 agentAlpha 加入 teamA，并分别绑定渠道
	alphaID := tenantOf(t, env, "alpha")
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: alphaID}
	teamA, err := team.NewCreate(env.DB).Handle(ctx, sys, team.Changes{Name: "品牌 A"})
	if err != nil {
		t.Fatal(err)
	}
	teamB, err := team.NewCreate(env.DB).Handle(ctx, sys, team.Changes{Name: "品牌 B"})
	if err != nil {
		t.Fatal(err)
	}
	if r := call(h, "PUT", "/t/alpha/api/teams/"+teamA.ID+"/members/"+addedMember.ID, "", ownerAlpha); r.status != http.StatusOK {
		t.Fatalf("加入团队 A：%d %s", r.status, r.body)
	}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "品牌 A 渠道", TeamID: &teamA.ID})
	if err != nil {
		t.Fatal(err)
	}
	chB, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "品牌 B 渠道", TeamID: &teamB.ID})
	if err != nil {
		t.Fatal(err)
	}
	recv := conversation.NewReceive(env.DB, a.channels, a.downstream())
	if _, err := recv.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "v-1", FromName: "Rahul", Text: "hello",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := recv.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: chB.ID, From: "v-b", FromName: "BrandB Customer", Text: "hello b",
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
	var list page.Cursor[contact.ContactDTO]
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

	// 跨团队隔离：ownerAlpha（ScopeAll）能看到全部 2 个联系人和 2 个渠道，agentAlpha 读不到也改不了品牌 B 联系人
	ownerSumRes := call(h, "GET", "/t/alpha/api/contacts/summary", "", ownerAlpha)
	var ownerSum contact.SummaryDTO
	if err := json.Unmarshal([]byte(ownerSumRes.body), &ownerSum); err != nil || ownerSum.All != 2 || len(ownerSum.Channels) != 2 {
		t.Fatalf("主管理员概览：%+v %v", ownerSum, err)
	}
	ownerListBRes := call(h, "GET", "/t/alpha/api/contacts?q=BrandB", "", ownerAlpha)
	var ownerListB page.Cursor[contact.ContactDTO]
	if err := json.Unmarshal([]byte(ownerListBRes.body), &ownerListB); err != nil || len(ownerListB.Items) != 1 {
		t.Fatalf("主管理员搜品牌 B：%d %s", ownerListBRes.status, ownerListBRes.body)
	}
	otherCID := ownerListB.Items[0].ID
	if r := call(h, "GET", "/t/alpha/api/contacts/"+otherCID, "", agentAlpha); r.status != http.StatusNotFound || r.code != "contact.not_found" {
		t.Errorf("跨团队读取：%d %s", r.status, r.code)
	}
	if r := call(h, "PUT", "/t/alpha/api/contacts/"+otherCID, `{"notes":"hack"}`, agentAlpha); r.status != http.StatusNotFound || r.code != "contact.not_found" {
		t.Errorf("跨团队修改：%d %s", r.status, r.code)
	}

	// 跨工作区读不到也改不了
	if r := call(h, "GET", "/t/beta/api/contacts/"+cid, "", ownerBeta); r.status != http.StatusNotFound || r.code != "contact.not_found" {
		t.Errorf("跨工作区读取：%d %s", r.status, r.code)
	}
	if r := call(h, "PUT", "/t/beta/api/contacts/"+cid, `{"notes":"x"}`, ownerBeta); r.status != http.StatusNotFound || r.code != "contact.not_found" {
		t.Errorf("跨工作区修改：%d %s", r.status, r.code)
	}
}
