package translation

import (
	"context"
	"slices"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tickettype"
	"agents-desk/internal/widget"
)

// TestTicketMetaAndWidgetTranslation 检查工单标题/描述自动识别与翻译、修改后重置旧译文，以及客户在 Widget 中看到客户语言的工单标题、描述和公开进度。
func TestTicketMetaAndWidgetTranslation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	f.model.language = "hi-Latn"

	// 创建一位通过浏览器冷启动上报 pt-BR 的客户及来源会话，验证跨国扩展不硬编码语种
	ch, err := channel.NewCreate(f.env.DB).Handle(ctx, f.system, channel.CreateParams{Kind: channel.Widget, Name: "官网插件"})
	if err != nil {
		t.Fatal(err)
	}
	brContact, _, err := contact.ResolveIdentity(ctx, f.env.DB, f.system.Tenant, ch.ID, "visitor:br1", contact.Profile{
		Name: "Lucas", Language: "pt-BR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if brContact.Language != "pt-BR" || brContact.LanguageManual {
		t.Fatalf("浏览器冷启动语言应作为非锁定先验写入联系人：%+v", brContact)
	}
	reg := channel.DefaultRegistry()
	reg[channel.Widget] = widget.NewConnector(nil)
	rec := conversation.NewReceive(f.env.DB, reg, f.down)
	in, err := rec.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "visitor:br1", Text: "Meu pagamento falhou",
	})
	if err != nil {
		t.Fatal(err)
	}
	convDTO, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, in.ConversationID)
	if err != nil {
		t.Fatal(err)
	}

	// 印度代理（ui_language=en, language=hi-Latn）创建客户可见工单
	roles, _ := f.env.Roles.Roles(ctx, f.system.Tenant)
	var roleID string
	for _, r := range roles {
		if r.Tenant == f.system.Tenant {
			roleID = r.ID
		}
	}
	inAgent, err := member.NewAdd(f.env.DB, f.env.Roles, f.env.MemberLogins, f.env.Files).Handle(ctx, f.agent, member.AddParams{
		Email: "agent-in@alpha.com", Name: "Rahul", Password: "password", Language: "hi-Latn", RoleID: roleID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if inAgent.Language != "hi-Latn" {
		t.Fatalf("建号指定沟通语言应保留 hi-Latn：%q", inAgent.Language)
	}
	rahulSub := identity.Subject{Type: member.Type, ID: inAgent.ID, Tenant: f.system.Tenant, Permissions: auth.WithBase(auth.Owner.Permissions)}

	tk, err := conversation.NewCreateTicket(f.env.DB, f.down).Handle(ctx, rahulSub, conversation.CreateTicketParams{
		Title:                "Payment kat gaya par order pending hai",
		Description:          "Customer ka paisa deduct ho gaya",
		Visibility:           tickettype.VisibilityAll,
		SourceConversationID: &in.ConversationID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 运行后台翻译任务：自动识别源语言并翻译成中国技术（zh-CN）、工作区主要语言和关联客户语言（pt-BR）
	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	if err := job.EnsureTicketMetaForAudience(ctx, f.system.Tenant, tk.ID); err != nil {
		t.Fatal(err)
	}

	// 中国技术（zh-CN）查看工单：看到中文标题和描述译文
	gotForCN, err := conversation.NewGetTicket(f.env.DB).Handle(ctx, f.agent, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotForCN.Language != "hi-Latn" || gotForCN.TitleTranslated != "[zh-CN] ticket title" || gotForCN.DescriptionTranslated != "[zh-CN] ticket desc" || gotForCN.MetaUntranslated {
		t.Fatalf("中国技术查看工单译文异常：%+v", gotForCN)
	}

	// 中国技术发布公开进度并执行广播：不仅投递给客户会话，还回写到工单时间线供 Widget 详情直接展示客户母语
	pub, err := conversation.NewPublishTicketUpdate(f.env.DB, f.down).Handle(ctx, f.agent, tk.ID, conversation.PublishTicketUpdateBody{
		Text: "财务正在核查这笔扣款",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.model.language = "zh-CN"
	bc := NewTicketBroadcast(f.env.DB, f.locker, f.translator, f.down)
	if err := bc.Handle(ctx, conversation.Appended{TenantID: f.system.Tenant, ConversationID: tk.ID, ItemID: pub.ID}); err != nil {
		t.Fatal(err)
	}

	// 巴西客户（pt-BR）在 Widget 查看工单详情：标题、描述与公开进度均为 pt-BR 译文
	contactSub := identity.Subject{Type: contact.Type, ID: convDTO.Contact.ID, Tenant: f.system.Tenant}
	widgetDetail, err := conversation.NewGetTicketForContact(f.env.DB, f.down.Media).Handle(ctx, contactSub, ch.ID, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if widgetDetail.Title != "[pt-BR] ticket title" || widgetDetail.Description != "[pt-BR] ticket desc" {
		t.Fatalf("Widget 客户工单标题/描述未按客户语言 pt-BR 翻译：%+v", widgetDetail)
	}
	if len(widgetDetail.Updates) != 1 || widgetDetail.Updates[0].Text != "[pt-BR] my order has not arrived" {
		t.Fatalf("Widget 客户工单公开进度未展示 pt-BR 译文：%+v", widgetDetail.Updates)
	}

	// 修改工单标题后旧译文必须清空并标为待译，避免展示过时译文
	newTitle := "Updated title"
	updated, err := conversation.NewUpdateTicket(f.env.DB, f.down).Handle(ctx, f.agent, tk.ID, conversation.UpdateTicketParams{Title: &newTitle})
	if err != nil {
		t.Fatal(err)
	}
	if updated.TitleTranslated != "" || !updated.MetaUntranslated {
		t.Fatalf("修改工单标题后应清空旧译文并标为未翻译：%+v", updated)
	}
}

// TestInternalAudienceSymmetryAndPretranslate 检查内部协作通道的目标语言双向对称推导、不扫描无关成员，以及会话参与者精准预译。
func TestInternalAudienceSymmetryAndPretranslate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)

	roles, _ := f.env.Roles.Roles(ctx, f.system.Tenant)
	var roleID string
	for _, r := range roles {
		if r.Tenant == f.system.Tenant {
			roleID = r.ID
		}
	}
	addMember := func(email, name, lang string) identity.Subject {
		m, err := member.NewAdd(f.env.DB, f.env.Roles, f.env.MemberLogins, f.env.Files).Handle(ctx, f.agent, member.AddParams{
			Email: email, Name: name, Password: "password", Language: lang, RoleID: roleID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return identity.Subject{Type: member.Type, ID: m.ID, Tenant: f.system.Tenant, Permissions: auth.WithBase(auth.Owner.Permissions)}
	}
	// 再加两位中文技术使中文成为唯一 PrimaryLanguage，加一位无关巴西同事 pt-BR，再加一位印度代理 hi-Latn
	cn2 := addMember("cn2@alpha.com", "CN2", "zh-CN")
	_ = addMember("cn3@alpha.com", "CN3", "zh-CN")
	_ = addMember("br-unrelated@alpha.com", "BR", "pt-BR")
	inAgent := addMember("in-agent@alpha.com", "Rahul", "hi-Latn")

	// 场景 1：中国技术建单并只由两位中国技术协作 -> 内部讨论绝不回退到无关的巴西同事 pt-BR
	cnOnlyTk, err := conversation.NewCreateTicket(f.env.DB, f.down).Handle(ctx, f.agent, conversation.CreateTicketParams{
		Title: "内部排查", AssigneeID: &cn2.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	preview := NewPreview(f.env.DB, f.translator, job)
	gotSame, err := preview.Handle(ctx, f.agent, cnOnlyTk.ID, PreviewBody{Text: "查一下日志", Mode: PreviewInternal})
	if err != nil {
		t.Fatal(err)
	}
	if gotSame.Needed || gotSame.Language != "" {
		t.Fatalf("全中文协作工单不应推导出无关巴西同事的语言：%+v", gotSame)
	}

	// 场景 2：印度代理（hi-Latn）建单并指派给中国技术（zh-CN）-> 双向完全对称
	crossTk, err := conversation.NewCreateTicket(f.env.DB, f.down).Handle(ctx, inAgent, conversation.CreateTicketParams{
		Title: "Order issue", AssigneeID: &f.agent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 中国技术打开 -> 锁定印度代理语言 hi-Latn
	gotForCN, err := preview.Handle(ctx, f.agent, crossTk.ID, PreviewBody{Text: "请提供订单号", Mode: PreviewInternal})
	if err != nil || !gotForCN.Needed || gotForCN.Language != "hi-Latn" {
		t.Fatalf("中国技术内部讨论应锁定印度代理 hi-Latn：%+v %v", gotForCN, err)
	}
	// 中国技术 @ 另一位中国技术 -> 优先按被 @ 人同语言，不误翻
	gotMentionCN, err := preview.Handle(ctx, f.agent, crossTk.ID, PreviewBody{Text: "@CN2 看下数据库", Mode: PreviewInternal, Mentions: []string{cn2.ID}})
	if err != nil || gotMentionCN.Needed {
		t.Fatalf("@ 同语言同事时不应触发翻译：%+v %v", gotMentionCN, err)
	}
	// 印度代理打开 -> 锁定中国技术语言 zh-CN（双向对称！）
	gotForIN, err := preview.Handle(ctx, inAgent, crossTk.ID, PreviewBody{Text: "Order number 12345 hai", Mode: PreviewInternal})
	if err != nil || !gotForIN.Needed || gotForIN.Language != "zh-CN" {
		t.Fatalf("印度代理内部讨论应反向锁定中国技术 zh-CN：%+v %v", gotForIN, err)
	}

	// 场景 3：会话参与者精准预译（尽管工作区人数最多是 zh-CN，中国技术在工单发消息仍会立刻预译出参与者印度代理的 hi-Latn）
	noteItem, err := conversation.NewAddTicketNote(f.env.DB, f.down).Handle(ctx, f.agent, crossTk.ID, "已经定位到问题", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.model.language = "zh-CN"
	if err := job.Job(ctx, conversation.Appended{TenantID: f.system.Tenant, ConversationID: crossTk.ID, ItemID: noteItem.ID}); err != nil {
		t.Fatal(err)
	}
	saved := f.item(t, noteItem.ID)
	trs, _ := saved.Derived["translations"].(map[string]any)
	if trs["hi-Latn"] == nil {
		t.Fatalf("应立刻按会话参与者预译出 hi-Latn，实际译文表：%+v", trs)
	}

	// 场景 4：源语言按文本判断（文本本身已是目标语言时 needed=false 且跳过盲回译）
	notNeeded := false
	f.model.replyNeeded = &notNeeded
	beforeCalls := f.model.count()
	gotAlreadyTarget, err := preview.Handle(ctx, f.agent, crossTk.ID, PreviewBody{Text: "Main check kar raha hoon", Mode: PreviewInternal})
	if err != nil {
		t.Fatal(err)
	}
	if gotAlreadyTarget.Needed || gotAlreadyTarget.BackTranslation != "" || f.model.count() != beforeCalls+1 {
		t.Fatalf("文本本身已是目标语言时应返回 needed=false 且跳过盲回译：%+v calls=%d", gotAlreadyTarget, f.model.count()-beforeCalls)
	}
	f.model.replyNeeded = nil

	// 场景 5：工作区活跃语言动态包含 pt-BR 与 hi-Latn（零硬编码）
	settingsDTO, err := NewSettingsActions(f.env.DB, NewGroup(f.env.DB), f.translator).Get(ctx, f.agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"zh-CN", "pt-BR", "hi-Latn"} {
		if !slices.Contains(settingsDTO.WorkspaceLanguages, want) {
			t.Fatalf("WorkspaceLanguages 应动态包含 %q，实际：%v", want, settingsDTO.WorkspaceLanguages)
		}
	}
}

// TestContactNotesTranslationAndFill 检查联系人长期备注翻译、FillMissing 补译与编辑后重置旧译文。
func TestContactNotesTranslationAndFill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, true)
	f.model.language = "pt-BR"

	reg := channel.DefaultRegistry()
	reg[channel.Widget] = widget.NewConnector(nil)
	rec := conversation.NewReceive(f.env.DB, reg, f.down)
	in, err := rec.Handle(ctx, f.system, conversation.ReceiveParams{ChannelID: f.ch.ID, From: "u-notes", Text: "Oi"})
	if err != nil {
		t.Fatal(err)
	}
	convDTO, err := conversation.NewGet(f.env.DB).Handle(ctx, f.agent, in.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	cid := convDTO.Contact.ID

	noteText := "Cliente VIP do Brasil, prefere atendimento rapido"
	upd := contact.NewUpdate(f.env.DB, f.env.Roles)
	ct, err := upd.Handle(ctx, f.agent, cid, contact.UpdateBody{Notes: &noteText})
	if err != nil {
		t.Fatal(err)
	}
	if !ct.NotesUntranslated {
		t.Fatalf("新备注尚未翻译前应标为 notes_untranslated=true：%+v", ct)
	}

	// FillMissing 支持直接对 contact_ids 发布补译任务
	fill := NewFillMissing(f.env.DB, f.translator)
	queued, err := fill.HandleBody(ctx, f.agent, FillBody{ContactIDs: []string{cid}})
	if err != nil || queued != 1 {
		t.Fatalf("应排队 1 条联系人备注补译任务：queued=%d err=%v", queued, err)
	}

	job := NewIncoming(f.env.DB, f.locker, f.translator, f.down)
	if err := job.Fill(ctx, FillJob{TenantID: f.system.Tenant, ContactID: cid, Language: "zh-CN"}); err != nil {
		t.Fatal(err)
	}
	ctAfter, err := contact.NewGet(f.env.DB).Handle(ctx, f.agent, cid)
	if err != nil {
		t.Fatal(err)
	}
	if ctAfter.NotesLanguage != "pt-BR" || ctAfter.NotesTranslated != "[zh-CN] contact note" || ctAfter.NotesUntranslated {
		t.Fatalf("联系人备注翻译结果异常：%+v", ctAfter)
	}

	// 修改备注后旧译文自动清空
	noteText2 := "Outro comentario"
	ctEdited, err := upd.Handle(ctx, f.agent, cid, contact.UpdateBody{Notes: &noteText2})
	if err != nil {
		t.Fatal(err)
	}
	if ctEdited.NotesTranslated != "" || !ctEdited.NotesUntranslated {
		t.Fatalf("修改备注后应清空旧译文：%+v", ctEdited)
	}
}
