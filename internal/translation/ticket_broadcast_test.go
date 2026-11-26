package translation

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
	"agents-desk/internal/tickettype"
)

// TestTicketBroadcastDedupAndNoteTranslation 验证 TK3：
//   - 客户可见工单发布公开进度后，3 个关联会话涉及 2 种客户语言（hi-Latn 与 en）仅调用 1 次模型批量翻译，
//     分别投递到各自会话、保留中文原文、不篡改原会话负责人，且重试幂等不重复发送；
//   - 工单内部备注（Note）支持跨语言成员自动/按需补译，且不污染客户语言。
func TestTicketBroadcastDedupAndNoteTranslation(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	model := &fakeModel{language: "zh-Hans"}
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)

	llmSettings := llm.NewGroup(env.DB, env.Crypt)
	if err := llm.NewSaveSettings(env.DB, llmSettings).Handle(ctx, apptest.Admin, llm.Settings{
		Gateway: llm.Gateway{BaseURL: srv.URL + "/v1", APIKey: "test-key"},
		Models:  llm.Models{Translation: "test-translate"},
	}); err != nil {
		t.Fatal(err)
	}
	translator := NewTranslator(env.DB, llm.NewClient(env.DB, llmSettings, srv.Client()), NewGroup(env.DB))
	locker := lock.New(pglock.New(env.DB))

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	subOwner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}

	// 创建 1 个测试渠道与 3 个印度客户会话（2 个 hi-Latn，1 个 en）
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	down := conversation.Downstream{}
	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	c1, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "c1", FromName: "Rahul", Text: "payment issue"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "c2", FromName: "Amit", Text: "payment issue"})
	if err != nil {
		t.Fatal(err)
	}
	c3, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "c3", FromName: "Priya", Text: "payment issue"})
	if err != nil {
		t.Fatal(err)
	}
	setLang := conversation.NewSetLanguage(env.DB, down)
	if _, err := setLang.Handle(ctx, subOwner, c1.ConversationID, "hi-Latn"); err != nil {
		t.Fatal(err)
	}
	if _, err := setLang.Handle(ctx, subOwner, c2.ConversationID, "hi-Latn"); err != nil {
		t.Fatal(err)
	}
	if _, err := setLang.Handle(ctx, subOwner, c3.ConversationID, "en"); err != nil {
		t.Fatal(err)
	}

	// 新建聚合工单关联这 3 位客户，并发布一条中文公开进度
	tt, err := tickettype.NewCreate(env.DB).Handle(ctx, sys, tickettype.CreateParams{Name: "充值问题", DefaultVisibility: tickettype.VisibilityAll})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := conversation.NewCreateTicket(env.DB, down).Handle(ctx, subOwner, conversation.CreateTicketParams{
		TypeID:               &tt.ID,
		Title:                "UPI 通道掉单",
		SourceConversationID: &c1.ConversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	linkContact := conversation.NewLinkTicketContact(env.DB, down)
	if _, err := linkContact.Handle(ctx, subOwner, tk.ID, conversation.LinkTicketContactBody{ConversationID: &c2.ConversationID}); err != nil {
		t.Fatal(err)
	}
	if _, err := linkContact.Handle(ctx, subOwner, tk.ID, conversation.LinkTicketContactBody{ConversationID: &c3.ConversationID}); err != nil {
		t.Fatal(err)
	}
	pub, err := conversation.NewPublishTicketUpdate(env.DB, down).Handle(ctx, subOwner, tk.ID, conversation.PublishTicketUpdateBody{
		Text: "UPI 通道已恢复，掉单资金正在原路补发。",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 执行广播任务：3 个会话、2 种目标语言（en、hi-Latn），应仅调用 1 次模型
	broadcaster := NewTicketBroadcast(env.DB, locker, translator, down)
	appended := conversation.Appended{TenantID: tn.ID, ConversationID: tk.ID, ItemID: pub.ID, Seq: pub.Seq}
	if err := broadcaster.Handle(ctx, appended); err != nil {
		t.Fatalf("首次广播失败：%v", err)
	}
	if got := len(model.requests); got != 1 {
		t.Fatalf("应按语言去重仅调用 1 次模型，实际调用 %d 次", got)
	}

	// 验证每个关联会话均收到对应语言的公开进度消息，保留原文且不篡改负责人
	tl := conversation.NewTimeline(env.DB)
	for _, tc := range []struct {
		convID   string
		wantLang string
	}{
		{c1.ConversationID, "hi-Latn"},
		{c2.ConversationID, "hi-Latn"},
		{c3.ConversationID, "en"},
	} {
		items, err := tl.Handle(ctx, subOwner, tc.convID, 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		var found *conversation.ItemDTO
		for i := range items {
			if items[i].Content.Prompt != nil && items[i].Content.Prompt.Kind == "ticket_update" {
				found = &items[i]
			}
		}
		if found == nil {
			t.Fatalf("会话 %s 未收到 ticket_update 消息", tc.convID)
		}
		if found.Content.Language != tc.wantLang || found.Content.Original != "UPI 通道已恢复，掉单资金正在原路补发。" {
			t.Fatalf("会话 %s 译文元信息异常：%+v", tc.convID, found.Content)
		}
		convDTO, err := conversation.NewGet(env.DB).Handle(ctx, subOwner, tc.convID)
		if err != nil {
			t.Fatal(err)
		}
		if convDTO.Assignee != nil {
			t.Fatalf("广播不应修改原会话负责人：%+v", convDTO.Assignee)
		}
	}

	// 幂等重跑广播：不应重复发送，也不应再调模型
	if err := broadcaster.Handle(ctx, appended); err != nil {
		t.Fatalf("重试广播失败：%v", err)
	}
	if got := len(model.requests); got != 1 {
		t.Fatalf("重试广播不应重复调用模型，实际 %d 次", got)
	}

	// 工单内部备注跨语言协作：中文成员写备注，英文成员（印度大代理）可按需补译成英文
	note, err := conversation.NewAddNote(env.DB, down).Handle(ctx, subOwner, tk.ID, "财务已核对完成，可以通知客户", nil)
	if err != nil {
		t.Fatal(err)
	}
	incoming := NewIncoming(env.DB, locker, translator, down)
	if err := incoming.Ensure(ctx, tn.ID, note.ID, []string{"en"}); err != nil {
		t.Fatalf("工单内部备注补译失败：%v", err)
	}
	// 统一翻译公理验证：工单公开进度消息与内部备注均支持跨语言成员补译成英文
	if err := incoming.Ensure(ctx, tn.ID, pub.ID, []string{"en"}); err != nil {
		t.Fatalf("工单公开进度补译失败：%v", err)
	}
	tkItems, err := tl.Handle(ctx, subOwner, tk.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var translatedNote, translatedPub *conversation.ItemDTO
	for i := range tkItems {
		if tkItems[i].ID == note.ID {
			translatedNote = &tkItems[i]
		}
		if tkItems[i].ID == pub.ID {
			translatedPub = &tkItems[i]
		}
	}
	if translatedNote == nil || translatedPub == nil {
		t.Fatalf("未找到工单内部备注或公开进度")
	}
	trMap, _ := translatedNote.Derived["translations"].(map[string]any)
	if enText, _ := trMap["en"].(string); !strings.Contains(enText, "My order") {
		t.Fatalf("内部备注应包含英文译文，实际 derived=%+v", translatedNote.Derived)
	}
	pubTrMap, _ := translatedPub.Derived["translations"].(map[string]any)
	if enText, _ := pubTrMap["en"].(string); !strings.Contains(enText, "My order") {
		t.Fatalf("公开进度应包含英文译文，实际 derived=%+v", translatedPub.Derived)
	}

	// 发送前人工校对的公开进度译文：该语言直接沿用人工译文，其余客户语言继续自动翻译
	beforeCalls := len(model.requests)
	pubVerified, err := conversation.NewPublishTicketUpdate(env.DB, down).Handle(ctx, subOwner, tk.ID, conversation.PublishTicketUpdateBody{
		Text:        "退款已原路退回，请查收。",
		Translation: &conversation.Translation{Language: "hi-Latn", Text: "Refund wapas bhej diya gaya hai, kripya check karein."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := broadcaster.Handle(ctx, conversation.Appended{TenantID: tn.ID, ConversationID: tk.ID, ItemID: pubVerified.ID, Seq: pubVerified.Seq}); err != nil {
		t.Fatalf("带校对译文的公开进度广播失败：%v", err)
	}
	if got := len(model.requests) - beforeCalls; got != 1 {
		t.Fatalf("应只对未校对的 en 语言调用 1 次模型，实际 %d 次", got)
	}
	hiItems, err := tl.Handle(ctx, subOwner, c1.ConversationID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var gotHi string
	for _, it := range hiItems {
		if it.Content.Prompt != nil && it.Content.Prompt.Params["item_id"] == pubVerified.ID {
			gotHi = it.Content.Text
		}
	}
	if gotHi != "Refund wapas bhej diya gaya hai, kripya check karein." {
		t.Fatalf("印地语客户应直接收到人工校对译文，实际：%q", gotHi)
	}
}
