package conversation_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/search"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// refreshAllSearch 把数据库中所有可检索的时间线项立即同步到全文索引，测试不依赖外部 NATS worker。
func refreshAllSearch(t *testing.T, env *apptest.Env) {
	t.Helper()
	ctx := context.Background()
	after := ""
	for {
		keys, err := conversation.ScanSearchKeys(ctx, env.DB, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) == 0 {
			return
		}
		if err := conversation.SearchIndex.Refresh(ctx, env.DB, conversation.LoadSearchDocs, keys...); err != nil {
			t.Fatal(err)
		}
		after = keys[len(keys)-1]
	}
}

func TestSearchFullTextAndTranslation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	down := conversation.Downstream{}

	// 创建两个工作区验证租户隔离
	t1, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "工作区一", Slug: "ws1", OwnerEmail: "owner1@example.com", OwnerName: "主管理一", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "工作区二", Slug: "ws2", OwnerEmail: "owner2@example.com", OwnerName: "主管理二", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys1 := identity.Subject{Type: identity.System, ID: "test", Tenant: t1.ID}
	sys2 := identity.Subject{Type: identity.System, ID: "test", Tenant: t2.ID}
	owner1, err := member.Resolve(env.DB, env.Roles)(identity.WithTenant(ctx, t1.ID), identity.Subject{Type: member.Type, ID: t1.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	owner2, err := member.Resolve(env.DB, env.Roles)(identity.WithTenant(ctx, t2.ID), identity.Subject{Type: member.Type, ID: t2.OwnerID})
	if err != nil {
		t.Fatal(err)
	}

	ch1, err := channel.NewCreate(env.DB).Handle(ctx, sys1, channel.CreateParams{Kind: channel.Test, Name: "测试渠道一"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := channel.NewCreate(env.DB).Handle(ctx, sys2, channel.CreateParams{Kind: channel.Test, Name: "测试渠道二"})
	if err != nil {
		t.Fatal(err)
	}

	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	reply := conversation.NewReply(env.DB, down)
	addNote := conversation.NewAddNote(env.DB, down)

	// 会话 1：客户发 Hinglish 消息，随后写入工作区主要语言（zh-CN）译文和非主要语言（fr）补译；再追加一条中文回复和内部备注
	r1, err := receive.Handle(ctx, sys1, conversation.ReceiveParams{
		ChannelID: ch1.ID, From: "rahul", FromName: "Rahul", Text: "mera order abhi tak nahi aaya",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.SaveTranslation(ctx, env.DB, down, t1.ID, r1.Item.ID, conversation.Translated{
		Language: "hi-Latn",
		Translations: map[string]string{
			"zh-CN": "我的订单到现在还没有送达",
			"fr":    "Ma commande exclusive_fr_token n'est pas arrivée",
		},
	}); err != nil {
		t.Fatal(err)
	}
	// 同一会话再发一条带翻译的坐席回复，验证多条命中折叠与 MatchCount
	if _, err := reply.Handle(ctx, owner1, r1.ConversationID, conversation.ReplyBody{
		Text: "正在核查您的订单物流状态",
		Translation: &conversation.Translation{
			Language: "hi-Latn",
			Text:     "hum aapka order tracking check kar rahe hain",
		},
	}); err != nil {
		t.Fatal(err)
	}
	// 内部备注包含订单编号
	if _, err := addNote.Handle(ctx, owner1, r1.ConversationID, "关联工单号 ORD-20261122-888 需加急处理", nil); err != nil {
		t.Fatal(err)
	}

	// 会话 2（已关闭）与标签筛选验证
	r2, err := receive.Handle(ctx, sys1, conversation.ReceiveParams{
		ChannelID: ch1.ID, From: "priya", FromName: "Priya", Text: "My withdrawal has been pending for two days",
	})
	if err != nil {
		t.Fatal(err)
	}
	refundTag, err := tag.NewCreate(env.DB).Handle(ctx, sys1, tag.Changes{Name: "提现", Color: tag.Yellow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewAddTag(env.DB, down).Handle(ctx, owner1, r2.ConversationID, refundTag.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewClose(env.DB, down).Handle(ctx, owner1, r2.ConversationID); err != nil {
		t.Fatal(err)
	}

	// 工作区二写入相同关键词，确认租户隔离
	if _, err := receive.Handle(ctx, sys2, conversation.ReceiveParams{
		ChannelID: ch2.ID, From: "other", FromName: "Other", Text: "我的订单和提现问题",
	}); err != nil {
		t.Fatal(err)
	}

	refreshAllSearch(t, env)
	searcher := conversation.NewSearch(env.DB)

	// 1. 按工作区主要语言（中文）搜到 Hinglish 客户消息的译文，且按会话折叠去重（r1 有 2 条消息含“订单”）
	resCN, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "订单"})
	if err != nil {
		t.Fatal(err)
	}
	if resCN.Total != 1 || len(resCN.Items) != 1 {
		t.Fatalf("搜中文“订单”期望 1 个会话，实际 total=%d items=%+v", resCN.Total, resCN.Items)
	}
	hit := resCN.Items[0]
	if hit.Conversation.ID != r1.ConversationID || hit.MatchCount != 2 {
		t.Fatalf("折叠结果异常：conv=%s match_count=%d", hit.Conversation.ID, hit.MatchCount)
	}
	if !hasMark(hit.Match.Snippet, "订单") {
		t.Fatalf("摘要未高亮“订单”：%#v", hit.Match.Snippet)
	}

	// 2. 按原文（Hinglish）搜到同一会话，且展示原文字段高亮
	resOrig, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "abhi"})
	if err != nil {
		t.Fatal(err)
	}
	if resOrig.Total != 1 || resOrig.Items[0].Match.Field != "original" || resOrig.Items[0].Match.Seq != r1.Item.Seq {
		t.Fatalf("原文检索异常：%+v", resOrig.Items)
	}

	// 3. 成员回复发给对方的译文也能搜到
	resReplyTrans, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "tracking"})
	if err != nil {
		t.Fatal(err)
	}
	if resReplyTrans.Total != 1 || resReplyTrans.Items[0].Match.Field != "translated" {
		t.Fatalf("成员回复译文检索异常：%+v", resReplyTrans.Items)
	}

	// 4. 非工作区主要语言的补译（fr）不进入全文索引
	resFR, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "exclusive_fr_token"})
	if err != nil {
		t.Fatal(err)
	}
	if resFR.Total != 0 {
		t.Fatalf("非主要语言译文不应被搜到，实际 total=%d", resFR.Total)
	}

	// 5. 内部备注可以搜到，且标识 kind=note, field=note
	resNote, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "ORD-20261122-888"})
	if err != nil {
		t.Fatal(err)
	}
	if resNote.Total != 1 || resNote.Items[0].Match.Kind != conversation.Note || resNote.Items[0].Match.Field != "note" {
		t.Fatalf("内部备注检索异常：%+v", resNote.Items)
	}

	// 6. 默认跨全部状态（能搜到已关闭会话），传 status=open 时过滤掉，传 tag_id 时精准匹配
	resClosed, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "withdrawal"})
	if err != nil || resClosed.Total != 1 || resClosed.Items[0].Conversation.ID != r2.ConversationID {
		t.Fatalf("默认应能搜到已关闭会话：%+v err=%v", resClosed, err)
	}
	resOpenOnly, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "withdrawal", Status: conversation.Open})
	if err != nil || resOpenOnly.Total != 0 {
		t.Fatalf("status=open 不应包含已关闭会话：total=%d err=%v", resOpenOnly.Total, err)
	}
	resTag, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "withdrawal", TagID: refundTag.ID})
	if err != nil || resTag.Total != 1 {
		t.Fatalf("按标签筛选失败：total=%d err=%v", resTag.Total, err)
	}

	// 7. 租户隔离：工作区二搜不到工作区一的消息，只能搜到自己的
	resWS2, err := searcher.Handle(ctx, owner2, &conversation.SearchInput{Q: "withdrawal"})
	if err != nil || resWS2.Total != 0 {
		t.Fatalf("租户隔离失效：ws2 搜到了 ws1 的会话 %+v err=%v", resWS2, err)
	}
	resWS2Own, err := searcher.Handle(ctx, owner2, &conversation.SearchInput{Q: "提现"})
	if err != nil || resWS2Own.Total != 1 {
		t.Fatalf("ws2 应能搜到自己的会话：total=%d err=%v", resWS2Own.Total, err)
	}

	// 8. 纯标点或空白查询返回空页，不退化为全量列表
	resPunct, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "！！！"})
	if err != nil || resPunct.Total != 0 || len(resPunct.Items) != 0 {
		t.Fatalf("纯标点查询应返回空：%+v err=%v", resPunct, err)
	}

	// 9. 系统事件（如关闭事件）不进入索引
	resEvent, err := searcher.Handle(ctx, owner1, &conversation.SearchInput{Q: "conversation.closed"})
	if err != nil || resEvent.Total != 0 {
		t.Fatalf("系统事件不应进入索引：%+v err=%v", resEvent, err)
	}
}

func TestSearchVisibilityScopes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	down := conversation.Downstream{}

	ws, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "权限测试", Slug: "perm-search", OwnerEmail: "owner@example.com", OwnerName: "主管理", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: ws.ID}
	owner, err := member.Resolve(env.DB, env.Roles)(identity.WithTenant(ctx, ws.ID), identity.Subject{Type: member.Type, ID: ws.OwnerID})
	if err != nil {
		t.Fatal(err)
	}

	// 创建两个角色：仅自己可见（ScopeAssigned）、所在团队可见（ScopeTeam）
	roleAssigned, err := role.NewCreate(env.DB).Handle(ctx, sys, role.Params{
		Name: "仅自己", ConversationScope: auth.ScopeAssigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	roleTeam, err := role.NewCreate(env.DB).Handle(ctx, sys, role.Params{
		Name: "团队内", ConversationScope: auth.ScopeTeam,
	})
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.MemberLogins, env.Files)
	mAssigned, err := addMember.Handle(ctx, sys, member.AddParams{
		Email: "a@example.com", Name: "坐席A", Password: "password123", RoleID: roleAssigned.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	mTeam, err := addMember.Handle(ctx, sys, member.AddParams{
		Email: "b@example.com", Name: "坐席B", Password: "password123", RoleID: roleTeam.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	subAssigned, err := member.Resolve(env.DB, env.Roles)(identity.WithTenant(ctx, ws.ID), identity.Subject{Type: member.Type, ID: mAssigned.ID})
	if err != nil {
		t.Fatal(err)
	}
	subTeam, err := member.Resolve(env.DB, env.Roles)(identity.WithTenant(ctx, ws.ID), identity.Subject{Type: member.Type, ID: mTeam.ID})
	if err != nil {
		t.Fatal(err)
	}

	team1, err := team.NewCreate(env.DB).Handle(ctx, sys, team.Changes{Name: "团队一", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	team2, err := team.NewCreate(env.DB).Handle(ctx, sys, team.Changes{Name: "团队二", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewPutMember(env.DB, env.MemberLogins, env.Files).Handle(ctx, sys, team1.ID, mTeam.ID); err != nil {
		t.Fatal(err)
	}

	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
	assign := conversation.NewAssign(env.DB, down)
	addNote := conversation.NewAddNote(env.DB, down)

	// 会话 1：指派给坐席 A（在团队二）
	c1, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "u1", FromName: "U1", Text: "发票开具问题"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, owner, c1.ConversationID, conversation.Assignment{TeamID: &team2.ID, AssigneeID: &mAssigned.ID}); err != nil {
		t.Fatal(err)
	}
	// 会话 2：在团队一（未指派负责人）
	c2, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "u2", FromName: "U2", Text: "发票抬头修改"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, owner, c2.ConversationID, conversation.Assignment{TeamID: &team1.ID}); err != nil {
		t.Fatal(err)
	}
	// 会话 3：在团队二，但内部备注里 @ 了坐席 A
	c3, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "u3", FromName: "U3", Text: "发票邮寄地址"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, owner, c3.ConversationID, conversation.Assignment{TeamID: &team2.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := addNote.Handle(ctx, owner, c3.ConversationID, "请 @坐席A 协助看下这张发票", []string{mAssigned.ID}); err != nil {
		t.Fatal(err)
	}

	refreshAllSearch(t, env)
	searcher := conversation.NewSearch(env.DB)

	idsOf := func(sub identity.Subject, in conversation.SearchInput) []string {
		res, err := searcher.Handle(ctx, sub, &in)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range res.Items {
			out = append(out, h.Conversation.ID)
		}
		slices.Sort(out)
		return out
	}

	// 主管理员（ScopeAll）：看到全部 3 个会话
	wantAll := []string{c1.ConversationID, c2.ConversationID, c3.ConversationID}
	slices.Sort(wantAll)
	if got := idsOf(owner, conversation.SearchInput{Q: "发票"}); !slices.Equal(got, wantAll) {
		t.Fatalf("ScopeAll = %v, want %v", got, wantAll)
	}

	// 坐席 A（ScopeAssigned）：看到指派给自己的 c1 + 被 @ 的 c3，看不到团队一的 c2
	wantAssigned := []string{c1.ConversationID, c3.ConversationID}
	slices.Sort(wantAssigned)
	if got := idsOf(subAssigned, conversation.SearchInput{Q: "发票"}); !slices.Equal(got, wantAssigned) {
		t.Fatalf("ScopeAssigned = %v, want %v", got, wantAssigned)
	}

	// 坐席 B（ScopeTeam，仅在团队一）：只看到 c2，看不到团队二的 c1 和 c3
	if got := idsOf(subTeam, conversation.SearchInput{Q: "发票"}); !slices.Equal(got, []string{c2.ConversationID}) {
		t.Fatalf("ScopeTeam = %v, want [%s]", got, c2.ConversationID)
	}

	// 分页检查
	p1, err := searcher.Handle(ctx, owner, &conversation.SearchInput{Q: "发票", OffsetParams: page.OffsetParams{Page: 1, PerPage: 2}})
	if err != nil || p1.Total != 3 || len(p1.Items) != 2 {
		t.Fatalf("page1 total=%d len=%d err=%v", p1.Total, len(p1.Items), err)
	}
	p2, err := searcher.Handle(ctx, owner, &conversation.SearchInput{Q: "发票", OffsetParams: page.OffsetParams{Page: 2, PerPage: 2}})
	if err != nil || p2.Total != 3 || len(p2.Items) != 1 {
		t.Fatalf("page2 total=%d len=%d err=%v", p2.Total, len(p2.Items), err)
	}
}

func hasMark(snippet search.Snippet, word string) bool {
	for _, p := range snippet {
		if p.Mark && strings.Contains(p.Text, word) {
			return true
		}
	}
	return false
}
