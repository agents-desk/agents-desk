package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/telegram"
	"agents-desk/internal/tenant"
)

func TestTelegramHTTPAndWebhook(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	// 进程内模拟 Telegram Bot API，验证管理接口权限与 Webhook 端到端链路
	botSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": 1, "is_bot": true, "username": "http_test_bot"}})
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(botSrv.Close)

	tgClient := telegram.NewClient(botSrv.Client(), botSrv.URL)
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	cfg := config.Default[Config]()
	cfg.HTTP.PublicURL = "https://desk.example.com"
	a := &App{
		cfg: cfg, db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		contactTokens: apitoken.NewStore(env.DB, tokens), crypt: env.Crypt,
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk,
		channels: registry(nil, env.Crypt, env.Files, tgClient, nil, nil),
		telegram: tgClient,
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
	cookie := loginRes.cookie

	// 未登录访问管理接口返回 401
	if r := call(h, "GET", "/t/alpha/api/telegrams", "", nil); r.status != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated, got %d", r.status)
	}

	// 管理员新建网关转发模式的 Telegram 渠道
	created := call(h, "POST", "/t/alpha/api/telegrams",
		`{"name":"客服飞机","bot_token":"123456:http-test-token","gateway_managed":true}`, cookie)
	if created.status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.status, created.body)
	}
	var tgDTO telegram.TelegramDTO
	if err := json.Unmarshal([]byte(created.body), &tgDTO); err != nil {
		t.Fatal(err)
	}
	if tgDTO.BotUsername != "http_test_bot" || !tgDTO.GatewayManaged {
		t.Fatalf("unexpected telegram dto: %+v", tgDTO)
	}

	// 读取接入参数，拿到 Webhook URL 与密钥
	instRes := call(h, "GET", "/t/alpha/api/telegrams/"+tgDTO.ID+"/installation", "", cookie)
	if instRes.status != http.StatusOK {
		t.Fatalf("expected 200 installation, got %d: %s", instRes.status, instRes.body)
	}
	var inst telegram.InstallationDTO
	if err := json.Unmarshal([]byte(instRes.body), &inst); err != nil {
		t.Fatal(err)
	}
	if inst.WebhookURL != "https://desk.example.com/webhooks/telegram/"+tgDTO.ID || inst.WebhookSecret == "" {
		t.Fatalf("unexpected installation: %+v", inst)
	}

	// 经公开 Webhook 路由模拟网关转发一条带外部身份头的私聊消息
	req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram/"+tgDTO.ID, strings.NewReader(
		`{"update_id":1,"message":{"message_id":55,"from":{"id":888001,"first_name":"Priya"},"chat":{"id":888001,"type":"private"},"text":"hello from gateway"}}`,
	))
	req.Header.Set(telegram.SecretHeader, inst.WebhookSecret)
	req.Header.Set(telegram.ExternalIDHeader, "cust-500")
	req.Header.Set(telegram.ExternalEmailHeader, "priya@example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 webhook, got %d: %s", rec.Code, rec.Body.String())
	}

	// 坐席收件箱能查到刚创建的会话
	listRes := call(h, "GET", "/t/alpha/api/conversations?scope=all", "", cookie)
	if listRes.status != http.StatusOK || !strings.Contains(listRes.body, "hello from gateway") {
		t.Fatalf("expected conversation with telegram message, got %d: %s", listRes.status, listRes.body)
	}
}
