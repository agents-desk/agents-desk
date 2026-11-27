package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/filesystem"
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
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	a := &App{
		cfg: config.Default[Config](), db: env.DB, hub: hub, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		contactTokens: apitoken.NewStore(env.DB, tokens), crypt: env.Crypt,
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk,
		channels: registry(hub, env.Crypt, env.Files, nil, nil, nil),
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

	if _, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
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
	decode(call(h, "POST", "/t/alpha/api/widgets", `{"name":"官网","appearance":{"greeting":"你好"}}`, owner), http.StatusCreated, &w)
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
	if cfg.Name != "官网" || cfg.Appearance.Greeting != "你好" {
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
	decode(callBearer(h, "POST", base+"/messages", `{"text":"mera order nahi aaya","client_id":"m1"}`, session.Token), http.StatusCreated, &sent)
	decode(callBearer(h, "POST", base+"/messages", `{"text":"kab aayega?"}`, session.Token), http.StatusCreated, &sent)
	var tl conversation.ContactTimelineDTO
	decode(callBearer(h, "GET", base+"/timeline", "", session.Token), http.StatusOK, &tl)
	if len(tl.Items) != 2 || tl.LatestConversation == nil || tl.LatestConversation.ID != sent.ConversationID || tl.Items[1].Text != "kab aayega?" {
		t.Fatalf("连续时间线：%+v", tl)
	}

	// 令牌只用于自己的聊天插件；联系人的令牌不能调用工作区的接口；成员的凭据不能调用聊天插件的接口
	if r := callBearer(h, "GET", "/w/"+other.ID+"/api/timeline", "", session.Token); r.status != http.StatusUnauthorized {
		t.Errorf("别的聊天插件：%d", r.status)
	}
	if r := callBearer(h, "GET", "/t/alpha/api/inbox", "", session.Token); r.status != http.StatusUnauthorized {
		t.Errorf("联系人调用工作区的接口：%d", r.status)
	}
	if r := call(h, "GET", base+"/timeline", "", owner); r.status != http.StatusUnauthorized {
		t.Errorf("成员调用聊天插件的接口：%d", r.status)
	}
	if r := callBearer(h, "GET", base+"/timeline", "", ""); r.status != http.StatusUnauthorized {
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
	var tl2 conversation.ContactTimelineDTO
	decode(callBearer(h, "GET", base+"/timeline", "", session.Token), http.StatusOK, &tl2)
	if len(tl2.Items) != 3 || tl2.Items[2].ID != reply.ID || tl2.Items[2].Author.Name != "owner" {
		t.Fatalf("时间线：%+v", tl2)
	}
	if r := callBearer(h, "POST", base+"/read", `{"up_to_item_id":"`+reply.ID+`"}`, session.Token); r.status != http.StatusNoContent {
		t.Fatalf("标记已读：%d %s", r.status, r.body)
	}
	var strangerTL conversation.ContactTimelineDTO
	decode(callBearer(h, "GET", base+"/timeline", "", stranger.Token), http.StatusOK, &strangerTL)
	if len(strangerTL.Items) != 0 {
		t.Errorf("陌生人不应看到他人消息：%+v", strangerTL)
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
	var rated conversation.ContactConversationMeta
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
	if _, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
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
		"/widget/preview":                "frame-ancestors 'self'",
		"/widget/preview/":               "frame-ancestors 'self'",
		"/widget/demo":                   "frame-ancestors 'none'",
		"/app/alpha/inbox":               "frame-ancestors 'none'",
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

// uploadMultipart 向路径 path 发送 multipart/form-data 文件上传请求；cookie 或 bearer 二选一作为凭据。
func uploadMultipart(t *testing.T, h http.Handler, path, filename, contentType string, data []byte, cookie *http.Cookie, bearer string) response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	hdr.Set("Content-Type", contentType)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	return response{status: rec.Code, code: problem.Code, body: rec.Body.String()}
}

// TestAttachmentsHTTP 在完整的 HTTP 路由上检查聊天插件访客和工作区成员上传文件、发送纯图片与文档附件、
// 读取时间线与列表摘要，以及跨访客越权引用附件被拒绝。
func TestAttachmentsHTTP(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	a := &App{
		cfg: config.Default[Config](), db: env.DB, hub: hub, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		contactTokens: apitoken.NewStore(env.DB, tokens), crypt: env.Crypt,
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk,
		channels: registry(hub, env.Crypt, env.Files, nil, nil, nil),
	}
	h := a.routes(nil)

	if _, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
	}); err != nil {
		t.Fatal(err)
	}
	loginRes := call(h, "POST", "/api/auth/login", `{"email":"owner@alpha.com","password":"password"}`, nil)
	if loginRes.status != http.StatusNoContent {
		t.Fatalf("登录失败：%d %s", loginRes.status, loginRes.body)
	}
	owner := loginRes.cookie

	var w widget.WidgetDTO
	wRes := call(h, "POST", "/t/alpha/api/widgets", `{"name":"官网"}`, owner)
	if wRes.status != http.StatusCreated || json.Unmarshal([]byte(wRes.body), &w) != nil {
		t.Fatalf("创建聊天插件：%d %s", wRes.status, wRes.body)
	}
	base := "/w/" + w.ID + "/api"
	var visitor, stranger widget.SessionDTO
	if r := callBearer(h, "POST", base+"/session", `{}`, ""); r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &visitor) != nil {
		t.Fatalf("访客建立会话：%d %s", r.status, r.body)
	}
	if r := callBearer(h, "POST", base+"/session", `{}`, ""); r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &stranger) != nil {
		t.Fatalf("另一访客建立会话：%d %s", r.status, r.body)
	}

	// 构造一张合法 PNG
	var pngBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}

	// 未登录不能上传
	if r := uploadMultipart(t, h, base+"/files", "shot.png", "image/png", pngBuf.Bytes(), nil, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("未登录上传应返回 401，得到 %d", r.status)
	}

	// 访客上传图片并发起纯图片会话
	var vFile filesystem.File
	upRes := uploadMultipart(t, h, base+"/files", "shot.png", "image/png", pngBuf.Bytes(), nil, visitor.Token)
	if upRes.status != http.StatusCreated || json.Unmarshal([]byte(upRes.body), &vFile) != nil {
		t.Fatalf("访客上传：%d %s", upRes.status, upRes.body)
	}
	var sent widget.Sent
	sendRes := callBearer(h, "POST", base+"/messages", `{"attachments":[{"file_id":"`+vFile.ID.String()+`"}],"client_id":"img-1"}`, visitor.Token)
	if sendRes.status != http.StatusCreated || json.Unmarshal([]byte(sendRes.body), &sent) != nil {
		t.Fatalf("访客发送纯图片：%d %s", sendRes.status, sendRes.body)
	}
	if len(sent.Item.Attachments) != 1 || sent.Item.Attachments[0].Width != 16 || sent.Item.Attachments[0].Height != 12 {
		t.Fatalf("访客返回附件：%+v", sent.Item.Attachments)
	}

	// 访客不能引用陌生人上传的文件
	var sFile filesystem.File
	sUp := uploadMultipart(t, h, base+"/files", "other.png", "image/png", pngBuf.Bytes(), nil, stranger.Token)
	if sUp.status != http.StatusCreated || json.Unmarshal([]byte(sUp.body), &sFile) != nil {
		t.Fatalf("陌生人上传：%d %s", sUp.status, sUp.body)
	}
	if r := callBearer(h, "POST", base+"/messages", `{"attachments":[{"file_id":"`+sFile.ID.String()+`"}]}`, visitor.Token); r.status != http.StatusNotFound {
		t.Fatalf("跨访客引用文件应返回 404，得到 %d %s", r.status, r.body)
	}

	// 工作区成员上传 CSV 并回复文字+附件
	var aFile filesystem.File
	aUp := uploadMultipart(t, h, "/t/alpha/api/files", "report.csv", "text/csv", []byte("a,b\n1,2\n"), owner, "")
	if aUp.status != http.StatusCreated || json.Unmarshal([]byte(aUp.body), &aFile) != nil {
		t.Fatalf("成员上传：%d %s", aUp.status, aUp.body)
	}
	var reply conversation.ItemDTO
	rRes := call(h, "POST", "/t/alpha/api/conversations/"+sent.ConversationID+"/replies", `{"text":"请查收","attachments":[{"file_id":"`+aFile.ID.String()+`"}]}`, owner)
	if rRes.status != http.StatusCreated || json.Unmarshal([]byte(rRes.body), &reply) != nil {
		t.Fatalf("成员带附件回复：%d %s", rRes.status, rRes.body)
	}
	if len(reply.Attachments) != 1 || reply.Attachments[0].Kind != conversation.AttachmentFile || reply.Attachments[0].Filename != "report.csv" {
		t.Fatalf("成员回复附件：%+v", reply.Attachments)
	}

	// 访客读取连续时间线包含双方附件信息
	var cStream conversation.ContactTimelineDTO
	if r := callBearer(h, "GET", base+"/timeline", "", visitor.Token); r.status != http.StatusOK || json.Unmarshal([]byte(r.body), &cStream) != nil {
		t.Fatalf("访客读取连续时间线：%d %s", r.status, r.body)
	}
	if len(cStream.Items) != 2 || len(cStream.Items[0].Attachments) != 1 || len(cStream.Items[1].Attachments) != 1 {
		t.Fatalf("访客时间线附件：%+v", cStream.Items)
	}
}
