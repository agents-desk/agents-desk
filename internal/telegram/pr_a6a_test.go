package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
)

// TestWebhookDropsBlockedContactMessages 验证已拉黑联系人发来的 Telegram Webhook 更新返回 200 并被静默丢弃，不新增时间线消息也不重开会话。
func TestWebhookDropsBlockedContactMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	tenantID, sys, _ := setupTenant(t, env, "tg-block")

	_, _, client := newFakeTelegramAPI(t, "123456:block-bot-token", "block_bot")
	gw, err := NewCreate(env.DB, env.Crypt, client, "https://desk.example.com").Handle(ctx, sys, CreateBody{
		Name: "拦截测试机器人", BotToken: "123456:block-bot-token", GatewayManaged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, sys, gw.ID)
	if err != nil {
		t.Fatal(err)
	}

	reg := channel.DefaultRegistry()
	reg[channel.Telegram] = NewConnector(env.Crypt, env.Files, client)
	down := conversation.Downstream{}
	recv := conversation.NewReceive(env.DB, reg, down)
	wh := NewWebhook(env.DB, env.Crypt, recv, down)

	r := chi.NewRouter()
	r.Post("/webhooks/telegram/{channel}", wh.ServeHTTP)
	postUpdate := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/telegram/"+gw.ID, strings.NewReader(body))
		req.Header.Set(SecretHeader, inst.WebhookSecret)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 1. 正常发送第一条私聊消息建立联系人与会话，随后关闭会话并拉黑联系人
	rec1 := postUpdate(`{"update_id":101,"message":{"message_id":1,"from":{"id":888001,"first_name":"Spammer"},"chat":{"id":888001,"type":"private"},"text":"hello"}}`)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first message status=%d body=%s", rec1.Code, rec1.Body.String())
	}

	var convID, contactID string
	if err := env.DB.NewRaw("SELECT id, contact_id FROM conversations WHERE tenant_id = ? LIMIT 1", tenantID).Scan(ctx, &convID, &contactID); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewClose(env.DB, down).Handle(ctx, sys, convID); err != nil {
		t.Fatal(err)
	}
	if _, err := contact.NewBlock(env.DB, nil).Handle(ctx, identity.Subject{Type: identity.System, ID: "admin", Tenant: tenantID}, contactID, contact.BlockBody{Reason: "spam"}); err != nil {
		t.Fatal(err)
	}

	var countBefore int
	if err := env.DB.NewRaw("SELECT count(*) FROM timeline_items WHERE conversation_id = ?", convID).Scan(ctx, &countBefore); err != nil {
		t.Fatal(err)
	}

	// 2. 被拉黑后再次发送消息：Webhook 返回 200 ok，但会话保持关闭且时间线无新消息
	rec2 := postUpdate(`{"update_id":102,"message":{"message_id":2,"from":{"id":888001,"first_name":"Spammer"},"chat":{"id":888001,"type":"private"},"text":"buy now"}}`)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"ok":true`) {
		t.Fatalf("blocked message expected 200 ok, got status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	var countAfter int
	if err := env.DB.NewRaw("SELECT count(*) FROM timeline_items WHERE conversation_id = ?", convID).Scan(ctx, &countAfter); err != nil {
		t.Fatal(err)
	}
	if countAfter != countBefore {
		t.Fatalf("已拉黑联系人来信不应写入时间线：before=%d after=%d", countBefore, countAfter)
	}
	var status conversation.Status
	if err := env.DB.NewRaw("SELECT status FROM conversations WHERE id = ?", convID).Scan(ctx, &status); err != nil {
		t.Fatal(err)
	}
	if status != conversation.Closed {
		t.Fatalf("已拉黑联系人来信不应重开会话：%s", status)
	}
}
