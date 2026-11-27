package widget

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"

	"agents-desk/internal/channel"
	"agents-desk/internal/push"
)

// TestConnectorSendUsesConversationKind 验证聊天插件把会话变化推送给客户时，事件类型跟随会话的种类：
// 服务型推 conversation，商务沟通推 chat，工单推 ticket，客户页面据此决定刷新哪一块。
func TestConnectorSendUsesConversationKind(t *testing.T) {
	t.Parallel()
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	conn := NewConnector(hub)
	ch := &channel.Channel{ID: "01900000-0000-7000-8000-000000000001"}
	sub := hub.Subscribe(Channel(ch.ID, "contact-1"))
	defer sub.Close()

	for _, tc := range []struct{ kind, want string }{
		{"service", push.Conversation},
		{"chat", push.Chat},
		{"ticket", push.Ticket},
		{"", push.Conversation},
	} {
		if _, _, err := conn.Send(context.Background(), channel.Outgoing{
			Channel: ch, ConversationID: "conv-1", ConversationKind: tc.kind,
			ItemID: "item-1", ContactID: "contact-1", Seq: 7,
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case raw := <-sub.C():
			var ev push.Event
			if err := json.Unmarshal(raw.Data, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Type != tc.want || ev.ID != "conv-1" || ev.Seq != 7 {
				t.Fatalf("kind=%q 的推送事件不对：%+v", tc.kind, ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("kind=%q 没有收到推送", tc.kind)
		}
	}
}
