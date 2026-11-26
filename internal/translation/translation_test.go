package translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
	"agents-desk/internal/tenant"
)

// fakeModel 是 OpenAI 兼容的 /chat/completions 的替身：按 schema 的名字回答，记下收到的请求。
type fakeModel struct {
	mu       sync.Mutex
	requests []map[string]any
	status   int    // 不为 0 时按这个状态码失败
	language string // 识别出的语言
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	f.mu.Lock()
	f.requests = append(f.requests, body)
	status, lang := f.status, f.language
	f.mu.Unlock()
	if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
		return
	}
	format := body["response_format"].(map[string]any)["json_schema"].(map[string]any)
	var answer string
	switch format["name"] {
	case "incoming_translation", "fill_translation":
		// 按请求中的目标语言作答：中文和英文是固定的译文，其他语言是带语言标签的文字
		known := map[string]string{"en": "My order has not arrived", "zh-CN": "我的订单还没到"}
		props := map[string]any{}
		if tr, ok := format["schema"].(map[string]any)["properties"].(map[string]any)["translations"].(map[string]any); ok {
			props = tr["properties"].(map[string]any)
		}
		translations := map[string]string{}
		for target := range props {
			translations[target] = known[target]
			if translations[target] == "" {
				translations[target] = "[" + target + "] my order has not arrived"
			}
		}
		payload := map[string]any{"translations": translations}
		if format["name"] == "incoming_translation" {
			payload["language"] = lang
		}
		out, _ := json.Marshal(payload)
		answer = string(out)
	case "reply_translation":
		answer = `{"translation":"Main aapka order check kar raha hoon"}`
	case "back_translation":
		answer = `{"translation":"我正在查您的订单"}`
	}
	resp := map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": answer}}},
		"usage": map[string]any{"prompt_tokens": 120, "completion_tokens": 30, "cost": 0.00042,
			"completion_tokens_details": map[string]any{"reasoning_tokens": 4}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// last 返回最后一次请求中 system 提示的文字。
func (f *fakeModel) lastSystem() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.requests[len(f.requests)-1]["messages"].([]any)
	return msgs[0].(map[string]any)["content"].(string)
}

// last 返回最后一次 schema 名为 name 的请求中 system 提示和 user 消息的文字。
func (f *fakeModel) last(name string) (system, user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.requests) - 1; i >= 0; i-- {
		r := f.requests[i]
		if r["response_format"].(map[string]any)["json_schema"].(map[string]any)["name"] != name {
			continue
		}
		msgs := r["messages"].([]any)
		return msgs[0].(map[string]any)["content"].(string), msgs[1].(map[string]any)["content"].(string)
	}
	return "", ""
}

func (f *fakeModel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// fixture 是一个测试的工作区、测试渠道、成员和模型替身。
type fixture struct {
	env        *apptest.Env
	system     identity.Subject
	agent      identity.Subject // 主管理员，语言是简体中文
	ch         *channel.Channel
	model      *fakeModel
	translator *Translator
	locker     *lock.Locker
	down       conversation.Downstream
}

// setup 创建工作区 alpha、测试渠道，加一位语言是英文的成员，并把翻译的模型接口配置成替身。
func setup(t *testing.T, configured bool) fixture {
	t.Helper()
	ctx := context.Background()
	env := apptest.New(t)
	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	owner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	if _, err := env.DB.NewRaw("UPDATE members SET language = 'zh-CN' WHERE id = ?", tn.OwnerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 第二位成员的语言是英文：收到的消息翻译成中文和英文
	roles, err := env.Roles.Roles(ctx, tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	var roleID string
	for _, r := range roles {
		if r.Tenant == tn.ID {
			roleID = r.ID
		}
	}
	if _, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, owner, member.AddParams{
		Email: "en@alpha.com", Name: "en", Password: "password", RoleID: roleID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE members SET language = 'en' WHERE name = 'en'").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeModel{language: "hi-Latn"}
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)
	llmSettings := llm.NewGroup(env.DB, env.Crypt)
	if configured {
		if err := llmSettings.Save(ctx, llm.Settings{
			Gateway: llm.Gateway{BaseURL: srv.URL + "/v1", APIKey: "test-key"},
			Models:  llm.Models{Translation: "test-model"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	tr := NewTranslator(env.DB, llm.NewClient(env.DB, llmSettings, srv.Client()), NewGroup(env.DB))
	return fixture{
		env: env, system: system, agent: owner, ch: ch, model: model, translator: tr, locker: lock.New(pglock.New(env.DB)),
		down: conversation.Downstream{Topics: []queue.Topic[conversation.Appended]{conversation.DeliverTopic, Topic}},
	}
}

// receive 写入客户 from 发来的消息，返回它的任务内容。
func (f fixture) receive(t *testing.T, from, text string) conversation.Appended {
	t.Helper()
	res, err := conversation.NewReceive(f.env.DB, channel.DefaultRegistry(), f.down).Handle(context.Background(), f.system, conversation.ReceiveParams{
		ChannelID: f.ch.ID, From: from, Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return conversation.Appended{TenantID: f.system.Tenant, ConversationID: res.ConversationID, ItemID: res.Item.ID, Seq: res.Item.Seq}
}

// item 读取时间线中的一项。
func (f fixture) item(t *testing.T, id string) conversation.Item {
	t.Helper()
	var it conversation.Item
	if err := f.env.DB.NewSelect().Model(&it).Where("id = ?", id).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return it
}

// TestIncoming 检查收到的消息：识别出用拉丁字母写的印地语，翻译成成员用到的中文和英文，存为派生内容，会话和联系人的语言随之更新，
// 调用记在 llm_calls 中；重复的任务不再调用模型；英文消息只翻译成中文；成员的回复不翻译。
func TestIncoming(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)

	p := f.receive(t, "rahul", "mera order abhi tak nahi aaya")
	if err := job.Job(ctx, p); err != nil {
		t.Fatal(err)
	}
	it := f.item(t, p.ItemID)
	tr, _ := it.Derived["translations"].(map[string]any)
	if it.Derived["language"] != "hi-Latn" || tr["zh-CN"] != "我的订单还没到" || tr["en"] != "My order has not arrived" {
		t.Fatalf("派生内容：%+v", it.Derived)
	}
	c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, p.ConversationID)
	if err != nil || c.Language != "hi-Latn" || c.Contact.Language != "hi-Latn" {
		t.Fatalf("会话和联系人的语言：%+v %v", c, err)
	}
	if !strings.Contains(f.model.lastSystem(), "en, zh-CN") {
		t.Errorf("目标语言：%s", f.model.lastSystem())
	}
	var call llm.Call
	if err := f.env.DB.NewSelect().Model(&call).Where("target_id = ?", p.ItemID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if call.CallType != llm.Translation || call.Model != "test-model" || call.PromptTokens != 120 || call.CompletionTokens != 30 || call.ReasoningTokens != 4 ||
		call.Cost == nil || *call.Cost != 0.00042 || call.Error != "" || call.TenantID == nil || *call.TenantID != f.system.Tenant {
		t.Fatalf("调用记录：%+v", call)
	}

	// 重复的任务不再调用模型
	before := f.model.count()
	if err := job.Job(ctx, p); err != nil || f.model.count() != before {
		t.Fatalf("重复的任务：%v，调用了 %d 次", err, f.model.count()-before)
	}

	// 英文消息：和英文成员同语言，只保存中文译文
	f.model.language = "en"
	en := f.receive(t, "john", "my order has not arrived")
	if err := job.Job(ctx, en); err != nil {
		t.Fatal(err)
	}
	tr, _ = f.item(t, en.ItemID).Derived["translations"].(map[string]any)
	if _, ok := tr["en"]; ok || tr["zh-CN"] == nil {
		t.Fatalf("英文消息的译文：%+v", tr)
	}

	// 成员的回复不翻译
	reply, err := conversation.NewReply(f.env.DB, f.down).Handle(ctx, f.agent, p.ConversationID, conversation.ReplyBody{Text: "您好"})
	if err != nil {
		t.Fatal(err)
	}
	before = f.model.count()
	if err := job.Job(ctx, conversation.Appended{TenantID: f.system.Tenant, ConversationID: p.ConversationID, ItemID: reply.ID}); err != nil || f.model.count() != before {
		t.Fatalf("成员的回复：%v", err)
	}
}

// TestIncomingFailures 检查没有配置时什么也不做、不报错；模型接口失败时任务返回错误（稍后重试），并记下失败的调用。
func TestIncomingFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	off := setup(t, false)
	if got, err := NewSettingsActions(off.env.DB, NewGroup(off.env.DB), off.translator).Get(ctx, off.agent); err != nil || got.Enabled {
		t.Fatalf("没有配置时的设置：%+v %v", got, err)
	}
	p := off.receive(t, "rahul", "hello")
	if err := NewIncoming(off.env.DB, off.locker, off.translator, off.down).Job(ctx, p); err != nil || off.model.count() != 0 {
		t.Fatalf("没有配置：%v，调用了 %d 次", err, off.model.count())
	}
	if lang := off.item(t, p.ItemID).Derived["language"]; lang != nil {
		t.Fatalf("没有配置时的派生内容：%v", lang)
	}

	f := setup(t, true)
	f.model.status = http.StatusServiceUnavailable
	q := f.receive(t, "rahul", "hello")
	if err := NewIncoming(f.env.DB, f.locker, f.translator, f.down).Job(ctx, q); !errors.Is(err, llm.ErrFailed) {
		t.Fatalf("模型失败：%v", err)
	}
	var call llm.Call
	if err := f.env.DB.NewSelect().Model(&call).Where("target_id = ?", q.ItemID).Scan(ctx); err != nil || !strings.Contains(call.Error, "503") {
		t.Fatalf("失败的调用记录：%+v %v", call, err)
	}
}

// TestPreviewAndReply 检查回复前的预览：翻译成对方的语言并译回成员的语言，提示中有语气和出现在文字中的术语；对方的语言与成员相同、
// 或者未知时不调用模型。带译文的回复发给对方的是译文，时间线中保存原文。
func TestPreviewAndReply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	settings := NewSettingsActions(f.env.DB, NewGroup(f.env.DB), f.translator)
	if got, err := settings.SaveTone(ctx, f.agent, Friendly); err != nil || got.Tone != Friendly || !got.Enabled {
		t.Fatalf("保存语气：%+v %v", got, err)
	}
	if _, err := settings.SaveGlossary(ctx, f.agent, []Term{
		{Term: " AgentsPay ", Keep: true},
		{Term: "提现", Translations: map[string]string{"hi-Latn": "withdrawal"}},
		{Term: "优惠券", Translations: map[string]string{"en": "coupon"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.SaveGlossary(ctx, f.agent, []Term{{Term: "a"}, {Term: "A"}}); !errors.Is(err, ErrDuplicateTerm) {
		t.Fatalf("重复的术语：%v", err)
	}

	p := f.receive(t, "rahul", "mera order abhi tak nahi aaya")
	incoming := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	preview := NewPreview(f.env.DB, f.translator, incoming)
	// 后台还没识别出对方的语言：预览先识别并顺带翻译主要语言，再翻译回复；后台任务随后执行时不再重复调用模型
	got, err := preview.Handle(ctx, f.agent, p.ConversationID, PreviewBody{Text: "我在查"})
	if err != nil || !got.Needed || got.Language != "hi-Latn" {
		t.Fatalf("对方的语言未知时的预览：%+v %v", got, err)
	}
	if it := f.item(t, p.ItemID); it.Derived["language"] != "hi-Latn" || it.Derived["translations"].(map[string]any)["zh-CN"] != "我的订单还没到" {
		t.Fatalf("预览前识别并顺带翻译的消息：%+v", it.Derived)
	}
	afterPreview := f.model.count()
	if err := incoming.Job(ctx, p); err != nil || f.model.count() != afterPreview {
		t.Fatalf("预览已顺带翻译后后台任务不应当再调用模型：%v，调用了 %d 次", err, f.model.count()-afterPreview)
	}
	// 会话列表的预览：对方的消息显示成员语言的译文
	if c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, p.ConversationID); err != nil || c.Preview != "我的订单还没到" {
		t.Fatalf("翻译后的预览：%+v %v", c, err)
	}
	got, err = preview.Handle(ctx, f.agent, p.ConversationID, PreviewBody{Text: "我在查您的 AgentsPay 提现"})
	if err != nil || !got.Needed || got.From != "zh-CN" || got.Language != "hi-Latn" || got.Translation == "" || got.BackTranslation == "" {
		t.Fatalf("预览：%+v %v", got, err)
	}
	system, _ := f.model.last("reply_translation")
	for _, want := range []string{"from zh-CN into hi-Latn", toneHint[Friendly], `"AgentsPay": keep it exactly as written`, `"提现": in hi-Latn always translate it as "withdrawal"`,
		"<history>\nCustomer: mera order abhi tak nahi aaya\n</history>"} {
		if !strings.Contains(system, want) {
			t.Errorf("提示中没有 %q：\n%s", want, system)
		}
	}
	if strings.Contains(system, "优惠券") {
		t.Errorf("没有出现在文字中的术语不进提示：\n%s", system)
	}
	// 盲回译：模型只看到译文，看不到成员的原文和会话
	backSystem, backUser := f.model.last("back_translation")
	if backUser != got.Translation || got.BackTranslation != "我正在查您的订单" || !strings.Contains(backSystem, "from hi-Latn into zh-CN") ||
		strings.Contains(backSystem+backUser, "提现") || strings.Contains(backSystem, "<history>") {
		t.Fatalf("回译的请求：%q %q", backSystem, backUser)
	}
	// 手动选择和成员相同的语言：不调用模型
	before := f.model.count()
	if same, err := preview.Handle(ctx, f.agent, p.ConversationID, PreviewBody{Text: "好的", Language: "zh-Hans"}); err != nil || same.Needed || f.model.count() != before {
		t.Fatalf("同语言：%+v %v", same, err)
	}

	// 带译文的回复：客户看到译文，成员看到原文
	reply, err := conversation.NewReply(f.env.DB, f.down).Handle(ctx, f.agent, p.ConversationID, conversation.ReplyBody{
		Text: "我在查您的订单", Translation: &conversation.Translation{Language: got.Language, Text: got.Translation},
	})
	if err != nil || reply.Content.Text != got.Translation || reply.Content.Original != "我在查您的订单" || reply.Content.Language != "hi-Latn" {
		t.Fatalf("带译文的回复：%+v %v", reply, err)
	}
	// 会话列表的预览：成员翻译后发出的回复显示成员写的原文
	if c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, p.ConversationID); err != nil || c.Preview != "我在查您的订单" {
		t.Fatalf("回复后的预览：%+v %v", c, err)
	}
	var contactID string
	if err := f.env.DB.NewRaw("SELECT contact_id FROM conversations WHERE id = ?", p.ConversationID).Scan(ctx, &contactID); err != nil {
		t.Fatal(err)
	}
	theirs, err := conversation.NewTimelineForContact(f.env.DB).Handle(ctx, identity.Subject{Type: contact.Type, ID: contactID, Tenant: f.system.Tenant}, f.ch.ID, p.ConversationID, 0, 100)
	if err != nil || theirs[len(theirs)-1].Text != got.Translation {
		t.Fatalf("客户看到的回复：%+v %v", theirs, err)
	}
}

// TestWithScript 检查按原文用的文字修正书写方式：拉丁字母写的印地语补上 -Latn，已经写了的、天城文的、本来就用拉丁字母的保持原样。
func TestWithScript(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ tag, text, want string }{
		{"hi", "bhai mera refund kab aayega", "hi-Latn"},
		{"hi", "मेरा पैसा अभी तक वापस नहीं आया", "hi"},
		{"hi-Latn", "mera order", "hi-Latn"},
		{"en", "where is my order", "en"},
		{"es", "¿cuándo llega mi pedido?", "es"},
		{"ar", "wen al talab", "ar-Latn"},
		{"zh-Hans", "我的订单 #A1029384", "zh-Hans"},
		{"zh", "我的订单 order", "zh"},
	} {
		if got := withScript(c.tag, c.text); got != c.want {
			t.Errorf("withScript(%q, %q) = %q，应当是 %q", c.tag, c.text, got, c.want)
		}
	}
}

// TestFill 检查补译：收到时没有翻译的消息，在时间线中对成员标为 untranslated，页面报告看到它时放进补译任务，补上成员用到的全部语言；
// 成员改成新的语言后，旧消息只补这种语言；已经齐了、不是对方的消息、不认识的 ID 不放进任务。
func TestFill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	fill := NewFillMissing(f.env.DB, f.translator)
	timeline := conversation.NewTimeline(f.env.DB)
	untranslated := func(convID, itemID string) bool {
		t.Helper()
		items, err := timeline.Handle(ctx, f.agent, convID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.ID == itemID {
				return it.Untranslated
			}
		}
		t.Fatalf("时间线中没有 %s", itemID)
		return false
	}

	// 收到时没有翻译（例如那时还没配置翻译）
	p := f.receive(t, "rahul", "mera order abhi tak nahi aaya")
	if !untranslated(p.ConversationID, p.ItemID) {
		t.Fatal("没有译文的消息应当标为 untranslated")
	}
	reply, err := conversation.NewReply(f.env.DB, f.down).Handle(ctx, f.agent, p.ConversationID, conversation.ReplyBody{Text: "您好"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := fill.Handle(ctx, f.agent, []string{p.ItemID, reply.ID, "not-a-uuid"})
	if err != nil || n != 1 {
		t.Fatalf("补译：%d %v", n, err)
	}
	if err := job.Fill(ctx, FillJob{TenantID: f.system.Tenant, ItemID: p.ItemID, Language: "zh-CN"}); err != nil {
		t.Fatal(err)
	}
	tr, _ := f.item(t, p.ItemID).Derived["translations"].(map[string]any)
	if tr["zh-CN"] != "我的订单还没到" || tr["en"] != nil || f.item(t, p.ItemID).Derived["language"] != "hi-Latn" {
		t.Fatalf("补译只补看的人的语言，并识别语言：%+v", f.item(t, p.ItemID).Derived)
	}
	if untranslated(p.ConversationID, p.ItemID) {
		t.Fatal("补译之后不应当再标为 untranslated")
	}
	if n, err := fill.Handle(ctx, f.agent, []string{p.ItemID}); err != nil || n != 0 {
		t.Fatalf("已经有译文时：%d %v", n, err)
	}

	// 成员改成日语：旧消息已知源语言，只补译日语不重新识别（即便模型会把短语识别成 und 也不影响），同一条消息重复执行只调用一次模型
	if _, err := f.env.DB.NewRaw("UPDATE members SET language = 'ja' WHERE id = ?", f.agent.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !untranslated(p.ConversationID, p.ItemID) {
		t.Fatal("改成日语后旧消息应当标为 untranslated")
	}
	if n, err := fill.Handle(ctx, f.agent, []string{p.ItemID}); err != nil || n != 1 {
		t.Fatalf("补日语：%d %v", n, err)
	}
	f.model.language = "und"
	before := f.model.count()
	for range 2 {
		if err := job.Fill(ctx, FillJob{TenantID: f.system.Tenant, ItemID: p.ItemID, Language: "ja"}); err != nil {
			t.Fatal(err)
		}
	}
	if f.model.count() != before+1 || !strings.Contains(f.model.lastSystem(), "written in hi-Latn into each of these languages: ja.") {
		t.Fatalf("补日语调用了 %d 次：%s", f.model.count()-before, f.model.lastSystem())
	}
	if tr, _ := f.item(t, p.ItemID).Derived["translations"].(map[string]any); tr["ja"] == nil || tr["zh-CN"] == nil {
		t.Fatalf("补日语后的译文：%+v", tr)
	}

	// 翻译不可用时什么也不做
	off := setup(t, false)
	q := off.receive(t, "rahul", "hello")
	if n, err := NewFillMissing(off.env.DB, off.translator).Handle(ctx, off.agent, []string{q.ItemID}); err != nil || n != 0 {
		t.Fatalf("没有配置时补译：%d %v", n, err)
	}
}

// TestManualLanguage 检查手动设置对方的语言：按规范写法保存在会话和联系人上并记为手动，时间线中有系统事件，消息上的识别结果不改；
// 之后收到的消息照常识别和翻译，但不再改会话和联系人的语言；恢复自动识别后马上按最近的消息重新判断。
func TestManualLanguage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	set := conversation.NewSetLanguage(f.env.DB, f.down)

	f.model.language = "en" // 模型把拉丁字母写的印地语错认成英文
	p := f.receive(t, "rahul", "mera order abhi tak nahi aaya")
	if err := job.Job(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := set.Handle(ctx, f.agent, p.ConversationID, "not a language!"); !errors.Is(err, conversation.ErrInvalidLanguage) {
		t.Fatalf("不合法的语言：%v", err)
	}
	c, err := set.Handle(ctx, f.agent, p.ConversationID, "HI-latn")
	if err != nil || c.Language != "hi-Latn" || c.Contact.Language != "hi-Latn" || !c.Contact.LanguageManual {
		t.Fatalf("手动设置：%+v %v", c, err)
	}
	if f.item(t, p.ItemID).Derived["language"] != "en" {
		t.Fatalf("消息上的识别结果不改：%+v", f.item(t, p.ItemID).Derived)
	}

	// 之后收到的消息：照常翻译，语言以手动设置的为准
	f.model.language = "en"
	q := f.receive(t, "rahul", "jaldi batao")
	if err := job.Job(ctx, q); err != nil {
		t.Fatal(err)
	}
	it := f.item(t, q.ItemID)
	if it.Derived["language"] != "en" || it.Derived["translations"] == nil {
		t.Fatalf("手动设置后收到的消息照常识别和翻译：%+v", it.Derived)
	}
	if c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, p.ConversationID); err != nil || c.Language != "hi-Latn" || c.Contact.Language != "hi-Latn" {
		t.Fatalf("手动设置后的语言：%+v %v", c, err)
	}

	// 恢复自动识别：马上按最近的消息重新判断（两条都识别成 en）
	if c, err := set.Handle(ctx, f.agent, p.ConversationID, ""); err != nil || c.Contact.LanguageManual || c.Language != "en" || c.Contact.Language != "en" {
		t.Fatalf("恢复自动识别：%+v %v", c, err)
	}

	var events []string
	if err := f.env.DB.NewRaw("SELECT content->>'event' FROM timeline_items WHERE conversation_id = ? AND kind = 'event' ORDER BY seq", p.ConversationID).Scan(ctx, &events); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(events, "conversation.language_set") || !slices.Contains(events, "conversation.language_auto") {
		t.Fatalf("系统事件：%v", events)
	}

	// 客户只有 und 消息时，手动设置后恢复自动识别应当清空残留的手动语言
	f.model.language = "und"
	onlyUnd := f.receive(t, "amit", "ok")
	if err := job.Job(ctx, onlyUnd); err != nil {
		t.Fatal(err)
	}
	if c, err := set.Handle(ctx, f.agent, onlyUnd.ConversationID, "ja"); err != nil || c.Language != "ja" || !c.Contact.LanguageManual {
		t.Fatalf("只有 und 时手动设置：%+v %v", c, err)
	}
	if c, err := set.Handle(ctx, f.agent, onlyUnd.ConversationID, ""); err != nil || c.Contact.LanguageManual || c.Language != "" || c.Contact.Language != "" {
		t.Fatalf("只有 und 时恢复自动识别应当清空语言：%+v %v", c, err)
	}

	// 同一个联系人的多个轮次会话（前一条关闭且重开窗口为 0 后新建第二条）：在一个会话手动设置语言，另一个会话立即同步看到新语言
	multiCh, err := channel.NewCreate(f.env.DB).Handle(ctx, f.system, channel.CreateParams{Kind: channel.Widget, Name: "插件"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.DB.NewRaw("UPDATE tenants SET reopen_window_hours = 0 WHERE id = ?", f.system.Tenant).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	reg := channel.Registry{channel.Widget: stubWidgetConnector{}}
	recv := conversation.NewReceive(f.env.DB, reg, f.down)
	r1, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{ChannelID: multiCh.ID, From: "same-user", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewClose(f.env.DB, f.down).Handle(ctx, f.agent, r1.ConversationID); err != nil {
		t.Fatal(err)
	}
	r2, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{ChannelID: multiCh.ID, From: "same-user", Text: "second"})
	if err != nil || r2.ConversationID == r1.ConversationID {
		t.Fatalf("应建两个会话：%+v %+v %v", r1, r2, err)
	}
	if _, err := set.Handle(ctx, f.agent, r1.ConversationID, "fr"); err != nil {
		t.Fatal(err)
	}
	if c2, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, r2.ConversationID); err != nil || c2.Language != "fr" || c2.Contact.Language != "fr" {
		t.Fatalf("同联系人的另一个会话应当同步语言：%+v %v", c2, err)
	}
}

// stubWidgetConnector 是用于测试的网页插件连接器。
type stubWidgetConnector struct{}

func (stubWidgetConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{}
}

func (stubWidgetConnector) Send(_ context.Context, m channel.Outgoing) (string, bool, error) {
	return "widget-" + m.ItemID, true, nil
}

// TestCustomerLanguage 检查客户的语言：判断不出语言的消息（ok）记为 und、不翻译、不改客户的语言；一段英文不改，连续两段英文才改。
func TestCustomerLanguage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	say := func(language, text string) conversation.Appended {
		t.Helper()
		f.model.language = language
		p := f.receive(t, "rahul", text)
		if err := job.Job(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	languageOf := func(p conversation.Appended) string {
		t.Helper()
		c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, p.ConversationID)
		if err != nil || c.Language != c.Contact.Language {
			t.Fatalf("会话和联系人的语言：%+v %v", c, err)
		}
		return c.Language
	}

	say("hi-Latn", "mera order nahi aaya")
	p := say("hi-Latn", "kab tak aayega")
	// 识别和翻译带上之前的消息作参考
	if system, user := f.model.last("incoming_translation"); user != "kab tak aayega" || !strings.Contains(system, "<history>\nCustomer: mera order nahi aaya\n</history>") {
		t.Fatalf("识别时的会话上下文：%q %q", system, user)
	}
	ok := say("und", "ok")
	if languageOf(p) != "hi-Latn" {
		t.Fatalf("ok 之后：%s", languageOf(p))
	}
	if it := f.item(t, ok.ItemID); it.Derived["language"] != "und" || len(it.Derived["translations"].(map[string]any)) != 0 {
		t.Fatalf("ok 的派生内容：%+v", it.Derived)
	}
	if !strings.Contains(f.model.lastSystem(), "This customer has been writing in hi-Latn") {
		t.Errorf("识别时的参考语言：%s", f.model.lastSystem())
	}
	say("en", "Payment failed: insufficient balance")
	if languageOf(p) != "hi-Latn" {
		t.Fatalf("一段英文之后：%s", languageOf(p))
	}
	say("en", "Can you reply in English please")
	if languageOf(p) != "en" {
		t.Fatalf("连续两段英文之后：%s", languageOf(p))
	}
}

// TestPrimaryLanguages 检查收到时只预译工作区的主要语言（用的人最多的语言）：2 中 1 英时只预译中文，英文成员看到时补译，
// 且纯补译只推送给英文成员（带 item_seq），中文成员收不到。
func TestPrimaryLanguages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	f.down.Hub = hub
	f.down.Roles = f.env.Roles

	roles, err := f.env.Roles.Roles(ctx, f.system.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	var roleID string
	for _, r := range roles {
		if r.Tenant == f.system.Tenant {
			roleID = r.ID
		}
	}
	addZH := func(name string) {
		t.Helper()
		if _, err := member.NewAdd(f.env.DB, f.env.Roles, f.env.MemberLogins, f.env.Files).Handle(ctx, f.agent, member.AddParams{
			Email: name + "@alpha.com", Name: name, Password: "password", RoleID: roleID,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.env.DB.NewRaw("UPDATE members SET language = 'zh-CN' WHERE name = ?", name).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// 2 中 1 英：收到时只预译中文
	addZH("zh2")
	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	p := f.receive(t, "rahul", "mera order abhi tak nahi aaya")
	if err := job.Job(ctx, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.model.lastSystem(), "these languages: zh-CN.") {
		t.Fatalf("收到时的目标语言：%s", f.model.lastSystem())
	}
	tr, _ := f.item(t, p.ItemID).Derived["translations"].(map[string]any)
	if tr["zh-CN"] == nil || tr["en"] != nil {
		t.Fatalf("收到时的译文：%+v", tr)
	}

	// 英文成员：会话列表中这一行标出还缺译文，显示原文
	var enID string
	if err := f.env.DB.NewRaw("SELECT id FROM members WHERE name = 'en'").Scan(ctx, &enID); err != nil {
		t.Fatal(err)
	}
	en := identity.Subject{Type: member.Type, ID: enID, Tenant: f.system.Tenant, Permissions: f.agent.Permissions}
	c, err := conversation.NewGet(f.env.DB).Handle(ctx, en, p.ConversationID)
	if err != nil || !c.PreviewUntranslated || c.PreviewItemID != p.ItemID || c.Preview != "mera order abhi tak nahi aaya" {
		t.Fatalf("英文成员看到的预览：%+v %v", c, err)
	}
	if c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, p.ConversationID); err != nil || c.PreviewUntranslated || c.Preview != "我的订单还没到" {
		t.Fatalf("中文成员看到的预览：%+v %v", c, err)
	}
	if n, err := NewFillMissing(f.env.DB, f.translator).Handle(ctx, en, []string{c.PreviewItemID, p.ItemID}); err != nil || n != 1 {
		t.Fatalf("英文成员补译：%d %v", n, err)
	}

	// 纯补译英文：只推送给英文成员且带 item_seq，中文成员不收到推送
	enSub := hub.Subscribe(push.Channel(f.system.Tenant, enID))
	defer enSub.Close()
	zhSub := hub.Subscribe(push.Channel(f.system.Tenant, f.agent.ID))
	defer zhSub.Close()
	if err := job.Fill(ctx, FillJob{TenantID: f.system.Tenant, ItemID: p.ItemID, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-enSub.C():
		var ev push.Event
		if err := json.Unmarshal(msg.Data, &ev); err != nil || ev.ID != p.ConversationID || ev.ItemSeq != p.Seq {
			t.Fatalf("英文成员收到的补译推送：%+v %v", ev, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("英文成员没有收到补译推送")
	}
	select {
	case msg := <-zhSub.C():
		t.Fatalf("纯补译英文不应当推送给中文成员：%s", string(msg.Data))
	case <-time.After(100 * time.Millisecond):
	}
}

// TestHistory 检查翻译时带的会话上下文：只取这条消息之前的、最近的 historyMessages 条，按时间先后；客户和客服分别标明，
// 内部备注不带；过长的消息截断，总长不超过 historyChars。
func TestHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	var last conversation.Appended
	for i := range 8 {
		last = f.receive(t, "rahul", fmt.Sprintf("message %d", i))
	}
	if _, err := conversation.NewReply(f.env.DB, f.down).Handle(ctx, f.agent, last.ConversationID, conversation.ReplyBody{Text: "我在查"}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewAddNote(f.env.DB, f.down).Handle(ctx, f.agent, last.ConversationID, "内部备注", nil); err != nil {
		t.Fatal(err)
	}

	got, err := f.translator.history(ctx, f.system.Tenant, last.ConversationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "<history>\nCustomer: message 3\nCustomer: message 4\nCustomer: message 5\nCustomer: message 6\nCustomer: message 7\nSupport: 我在查\n</history>"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("最近的消息：%q", got)
	}
	// 某条消息之前的：不含它自己和之后的
	if got, err := f.translator.history(ctx, f.system.Tenant, last.ConversationID, last.Seq); err != nil || strings.Contains(got, "message 7") || !strings.Contains(got, "message 6") {
		t.Fatalf("之前的消息：%q %v", got, err)
	}

	// 过长的消息截断，总长有上限
	long := f.receive(t, "rahul", strings.Repeat("a", 1000))
	for range 5 {
		f.receive(t, "rahul", strings.Repeat("b", 1000))
	}
	got, err = f.translator.history(ctx, f.system.Tenant, long.ConversationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(got[strings.Index(got, "<history>\n")+len("<history>\n"):], "\n</history>"), "\n")
	total := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > len("Customer: ")+historyEach+1 {
			t.Fatalf("没有截断：%d", n)
		}
		total += len([]rune(l))
	}
	if total > historyChars || len(lines) == 0 || !strings.HasSuffix(lines[0], "…") {
		t.Fatalf("总长 %d，%d 条：%q", total, len(lines), lines)
	}
}
