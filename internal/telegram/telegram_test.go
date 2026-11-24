package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// fakeTelegramAPI 在进程内模拟 Telegram Bot API，记录每一次调用并返回可控响应。
type fakeTelegramAPI struct {
	mu             sync.Mutex
	validToken     string
	username       string
	setWebhookErr  string
	setWebhooks    []map[string]any
	deleteWebhooks int
	sentMessages   []map[string]any
	sentPhotos     []sentMedia
	sentDocuments  []sentMedia
	files          map[string][]byte
	nextMessageID  int64
}

type sentMedia struct {
	ChatID   string
	Caption  string
	Filename string
	Body     []byte
}

func newFakeTelegramAPI(t *testing.T, validToken, username string) (*fakeTelegramAPI, *httptest.Server, *Client) {
	t.Helper()
	f := &fakeTelegramAPI{
		validToken:    validToken,
		username:      username,
		files:         map[string][]byte{},
		nextMessageID: 100,
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv, NewClient(srv.Client(), srv.URL)
}

func (f *fakeTelegramAPI) serve(w http.ResponseWriter, r *http.Request) {
	// 文件下载：GET /file/bot<token>/<file_path>
	if strings.HasPrefix(r.URL.Path, "/file/bot") {
		rest := strings.TrimPrefix(r.URL.Path, "/file/bot")
		tok, filePath, ok := strings.Cut(rest, "/")
		if !ok || tok != f.validToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		data, exists := f.files[filePath]
		f.mu.Unlock()
		if !exists {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
		return
	}

	// Bot API 方法：POST /bot<token>/<method>
	rest := strings.TrimPrefix(r.URL.Path, "/bot")
	tok, method, ok := strings.Cut(rest, "/")
	if !ok || tok != f.validToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch method {
	case "getMe":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"id": 999, "is_bot": true, "username": f.username},
		})
	case "setWebhook":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.setWebhooks = append(f.setWebhooks, body)
		if f.setWebhookErr != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": f.setWebhookErr})
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	case "deleteWebhook":
		f.deleteWebhooks++
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	case "getFile":
		var body struct {
			FileID string `json:"file_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		filePath := "photos/" + body.FileID
		data, exists := f.files[filePath]
		if !exists {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"file not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"file_id": body.FileID, "file_path": filePath, "file_size": len(data)},
		})
	case "sendMessage":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.sentMessages = append(f.sentMessages, body)
		f.nextMessageID++
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.nextMessageID}})
	case "sendPhoto", "sendDocument":
		_ = r.ParseMultipartForm(10 << 20)
		field := "photo"
		if method == "sendDocument" {
			field = "document"
		}
		file, hdr, err := r.FormFile(field)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(file)
		_ = file.Close()
		item := sentMedia{ChatID: r.FormValue("chat_id"), Caption: r.FormValue("caption"), Filename: hdr.Filename, Body: data}
		if method == "sendPhoto" {
			f.sentPhotos = append(f.sentPhotos, item)
		} else {
			f.sentDocuments = append(f.sentDocuments, item)
		}
		f.nextMessageID++
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.nextMessageID}})
	default:
		http.NotFound(w, r)
	}
}

func samplePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setupTenant(t *testing.T, env *apptest.Env, slug string) (string, identity.Subject, identity.Subject) {
	t.Helper()
	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(context.Background(), apptest.Admin, tenant.CreateParams{
		Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	agent := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}
	return tn.ID, sys, agent
}

func TestTelegramChannelCRUDAndModes(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	_, sysA, _ := setupTenant(t, env, "tg-alpha")
	_, sysB, _ := setupTenant(t, env, "tg-beta")

	fake, _, client := newFakeTelegramAPI(t, "123456:valid-bot-token", "support_demo_bot")

	// 无效 Token 立即失败
	if _, err := NewCreate(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sysA, CreateBody{
		Name: "坏机器人", BotToken: "123456:wrong-token",
	}); !errors.Is(err, ErrInvalidBotToken) {
		t.Fatalf("expected ErrInvalidBotToken, got %v", err)
	}

	// 直连模式：自动调用 setWebhook
	direct, err := NewCreate(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sysA, CreateBody{
		Name: "直连机器人", BotToken: "123456:valid-bot-token", GatewayManaged: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.BotUsername != "support_demo_bot" || direct.GatewayManaged || !direct.Enabled {
		t.Fatalf("unexpected direct dto: %+v", direct)
	}
	if len(fake.setWebhooks) != 1 || fake.setWebhooks[0]["url"] != "https://desk.example.com/webhooks/telegram/"+direct.ID {
		t.Fatalf("expected 1 setWebhook call, got %+v", fake.setWebhooks)
	}

	// 跨工作区隔离：beta 访问不到 alpha 的渠道
	if _, err := NewGet(env.DB).Handle(ctx, sysB, direct.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("expected ErrNotFound across tenants, got %v", err)
	}
	if _, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, sysB, direct.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for installation across tenants, got %v", err)
	}

	// 直连模式停用调用 deleteWebhook；轮换密钥时不调用（已停用）
	if _, err := NewUpdate(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sysA, direct.ID, UpdateBody{
		Name: "直连机器人", GatewayManaged: false, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	if fake.deleteWebhooks != 1 {
		t.Fatalf("expected deleteWebhook once, got %d", fake.deleteWebhooks)
	}

	// 外部网关转发模式：创建、停用、轮换密钥均不调用 setWebhook / deleteWebhook
	gw, err := NewCreate(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sysA, CreateBody{
		Name: "网关机器人", BotToken: "123456:valid-bot-token", GatewayManaged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.setWebhooks) != 1 {
		t.Fatalf("gateway mode should not call setWebhook, got %d", len(fake.setWebhooks))
	}
	instBefore, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, sysA, gw.ID)
	if err != nil {
		t.Fatal(err)
	}
	instAfter, err := NewRotateSecret(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sysA, gw.ID)
	if err != nil {
		t.Fatal(err)
	}
	if instAfter.WebhookSecret == instBefore.WebhookSecret || instAfter.WebhookSecret == "" {
		t.Fatalf("expected rotated webhook secret")
	}
	if len(fake.setWebhooks) != 1 {
		t.Fatalf("gateway rotate secret should not call setWebhook, got %d", len(fake.setWebhooks))
	}

	// 直连模式 setWebhook 失败时自动回滚，不留孤儿渠道
	fake.setWebhookErr = "bad webhook host"
	if _, err := NewCreate(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sysA, CreateBody{
		Name: "回滚机器人", BotToken: "123456:valid-bot-token", GatewayManaged: false,
	}); !errors.Is(err, ErrWebhookFailed) {
		t.Fatalf("expected ErrWebhookFailed, got %v", err)
	}
	list, err := NewList(env.DB).Handle(ctx, sysA, &ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 2 {
		t.Fatalf("expected 2 channels after rollback, got %d", list.Total)
	}
}

func TestTelegramWebhookReceiveGatewayIdentityAndSend(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tenantID, sys, agent := setupTenant(t, env, "tg-flow")

	fake, _, client := newFakeTelegramAPI(t, "123456:valid-bot-token", "trader_bot")
	pngData := samplePNG(t)
	fake.files["photos/photo-file-1"] = pngData

	gw, err := NewCreate(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sys, CreateBody{
		Name: "业务网关机器人", BotToken: "123456:valid-bot-token", GatewayManaged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, sys, gw.ID)
	if err != nil {
		t.Fatal(err)
	}

	reg := channel.DefaultRegistry()
	conn := NewConnector(env.Crypt, env.Files, client)
	reg[channel.Telegram] = conn
	down := conversation.Downstream{Media: conversation.Media{Files: env.Files}}
	recv := conversation.NewReceive(env.DB, reg, down)
	wh := NewWebhook(env.DB, env.Crypt, env.Files, client, recv, down)

	r := chi.NewRouter()
	r.Post("/webhooks/telegram/{channel}", wh.ServeHTTP)

	postUpdate := func(secret string, headers map[string]string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram/"+gw.ID, strings.NewReader(body))
		if secret != "" {
			req.Header.Set(SecretHeader, secret)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 1. 密钥错误返回 403
	if rec := postUpdate("wrong-secret", nil, `{"update_id":1}`); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for bad secret, got %d", rec.Code)
	}

	// 2. 客户先在未绑定状态下发私聊消息（建立线索与会话），重复投递幂等
	msg1 := `{"update_id":10,"message":{"message_id":101,"from":{"id":777001,"first_name":"Rahul","username":"rahul_tg"},"chat":{"id":777001,"type":"private"},"text":"hi, need help"}}`
	if rec := postUpdate(inst.WebhookSecret, nil, msg1); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := postUpdate(inst.WebhookSecret, nil, msg1); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on duplicate, got %d", rec.Code)
	}

	// 3. 随后客户绑定账号，外部网关带 X-Agents-Desk-External-Id 转发图片消息 → 自动合并旧线索并入库图片附件
	msg2 := `{"update_id":11,"message":{"message_id":102,"from":{"id":777001,"first_name":"Rahul"},"chat":{"id":777001,"type":"private"},"caption":"receipt attached","photo":[{"file_id":"photo-file-1","width":4,"height":4}]}}`
	rec := postUpdate(inst.WebhookSecret, map[string]string{
		ExternalIDHeader:    "10086",
		ExternalEmailHeader: "rahul@example.com",
		ExternalNameHeader:  "Rahul Sharma",
	}, msg2)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for photo update, got %d: %s", rec.Code, rec.Body.String())
	}

	// 验证原线索已合并为 external_user_id = 10086 的唯一用户，且同一会话包含 2 条消息（去重生效）且第 2 条含图片附件
	var contacts []contact.Contact
	if err := env.DB.NewSelect().Model(&contacts).Where("tenant_id = ?", tenantID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Kind != contact.User || contacts[0].ExternalUserID != "10086" || contacts[0].Email != "rahul@example.com" {
		t.Fatalf("expected single merged user 10086, got %+v", contacts)
	}

	var convs []conversation.Conversation
	if err := env.DB.NewSelect().Model(&convs).Where("tenant_id = ?", tenantID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(convs) != 1 || convs[0].ContactID == nil || *convs[0].ContactID != contacts[0].ID {
		t.Fatalf("expected 1 conversation owned by merged user, got %+v", convs)
	}
	timeline, err := conversation.NewTimeline(env.DB, down.Media).Handle(ctx, agent, convs[0].ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 2 || len(timeline[1].Attachments) != 1 || timeline[1].Attachments[0].Kind != conversation.AttachmentImage {
		t.Fatalf("expected 2 timeline items with confirmed image attachment on 2nd item, got %+v", timeline)
	}

	// 4. 坐席回复文本 + 图片附件 → Connector 通过 Telegram Bot API 发出
	uploaded, err := env.Files.Upload(ctx, agent, filesystem.FileInput{
		Filename: "reply.png", ContentType: "image/png", Size: int64(len(pngData)),
	}, bytes.NewReader(pngData))
	if err != nil {
		t.Fatal(err)
	}
	replyOut, err := conversation.NewReply(env.DB, down).Handle(ctx, agent, convs[0].ID, conversation.ReplyBody{
		Text: "Here is your receipt", Attachments: []string{uploaded.ID.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.NewDeliver(env.DB, reg, down).Job(ctx, conversation.Appended{
		TenantID: tenantID, ConversationID: convs[0].ID, ItemID: replyOut.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.sentPhotos) != 1 || fake.sentPhotos[0].ChatID != "777001" || fake.sentPhotos[0].Caption != "Here is your receipt" {
		t.Fatalf("expected 1 sent photo to 777001, got %+v", fake.sentPhotos)
	}

	// 5. 坐席发纯文本回复 → 调用 sendMessage
	textOut, err := conversation.NewReply(env.DB, down).Handle(ctx, agent, convs[0].ID, conversation.ReplyBody{Text: "Anything else?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.NewDeliver(env.DB, reg, down).Job(ctx, conversation.Appended{
		TenantID: tenantID, ConversationID: convs[0].ID, ItemID: textOut.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.sentMessages) != 1 || fake.sentMessages[0]["chat_id"] != "777001" || fake.sentMessages[0]["text"] != "Anything else?" {
		t.Fatalf("expected 1 sent text message, got %+v", fake.sentMessages)
	}
}

// TestClientRedactsBotTokenInErrors 检查网络层出错时错误信息不包含明文 Bot Token。
func TestClientRedactsBotTokenInErrors(t *testing.T) {
	t.Parallel()
	const secretToken = "999888:SUPER_SECRET_BOT_TOKEN"
	cli := NewClient(&http.Client{Timeout: 50 * time.Millisecond}, "http://127.0.0.1:1")
	_, err := cli.SendMessage(context.Background(), secretToken, "12345", "hello")
	if err == nil {
		t.Fatal("expected network error")
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("error leaked bot token: %v", err)
	}
}
