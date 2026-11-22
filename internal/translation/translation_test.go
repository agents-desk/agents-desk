package translation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
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
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
		return
	}
	format := body["response_format"].(map[string]any)["json_schema"].(map[string]any)
	var answer string
	switch format["name"] {
	case "incoming_translation":
		answer = `{"language":"` + lang + `","translations":{"en":"My order has not arrived","zh-CN":"我的订单还没到"}}`
	case "reply_translation":
		answer = `{"translation":"Main aapka order check kar raha hoon","back_translation":"我正在查您的订单"}`
	}
	resp := map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": answer}}},
		"usage":   map[string]any{"prompt_tokens": 120, "completion_tokens": 30, "cost": 0.00042},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// last 返回最后一次请求中 system 提示的文字。
func (f *fakeModel) lastSystem() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	msgs := f.requests[len(f.requests)-1]["messages"].([]any)
	return msgs[0].(map[string]any)["content"].(string)
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
	down       conversation.Downstream
}

// setup 创建工作区 alpha、测试渠道，加一位语言是英文的成员，并把翻译的模型接口配置成替身。
func setup(t *testing.T, configured bool) fixture {
	t.Helper()
	ctx := context.Background()
	env := apptest.New(t)
	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	owner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}
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
	if _, err := member.NewAdd(env.DB, env.MemberLogins, env.Files).Handle(ctx, owner, member.AddParams{
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
	llmSettings := llm.NewGroup(env.DB)
	if configured {
		if err := llmSettings.Save(ctx, llm.Settings{Translation: llm.Endpoint{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "test-model"}}); err != nil {
			t.Fatal(err)
		}
	}
	tr := NewTranslator(env.DB, llm.NewClient(env.DB, llmSettings, srv.Client()), NewGroup(env.DB))
	return fixture{
		env: env, system: system, agent: owner, ch: ch, model: model, translator: tr,
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
	job := NewIncoming(f.env.DB, f.translator, f.down)

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
	if call.CallType != llm.Translation || call.Model != "test-model" || call.PromptTokens != 120 || call.CompletionTokens != 30 ||
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
	if err := NewIncoming(off.env.DB, off.translator, off.down).Job(ctx, p); err != nil || off.model.count() != 0 {
		t.Fatalf("没有配置：%v，调用了 %d 次", err, off.model.count())
	}
	if lang := off.item(t, p.ItemID).Derived["language"]; lang != nil {
		t.Fatalf("没有配置时的派生内容：%v", lang)
	}

	f := setup(t, true)
	f.model.status = http.StatusServiceUnavailable
	q := f.receive(t, "rahul", "hello")
	if err := NewIncoming(f.env.DB, f.translator, f.down).Job(ctx, q); !errors.Is(err, llm.ErrFailed) {
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
	preview := NewPreview(f.env.DB, f.translator)
	// 还没有识别出对方的语言：按原文发出
	if got, err := preview.Handle(ctx, f.agent, p.ConversationID, PreviewBody{Text: "我在查"}); err != nil || got.Needed {
		t.Fatalf("对方的语言未知：%+v %v", got, err)
	}
	if err := NewIncoming(f.env.DB, f.translator, f.down).Job(ctx, p); err != nil {
		t.Fatal(err)
	}
	// 会话列表的预览：对方的消息显示成员语言的译文
	if c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, p.ConversationID); err != nil || c.Preview != "我的订单还没到" {
		t.Fatalf("翻译后的预览：%+v %v", c, err)
	}
	got, err := preview.Handle(ctx, f.agent, p.ConversationID, PreviewBody{Text: "我在查您的 AgentsPay 提现"})
	if err != nil || !got.Needed || got.From != "zh-CN" || got.Language != "hi-Latn" || got.Translation == "" || got.BackTranslation == "" {
		t.Fatalf("预览：%+v %v", got, err)
	}
	system := f.model.lastSystem()
	for _, want := range []string{"from zh-CN into hi-Latn", toneHint[Friendly], `"AgentsPay": keep it exactly as written`, `"提现": in hi-Latn always translate it as "withdrawal"`} {
		if !strings.Contains(system, want) {
			t.Errorf("提示中没有 %q：\n%s", want, system)
		}
	}
	if strings.Contains(system, "优惠券") {
		t.Errorf("没有出现在文字中的术语不进提示：\n%s", system)
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

// TestSameLanguage 检查语言的比较：主语言和书写方式相同才算同一种语言，地区不算，中文按地区推断简体和繁体。
func TestSameLanguage(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"zh-CN", "zh-Hans", true}, {"zh-CN", "zh", true}, {"zh-TW", "zh-Hans", false}, {"zh-Hant", "zh-HK", true},
		{"en", "en-US", true}, {"hi", "hi-Latn", false}, {"hi-Latn", "hi-Latn", true}, {"en", "zh-CN", false}, {"pt_BR", "pt", true},
	} {
		if got := sameLanguage(c.a, c.b); got != c.same {
			t.Errorf("sameLanguage(%q, %q) = %v", c.a, c.b, got)
		}
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
