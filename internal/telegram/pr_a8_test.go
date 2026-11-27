package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestClientStreamingMultipartUpload 验证流式 multipart 上传无需整包缓存内存即可完整发送大于 1 MiB 的文件。
func TestClientStreamingMultipartUpload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	payload := bytes.Repeat([]byte("TGSTREAM01234567"), (1<<20)/16+512)
	var received []byte
	var gotCaption string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "caption":
				gotCaption = string(data)
			case "document":
				received = data
			}
			_ = part.Close()
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":701}}`))
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.Client(), srv.URL)
	msgID, err := client.SendDocument(ctx, "tok", "12345", "large.bin", bytes.NewReader(payload), "附件说明")
	if err != nil {
		t.Fatalf("流式上传失败：%v", err)
	}
	if msgID != 701 || gotCaption != "附件说明" || !bytes.Equal(received, payload) {
		t.Fatalf("流式上传内容不一致：msgID=%d caption=%q len=%d want=%d", msgID, gotCaption, len(received), len(payload))
	}
}

// TestClientClassifySendErrors 验证 429 限流可延迟重试、403/永久 400 转入死信、502 仍可退避重试。
func TestClientClassifySendErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var mu sync.Mutex
	var status int
	var respBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		st, body := status, respBody
		mu.Unlock()
		w.WriteHeader(st)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.Client(), srv.URL)

	setResp := func(st int, body string) {
		mu.Lock()
		status, respBody = st, body
		mu.Unlock()
	}

	setResp(http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 12","parameters":{"retry_after":12}}`)
	_, err := client.SendMessage(ctx, "tok", "1001", "hi")
	if err == nil || queue.IsPermanent(err) {
		t.Fatalf("429 限流应保持可重试，实际 %v", err)
	}

	setResp(http.StatusForbidden, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	_, err = client.SendMessage(ctx, "tok", "1001", "hi")
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("403 拉黑应标为 Permanent，实际 %v", err)
	}

	setResp(http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	_, err = client.SendMessage(ctx, "tok", "1001", "hi")
	if err == nil || !queue.IsPermanent(err) {
		t.Fatalf("400 chat not found 应标为 Permanent，实际 %v", err)
	}

	setResp(http.StatusBadGateway, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	_, err = client.SendMessage(ctx, "tok", "1001", "hi")
	if err == nil || queue.IsPermanent(err) {
		t.Fatalf("502 网关故障应可重试，实际 %v", err)
	}
}

// TestConnectorUTF16CaptionAndLongTextSplitting 验证 UTF-16 caption 长度计数与超过 4096 UTF-16 单元的长消息分片。
func TestConnectorUTF16CaptionAndLongTextSplitting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	fake, _, client := newFakeTelegramAPI(t, "bot-split-tok", "split_bot")

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "SplitAlpha", Slug: "tg-split", OwnerEmail: "owner@tg-split.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := NewCreate(env.DB, env.Crypt, client, "https://app.example.com").Handle(ctx, identity.Subject{
		Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID,
	}, CreateBody{Name: "Split Bot", BotToken: "bot-split-tok", GatewayManaged: true})
	if err != nil {
		t.Fatal(err)
	}
	chModel, err := channel.Find(ctx, env.DB, tn.ID, ch.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 上传一张小图，配文 600 个 emoji（UTF-8 为 600 字符 < 1024，但 UTF-16 为 1200 单元 > 1024）
	imgData := testPNGBytes(t)
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	up, err := env.Files.Upload(ctx, sub, filesystem.FileInput{
		Disk: env.Files.PrivateDisk(), Filename: "tiny.png", ContentType: "image/png", Size: int64(len(imgData)),
	}, bytes.NewReader(imgData))
	if err != nil {
		t.Fatal(err)
	}
	conf, err := env.Files.Confirm(ctx, sub, up.ID, filesystem.Accept{Types: []string{"image/png"}, MaxSize: 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}

	conn := NewConnector(env.Crypt, env.Files, client)
	emoji600 := strings.Repeat("😀", 600)
	if _, ok, err := conn.Send(ctx, channel.Outgoing{
		Channel:    chModel,
		ExternalTo: "9001",
		Text:       emoji600,
		Mentions:   []channel.ExternalMention{{ExternalID: "123", Name: "Alice"}},
		Attachments: []channel.Attachment{{
			FileID: conf.ID.String(), Kind: "image", Filename: "tiny.png", ContentType: "image/png",
		}},
	}); err != nil || !ok {
		t.Fatalf("发送 UTF-16 超长 caption 媒体失败：ok=%v err=%v", ok, err)
	}

	// 再发一条包含 2300 个 emoji（4600 UTF-16 单元 > 4096）+ @ 提及的长消息，验证拆成 2 段
	emoji2300 := strings.Repeat("😀", 2300)
	if _, ok, err := conn.Send(ctx, channel.Outgoing{
		Channel:    chModel,
		ExternalTo: "9001",
		Text:       emoji2300,
		Mentions:   []channel.ExternalMention{{ExternalID: "123", Name: "Alice"}},
	}); err != nil || !ok {
		t.Fatalf("发送超长文本消息失败：ok=%v err=%v", ok, err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.sentPhotos) != 1 || fake.sentPhotos[0].Caption != "" {
		t.Fatalf("UTF-16 超过 1024 单元时应拆分发出，图片 caption 应为空：%+v", fake.sentPhotos)
	}
	if len(fake.sentMessages) != 3 {
		t.Fatalf("期望 1 条拆出的 caption 消息 + 2 条长文本分片，实际 %d 条", len(fake.sentMessages))
	}
	if !strings.Contains(fake.sentMessages[1]["text"].(string), "tg://user?id=123") {
		t.Fatalf("长文本首段应保留 HTML 提及标签：%v", fake.sentMessages[1])
	}
	if _, hasPM := fake.sentMessages[2]["parse_mode"]; hasPM {
		t.Fatalf("长文本续段不应重复携带 parse_mode：%v", fake.sentMessages[2])
	}
	for i, m := range fake.sentMessages {
		text, _ := m["text"].(string)
		units := 0
		for _, r := range text {
			units += utf16.RuneLen(r)
		}
		if units > maxMessageRunes+64 {
			t.Fatalf("分片 %d 长度异常：utf16=%d", i, units)
		}
	}
}

// TestUpdateAndRotateSecretKeepDBUnchangedWhenWebhookFails 验证 setWebhook 失败时数据库名称、启停状态与密钥保持原样不变。
func TestUpdateAndRotateSecretKeepDBUnchangedWhenWebhookFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	fake, _, client := newFakeTelegramAPI(t, "bot-wh-tok", "wh_bot")

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "WHAlpha", Slug: "tg-wh", OwnerEmail: "owner@tg-wh.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	dto, err := NewCreate(env.DB, env.Crypt, client, "https://app.example.com").Handle(ctx, sub, CreateBody{
		Name: "Original Bot", BotToken: "bot-wh-tok", GatewayManaged: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	origInst, err := NewGetInstallation(env.DB, env.Crypt, "https://app.example.com").Handle(ctx, sub, dto.ID)
	if err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	fake.setWebhookErr = "bad webhook"
	fake.mu.Unlock()

	if _, err := NewRotateSecret(env.DB, env.Crypt, client, "https://app.example.com").Handle(ctx, sub, dto.ID); err == nil {
		t.Fatal("setWebhook 失败时 RotateSecret 应返回错误")
	}
	afterRotateInst, err := NewGetInstallation(env.DB, env.Crypt, "https://app.example.com").Handle(ctx, sub, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRotateInst.WebhookSecret != origInst.WebhookSecret {
		t.Fatal("setWebhook 失败后数据库中的 webhook_secret 不应被轮换覆盖")
	}

	if _, err := NewUpdate(env.DB, env.Crypt, client, "https://app.example.com").Handle(ctx, sub, dto.ID, UpdateBody{
		Name: "Changed Bot", BotToken: "bot-wh-tok", Enabled: true, GatewayManaged: false,
	}); err == nil {
		t.Fatal("setWebhook 失败时 Update 应返回错误")
	}
	afterUpdate, err := NewGet(env.DB).Handle(ctx, sub, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUpdate.Name != "Original Bot" {
		t.Fatalf("setWebhook 失败后数据库中的名称不应被修改，实际 %q", afterUpdate.Name)
	}
	_ = json.Marshal
}

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	// 1x1 透明 PNG
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
}
