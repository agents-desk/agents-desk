package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/automation"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
	"agents-desk/internal/widget"
)

// callBearer 向 h 发送请求，token 非空时放在 Authorization: Bearer 中：聊天窗口在第三方网站中，不用 cookie。
func callBearer(h http.Handler, method, path, body, token string) response {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	return response{status: rec.Code, code: problem.Code, body: rec.Body.String()}
}

// TestWidgetAPI 在完整的路由上检查聊天插件：有 channels:manage 的成员新建聊天插件、读取嵌入代码，坐席没有这个权限；
// 客户读取公开设置、建立会话，用联系人的令牌发起会话、发消息、读取自己的会话和时间线；联系人的令牌只能用在自己的聊天插件中，
// 不能调用工作区的接口，成员的登录凭据也不能调用聊天插件的接口；客户经 /w/{channel}/realtime 只能订阅自己的频道，
// 收到坐席的回复。
func TestWidgetAPI(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	hub := broadcast.New(broadcasttest.NewBackend(), nil)
	t.Cleanup(func() { _ = hub.Close() })
	a := &App{
		cfg: config.Default[Config](), db: env.DB, hub: hub, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		contactTokens: apitoken.NewStore(env.DB, tokens), crypt: env.Crypt,
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
		channels: registry(hub),
	}
	rt, err := a.newWidgetRealtime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	h := a.routes(nil)
	h.With(widget.Middleware(env.DB)).Handle("/w/{channel}/realtime", rt.Handler())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	if _, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
	}); err != nil {
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
	decode := func(r response, want int, v any) {
		t.Helper()
		if r.status != want {
			t.Fatalf("应当是 %d：%d %s", want, r.status, r.body)
		}
		if err := json.Unmarshal([]byte(r.body), v); err != nil {
			t.Fatal(err)
		}
	}
	owner := login("owner@alpha.com")

	// 管理：主管理员新建两个聊天插件，读取嵌入代码；坐席没有 channels:manage
	var w, other widget.WidgetDTO
	decode(call(h, "POST", "/t/alpha/api/widgets", `{"name":"官网","greeting":"你好"}`, owner), http.StatusCreated, &w)
	decode(call(h, "POST", "/t/alpha/api/widgets", `{"name":"App"}`, owner), http.StatusCreated, &other)
	var inst widget.InstallationDTO
	decode(call(h, "GET", "/t/alpha/api/widgets/"+w.ID+"/installation", "", owner), http.StatusOK, &inst)
	if !strings.Contains(inst.EmbedCode, w.ID) || !strings.Contains(inst.EmbedCode, "/widget/loader.js") || inst.IdentitySecret == "" {
		t.Fatalf("安装内容：%+v", inst)
	}
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	if r := call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"agent","password":"password","role_id":"`+agentRole+`"}`, owner); r.status != http.StatusCreated {
		t.Fatalf("添加坐席：%d %s", r.status, r.body)
	}
	agent := login("agent@alpha.com")
	if r := call(h, "GET", "/t/alpha/api/widgets", "", agent); r.status != http.StatusForbidden {
		t.Errorf("坐席读取聊天插件：%d", r.status)
	}

	// 客户：公开设置、建立会话、发起会话、发消息、读取
	base := "/w/" + w.ID + "/api"
	var cfg widget.ConfigDTO
	decode(callBearer(h, "GET", base+"/config", "", ""), http.StatusOK, &cfg)
	if cfg.Name != "官网" || cfg.Greeting != "你好" {
		t.Fatalf("公开设置：%+v", cfg)
	}
	if r := callBearer(h, "GET", "/w/00000000-0000-7000-8000-000000000000/api/config", "", ""); r.status != http.StatusNotFound || r.code != "channel.not_found" {
		t.Errorf("不存在的聊天插件：%d %s", r.status, r.code)
	}
	if r := callBearer(h, "GET", "/w/not-a-uuid/api/config", "", ""); r.status != http.StatusNotFound {
		t.Errorf("不是 UUID 的渠道：%d", r.status)
	}
	var session widget.SessionDTO
	decode(callBearer(h, "POST", base+"/session", `{}`, ""), http.StatusOK, &session)
	if r := callBearer(h, "POST", base+"/session", `{"user_token":"bad"}`, ""); r.status != http.StatusUnauthorized || r.code != "widget.invalid_user_token" {
		t.Errorf("无效的用户身份：%d %s", r.status, r.code)
	}
	var sent widget.Sent
	decode(callBearer(h, "POST", base+"/conversations", `{"text":"mera order nahi aaya","client_id":"m1"}`, session.Token), http.StatusCreated, &sent)
	decode(callBearer(h, "POST", base+"/conversations/"+sent.ConversationID+"/messages", `{"text":"kab aayega?"}`, session.Token), http.StatusCreated, &sent)
	var list []conversation.ContactConversationDTO
	decode(callBearer(h, "GET", base+"/conversations", "", session.Token), http.StatusOK, &list)
	if len(list) != 1 || list[0].ID != sent.ConversationID || list[0].Preview != "kab aayega?" {
		t.Fatalf("我的会话：%+v", list)
	}

	// 令牌只用于自己的聊天插件；联系人的令牌不能调用工作区的接口；成员的凭据不能调用聊天插件的接口
	if r := callBearer(h, "GET", "/w/"+other.ID+"/api/conversations", "", session.Token); r.status != http.StatusUnauthorized {
		t.Errorf("别的聊天插件：%d", r.status)
	}
	if r := callBearer(h, "GET", "/t/alpha/api/inbox", "", session.Token); r.status != http.StatusUnauthorized {
		t.Errorf("联系人调用工作区的接口：%d", r.status)
	}
	if r := call(h, "GET", base+"/conversations", "", owner); r.status != http.StatusUnauthorized {
		t.Errorf("成员调用聊天插件的接口：%d", r.status)
	}
	if r := callBearer(h, "GET", base+"/conversations", "", ""); r.status != http.StatusUnauthorized {
		t.Errorf("没有令牌：%d", r.status)
	}

	// 实时频道：只能订阅自己的；坐席回复后收到推送
	ws := dialWidgetRealtime(t, srv.URL+"/w/"+w.ID+"/realtime", session.Token)
	if code := ws.subscribe(widget.Channel(w.ID, session.Contact.ID)); code != 0 {
		t.Fatalf("订阅自己的频道：%d", code)
	}
	var stranger widget.SessionDTO
	decode(callBearer(h, "POST", base+"/session", `{}`, ""), http.StatusOK, &stranger)
	for name, ch := range map[string]string{
		"别人的频道":      widget.Channel(w.ID, stranger.Contact.ID),
		"别的聊天插件中的频道": widget.Channel(other.ID, session.Contact.ID),
	} {
		if code := ws.subscribe(ch); code != 103 {
			t.Errorf("订阅%s：%d，应当是 103", name, code)
		}
	}
	var reply conversation.ItemDTO
	decode(call(h, "POST", "/t/alpha/api/conversations/"+sent.ConversationID+"/replies", `{"text":"您好"}`, owner), http.StatusCreated, &reply)
	if err := conversation.NewDeliver(env.DB, a.channels).Job(ctx, conversation.Appended{
		TenantID: tenantOf(t, env, "alpha"), ConversationID: sent.ConversationID, ItemID: reply.ID, Seq: reply.Seq,
	}); err != nil {
		t.Fatal(err)
	}
	if ev := ws.publication(); ev.ID != sent.ConversationID || ev.Seq != reply.Seq {
		t.Fatalf("推送：%+v", ev)
	}
	var items []conversation.ContactItemDTO
	decode(callBearer(h, "GET", base+"/conversations/"+sent.ConversationID+"/items", "", session.Token), http.StatusOK, &items)
	if len(items) != 3 || items[2].ID != reply.ID || items[2].Author.Name != "owner" {
		t.Fatalf("时间线：%+v", items)
	}
	if r := callBearer(h, "POST", base+"/conversations/"+sent.ConversationID+"/read", `{"seq":`+strconv.FormatInt(reply.Seq, 10)+`}`, session.Token); r.status != http.StatusNoContent {
		t.Fatalf("标记已读：%d %s", r.status, r.body)
	}
	if r := callBearer(h, "GET", base+"/conversations/"+sent.ConversationID, "", stranger.Token); r.status != http.StatusNotFound {
		t.Errorf("别人读取会话：%d", r.status)
	}
	// 评价：打开的会话不能评价，星数在 1 到 5 之间
	if r := callBearer(h, "POST", base+"/conversations/"+sent.ConversationID+"/rating", `{"score":5}`, session.Token); r.status != http.StatusConflict || r.code != "conversation.not_closed" {
		t.Errorf("评价打开的会话：%d %s", r.status, r.code)
	}
	if r := callBearer(h, "POST", base+"/conversations/"+sent.ConversationID+"/rating", `{"score":6}`, session.Token); r.status != http.StatusUnprocessableEntity {
		t.Errorf("6 星：%d", r.status)
	}
	var closed conversation.ConversationDTO
	decode(call(h, "POST", "/t/alpha/api/conversations/"+sent.ConversationID+"/close", "", owner), http.StatusOK, &closed)
	// 自动化处理关闭事件后发出评价邀请，客户才能评价
	var closeItemID string
	if err := env.DB.NewRaw("SELECT id FROM timeline_items WHERE conversation_id = ? AND seq = ?", sent.ConversationID, closed.LastSeq).Scan(ctx, &closeItemID); err != nil {
		t.Fatal(err)
	}
	if err := automation.NewRunner(env.DB, nil, a.downstream()).Job(ctx, conversation.Appended{
		TenantID: tenantOf(t, env, "alpha"), ConversationID: sent.ConversationID, ItemID: closeItemID, Seq: closed.LastSeq,
	}); err != nil {
		t.Fatal(err)
	}
	var rated conversation.ContactConversationDTO
	decode(callBearer(h, "POST", base+"/conversations/"+sent.ConversationID+"/rating", `{"score":5,"comment":"谢谢"}`, session.Token), http.StatusOK, &rated)
	if rated.Status != conversation.Closed || !rated.RatingRequested || rated.Rating == nil || rated.Rating.Score != 5 {
		t.Fatalf("评价后：%+v", rated)
	}
}

// TestPageCSP 检查页面的内容安全策略：聊天窗口的页面按聊天插件允许嵌入的网站生成 frame-ancestors，
// 不存在的聊天插件和其他页面都不能被嵌入。
func TestPageCSP(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	a := &App{db: env.DB}
	if _, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
	}); err != nil {
		t.Fatal(err)
	}
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tenantOf(t, env, "alpha")}
	open, err := widget.NewCreate(env.DB, env.Crypt).Handle(ctx, system, widget.Changes{Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	limited, err := widget.NewCreate(env.DB, env.Crypt).Handle(ctx, system, widget.Changes{Name: "App", AllowedOrigins: []string{"https://shop.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{
		"/widget/?channel=" + open.ID:    "frame-ancestors *",
		"/widget/?channel=" + limited.ID: "frame-ancestors https://shop.example.com",
		"/widget/?channel=x":             "frame-ancestors 'none'",
		"/widget/demo":                   "frame-ancestors 'none'",
		"/t/alpha/inbox":                 "frame-ancestors 'none'",
		"/widgets?channel=" + open.ID:    "frame-ancestors 'none'",
	} {
		csp := a.pageCSP(httptest.NewRequest(http.MethodGet, target, nil), "n0nce")
		if !strings.Contains(csp, want+";") || !strings.Contains(csp, "'nonce-n0nce'") {
			t.Errorf("%s：%s", target, csp)
		}
	}
}

// tenantOf 返回工作区 slug 的 ID。
func tenantOf(t *testing.T, env *apptest.Env, slug string) string {
	t.Helper()
	var id string
	if err := env.DB.NewRaw("SELECT id FROM tenants WHERE slug = ?", slug).Scan(context.Background(), &id); err != nil {
		t.Fatal(err)
	}
	return id
}

// dialWidgetRealtime 像聊天窗口一样建立实时连接：令牌在 connect 命令中由 SDK 传入，不用 cookie。
func dialWidgetRealtime(t *testing.T, url, token string) *realtimeClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{"Origin": {url[:strings.Index(url, "/w/")]}}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http"), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	c := &realtimeClient{t: t, conn: conn}
	c.send(map[string]any{"connect": map[string]any{"token": token}})
	return c
}
