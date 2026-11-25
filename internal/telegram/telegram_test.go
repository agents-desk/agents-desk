package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
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
	"github.com/go-tenon/tenon/queue/queuetest"

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
	sentVideos     []sentMedia
	sentVoices     []sentMedia
	sentAudios     []sentMedia
	sentDocuments  []sentMedia
	getFileCalls   []string
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
		f.getFileCalls = append(f.getFileCalls, body.FileID)
		filePath := "media/" + body.FileID
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
	case "sendPhoto", "sendVideo", "sendVoice", "sendAudio", "sendDocument":
		_ = r.ParseMultipartForm(10 << 20)
		field := map[string]string{
			"sendPhoto":    "photo",
			"sendVideo":    "video",
			"sendVoice":    "voice",
			"sendAudio":    "audio",
			"sendDocument": "document",
		}[method]
		file, hdr, err := r.FormFile(field)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(file)
		_ = file.Close()
		item := sentMedia{ChatID: r.FormValue("chat_id"), Caption: r.FormValue("caption"), Filename: hdr.Filename, Body: data}
		switch method {
		case "sendPhoto":
			f.sentPhotos = append(f.sentPhotos, item)
		case "sendVideo":
			f.sentVideos = append(f.sentVideos, item)
		case "sendVoice":
			f.sentVoices = append(f.sentVoices, item)
		case "sendAudio":
			f.sentAudios = append(f.sentAudios, item)
		default:
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
	mp4Data := make([]byte, 28)
	binary.BigEndian.PutUint32(mp4Data[0:4], 20)
	copy(mp4Data[4:8], "ftyp")
	copy(mp4Data[8:12], "isom")
	copy(mp4Data[16:20], "mp41")
	oggData := append(append([]byte("OggS"), make([]byte, 24)...), []byte("OpusHead\x01\x01")...)
	fake.files["media/photo-file-1.png"] = pngData
	fake.files["media/video-thumb-1.png"] = pngData
	fake.files["media/video-file-1.mp4"] = mp4Data
	fake.files["media/voice-file-1.ogg"] = oggData
	fake.files["media/huge-thumb-1.png"] = pngData

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
	wh := NewWebhook(env.DB, env.Crypt, recv, down)
	ingestor := conversation.NewMediaIngestor(env.DB, reg, down)
	fetcher := conversation.NewFetchAttachment(env.DB, reg, down)

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

	// 3. 客户绑定账号后发图片、语音、视频、超大视频：Webhook 阶段 0 次 getFile 同步下载
	msg2 := `{"update_id":11,"message":{"message_id":102,"from":{"id":777001,"first_name":"Rahul"},"chat":{"id":777001,"type":"private"},"caption":"receipt attached","photo":[{"file_id":"photo-file-1.png","width":4,"height":4,"file_size":100}]}}`
	rec := postUpdate(inst.WebhookSecret, map[string]string{
		ExternalIDHeader:    "10086",
		ExternalEmailHeader: "rahul@example.com",
		ExternalNameHeader:  "Rahul Sharma",
	}, msg2)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for photo update, got %d: %s", rec.Code, rec.Body.String())
	}
	msg3 := `{"update_id":12,"message":{"message_id":103,"from":{"id":777001,"first_name":"Rahul"},"chat":{"id":777001,"type":"private"},"voice":{"file_id":"voice-file-1.ogg","duration":7,"mime_type":"audio/ogg","file_size":40}}}`
	if rec := postUpdate(inst.WebhookSecret, nil, msg3); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for voice update, got %d: %s", rec.Code, rec.Body.String())
	}
	msg4 := `{"update_id":13,"message":{"message_id":104,"from":{"id":777001,"first_name":"Rahul"},"chat":{"id":777001,"type":"private"},"caption":"screen recording","video":{"file_id":"video-file-1.mp4","width":1280,"height":720,"duration":18,"file_name":"demo.mp4","mime_type":"video/mp4","file_size":28,"thumbnail":{"file_id":"video-thumb-1.png","width":4,"height":4}}}}`
	if rec := postUpdate(inst.WebhookSecret, nil, msg4); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for video update, got %d: %s", rec.Code, rec.Body.String())
	}
	msg5 := `{"update_id":14,"message":{"message_id":105,"from":{"id":777001,"first_name":"Rahul"},"chat":{"id":777001,"type":"private"},"video":{"file_id":"huge-video.mp4","width":1920,"height":1080,"duration":90,"mime_type":"video/mp4","file_size":26214400,"thumbnail":{"file_id":"huge-thumb-1.png","width":4,"height":4}}}}`
	if rec := postUpdate(inst.WebhookSecret, nil, msg5); rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for huge video update, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(fake.getFileCalls) != 0 {
		t.Fatalf("webhook must not download media synchronously, got getFile calls: %v", fake.getFileCalls)
	}

	// 验证原线索已合并为 external_user_id = 10086 的唯一用户
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

	// 4. 运行异步媒体拉取队列：图片、语音、两个视频封面均应落盘；视频本体绝不被自动下载
	for _, job := range queuetest.Pending(t, env.DB, conversation.MediaIngestTopic) {
		if err := ingestor.Job(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	for _, called := range fake.getFileCalls {
		if called == "video-file-1.mp4" || called == "huge-video.mp4" {
			t.Fatalf("video body should not be auto-fetched, got getFile call: %s", called)
		}
	}

	timeline, err := conversation.NewTimeline(env.DB, down.Media).Handle(ctx, agent, convs[0].ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 5 {
		t.Fatalf("expected 5 timeline items, got %d", len(timeline))
	}
	// 第 2 条图片已就绪
	if img := timeline[1].Attachments[0]; img.Kind != conversation.AttachmentImage || img.Status != conversation.FetchReady || img.URL == "" {
		t.Fatalf("expected ready image attachment, got %+v", img)
	}
	// 第 3 条语音已就绪且带 is_voice 与时长
	if voice := timeline[2].Attachments[0]; voice.Kind != conversation.AttachmentAudio || !voice.IsVoice || voice.DurationMs != 7000 || voice.Status != conversation.FetchReady || voice.URL == "" {
		t.Fatalf("expected ready voice attachment, got %+v", voice)
	}
	// 第 4 条视频：封面已就绪，主视频仍为 remote（未占用存储）
	vidAtt := timeline[3].Attachments[0]
	if vidAtt.Kind != conversation.AttachmentVideo || vidAtt.Status != conversation.FetchRemote || vidAtt.URL != "" || vidAtt.ThumbURL == "" || vidAtt.DurationMs != 18000 {
		t.Fatalf("expected remote video with thumb, got %+v", vidAtt)
	}
	// 第 5 条超大视频：封面已就绪，主视频标记为 unavailable（channel.media_too_large）
	hugeAtt := timeline[4].Attachments[0]
	if hugeAtt.Kind != conversation.AttachmentVideo || hugeAtt.Status != conversation.FetchUnavailable || hugeAtt.Error != channel.ErrMediaTooLarge.Code() || hugeAtt.ThumbURL == "" {
		t.Fatalf("expected unavailable huge video with thumb, got %+v", hugeAtt)
	}

	// 5. 坐席点击第 4 条视频按需拉取 → 流式拉取落盘为 ready，返回可播放 URL；其他工作区访问不到
	_, _, otherAgent := setupTenant(t, env, "tg-other")
	if _, err := fetcher.Handle(ctx, otherAgent, convs[0].ID, vidAtt.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("cross-tenant fetch should fail with ErrNotFound, got %v", err)
	}
	fetchedVid, err := fetcher.Handle(ctx, agent, convs[0].ID, vidAtt.ID)
	if err != nil {
		t.Fatalf("fetch video failed: %v", err)
	}
	if fetchedVid.Status != conversation.FetchReady || fetchedVid.URL == "" || fetchedVid.ThumbURL == "" {
		t.Fatalf("expected fetched video ready with url and thumb, got %+v", fetchedVid)
	}
	// 再次点击幂等返回，不重复请求 Telegram
	callsBefore := len(fake.getFileCalls)
	if again, err := fetcher.Handle(ctx, agent, convs[0].ID, vidAtt.ID); err != nil || again.Status != conversation.FetchReady || len(fake.getFileCalls) != callsBefore {
		t.Fatalf("repeat fetch should be idempotent, got %+v %v calls=%d", again, err, len(fake.getFileCalls))
	}

	// 6. 冷缓存回收：将刚缓存的视频回收后，主文件删除并回退为 remote，封面与 source_ref 保留
	if evicted, err := conversation.EvictCachedMedia(ctx, env.DB, env.Files, time.Now().Add(time.Minute), 10); err != nil || evicted != 1 {
		t.Fatalf("expected 1 evicted cached video, got %d %v", evicted, err)
	}
	afterEvict, err := conversation.NewTimeline(env.DB, down.Media).Handle(ctx, agent, convs[0].ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if v := afterEvict[3].Attachments[0]; v.Status != conversation.FetchRemote || v.URL != "" || v.ThumbURL == "" {
		t.Fatalf("evicted video should revert to remote with thumb kept, got %+v", v)
	}

	// 7. 坐席回复文本 + 图片 / 视频 / 语音 → Connector 按类型调用 sendPhoto / sendVideo / sendVoice
	uploadedImg, err := env.Files.Upload(ctx, agent, filesystem.FileInput{
		Filename: "reply.png", ContentType: "image/png", Size: int64(len(pngData)),
	}, bytes.NewReader(pngData))
	if err != nil {
		t.Fatal(err)
	}
	uploadedVid, err := env.Files.Upload(ctx, agent, filesystem.FileInput{
		Filename: "guide.mp4", ContentType: "video/mp4", Size: int64(len(mp4Data)),
	}, bytes.NewReader(mp4Data))
	if err != nil {
		t.Fatal(err)
	}
	uploadedVoice, err := env.Files.Upload(ctx, agent, filesystem.FileInput{
		Filename: "answer.ogg", ContentType: "audio/ogg", Size: int64(len(oggData)),
	}, bytes.NewReader(oggData))
	if err != nil {
		t.Fatal(err)
	}
	replyOut, err := conversation.NewReply(env.DB, down).Handle(ctx, agent, convs[0].ID, conversation.ReplyBody{
		Text: "Here is your receipt",
		Attachments: []conversation.AttachmentInput{
			{FileID: uploadedImg.ID.String()},
			{FileID: uploadedVid.ID.String(), DurationMs: 5000, Width: 640, Height: 360},
			{FileID: uploadedVoice.ID.String(), DurationMs: 3000, IsVoice: true},
		},
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
	if len(fake.sentVideos) != 1 || fake.sentVideos[0].ChatID != "777001" {
		t.Fatalf("expected 1 sent video to 777001, got %+v", fake.sentVideos)
	}
	if len(fake.sentVoices) != 1 || fake.sentVoices[0].ChatID != "777001" {
		t.Fatalf("expected 1 sent voice to 777001, got %+v", fake.sentVoices)
	}

	// 8. 坐席发纯文本回复 → 调用 sendMessage
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
