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
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestMacrosHTTP 在完整 HTTP 路由上验证 M1：快捷回复的共享/个人权限、CRUD、跨工作区隔离，
// 以及回复附带原子操作和纯附带操作接口。
func TestMacrosHTTP(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
		channels: channel.DefaultRegistry(),
	}
	h := a.routes(nil)

	createTenant := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "B主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	idOf := func(body string) string {
		var v struct{ ID string }
		_ = json.Unmarshal([]byte(body), &v)
		return v.ID
	}

	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"坐席","password":"password","role_id":"`+agentRole+`"}`, owner)
	agent := login("agent@alpha.com")

	// 1. 准备标签和团队，用于快捷回复附带操作
	tagID := idOf(call(h, "POST", "/t/alpha/api/tags", `{"name":"退款","color":"deep-orange"}`, owner).body)
	teamID := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"一线客服","assignment":"manual"}`, owner).body)
	// 把坐席加入一线客服，让坐席能看到团队会话
	var membersResp struct {
		Items []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/members", "", owner).body), &membersResp)
	var agentID string
	for _, m := range membersResp.Items {
		if m.Email == "agent@alpha.com" {
			agentID = m.ID
		}
	}
	call(h, "PUT", "/t/alpha/api/teams/"+teamID+"/members/"+agentID, "", owner)

	// 2. 快捷回复 CRUD 与权限边界
	r := call(h, "POST", "/t/alpha/api/macros", `{"scope":"shared","title":"退款模板","shortcut":"refund","body":"您好 {{contact.name}}","actions":{"priority":"high","add_tag_ids":["`+tagID+`"],"status":{"kind":"close"}}}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("主管理员新建共享快捷回复：%d %s", r.status, r.body)
	}
	sharedID := idOf(r.body)

	// 坐席不能建/改/删共享快捷回复，但能读列表和新建自己的个人快捷回复
	if r := call(h, "POST", "/t/alpha/api/macros", `{"scope":"shared","title":"无权","body":"x"}`, agent); r.status != http.StatusForbidden {
		t.Fatalf("坐席建共享应 403：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/macros/"+sharedID, `{"title":"偷改","body":"x"}`, agent); r.status != http.StatusForbidden {
		t.Fatalf("坐席改共享应 403：%d %s", r.status, r.body)
	}
	r = call(h, "POST", "/t/alpha/api/macros", `{"scope":"personal","title":"我的关闭","shortcut":"refund","actions":{"status":{"kind":"close"}}}`, agent)
	if r.status != http.StatusCreated {
		t.Fatalf("坐席建个人快捷回复：%d %s", r.status, r.body)
	}
	personalID := idOf(r.body)

	// 跨工作区隔离
	betaMacroID := idOf(call(h, "POST", "/t/beta/api/macros", `{"scope":"shared","title":"B模板","body":"beta"}`, betaOwner).body)
	if r := call(h, "GET", "/t/alpha/api/macros/"+betaMacroID, "", owner); r.code != "macro.not_found" {
		t.Fatalf("跨工作区读快捷回复应 404：%d %s", r.status, r.body)
	}
	// 主管理员看不到坐席的个人快捷回复
	if r := call(h, "GET", "/t/alpha/api/macros/"+personalID, "", owner); r.code != "macro.not_found" {
		t.Fatalf("他人读个人快捷回复应 404：%d %s", r.status, r.body)
	}
	// 坐席搜索列表同时包含共享与个人
	if r := call(h, "GET", "/t/alpha/api/macros?q=refund", "", agent); r.status != http.StatusOK || !strings.Contains(r.body, sharedID) || !strings.Contains(r.body, personalID) {
		t.Fatalf("坐席按 shortcut 搜索：%d %s", r.status, r.body)
	}

	// 3. 在真实会话上通过 replies 附带 actions 原子提交，以及纯 actions 提交
	inAlpha := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	ch := &channel.Channel{ID: "019a0000-0000-7000-8000-000000000099", TenantID: alpha.ID, Kind: channel.Test, Name: "测试", TeamID: &teamID, Settings: map[string]any{}, Enabled: true}
	if _, err := env.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), a.downstream())
	c1, err := recv.Handle(ctx, inAlpha, conversation.ReceiveParams{ChannelID: ch.ID, From: "v1", ExternalID: "msg1", Text: "我要退款"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := recv.Handle(ctx, inAlpha, conversation.ReceiveParams{ChannelID: ch.ID, From: "v2", ExternalID: "msg2", Text: "帮我关掉"})
	if err != nil {
		t.Fatal(err)
	}
	_ = beta

	r = call(h, "POST", "/t/alpha/api/conversations/"+c1.ConversationID+"/replies",
		`{"text":"已为您处理退款","actions":{"priority":"high","add_tag_ids":["`+tagID+`"],"status":{"kind":"close"}}}`, agent)
	if r.status != http.StatusCreated {
		t.Fatalf("原子回复带操作：%d %s", r.status, r.body)
	}
	r = call(h, "GET", "/t/alpha/api/conversations/"+c1.ConversationID, "", agent)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"status":"closed"`) || !strings.Contains(r.body, `"priority":"high"`) || !strings.Contains(r.body, tagID) {
		t.Fatalf("原子回复后会话状态异常：%d %s", r.status, r.body)
	}

	// 纯附带操作接口
	r = call(h, "POST", "/t/alpha/api/conversations/"+c2.ConversationID+"/actions",
		`{"assignee":{"mode":"me"},"status":{"kind":"close"}}`, agent)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"status":"closed"`) || !strings.Contains(r.body, agentID) {
		t.Fatalf("纯附带操作接口异常：%d %s", r.status, r.body)
	}
}
