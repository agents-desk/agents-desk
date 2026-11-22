package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/attribute"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tag"
	"agents-desk/internal/tenant"
)

// TestViews 在完整路由上验证 V1 视图全链路：
//  1. 共享/个人作用域与 views:manage 权限、个人视图隔离、跨工作区 404、成员移出清理个人视图；
//  2. 三种会话可见范围（all/team/assigned）下 /inbox 单 SQL 聚合计数、/conversations?view_id 列表、
//     /conversations/search?view_id 搜索三处口径严格一致且不越权；
//  3. 动态条件（me、my_teams）、多状态、等待回复、优先级、渠道、多标签（any/all）、客户语言、自定义属性筛选、
//     同构 filter 临时微调预览、视图排序与渠道列表接口。
func TestViews(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
		channels: channel.DefaultRegistry(),
	}
	h := a.routes(nil)

	tenants := map[string]string{}
	for _, slug := range []string{"alpha", "beta"} {
		tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: slug + " 主管理员", OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		tenants[slug] = tn.ID
	}

	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	idOf := func(body string) string {
		var v struct{ ID string }
		_ = json.Unmarshal([]byte(body), &v)
		return v.ID
	}

	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	soloRole := idOf(call(h, "POST", "/t/alpha/api/roles", `{"name":"专员","conversation_scope":"assigned"}`, owner).body)

	agent1ID := idOf(call(h, "POST", "/t/alpha/api/members", `{"email":"agent1@alpha.com","name":"坐席1","password":"password","role_id":"`+agentRole+`"}`, owner).body)
	soloID := idOf(call(h, "POST", "/t/alpha/api/members", `{"email":"solo@alpha.com","name":"专员","password":"password","role_id":"`+soloRole+`"}`, owner).body)
	agent1, solo := login("agent1@alpha.com"), login("solo@alpha.com")

	teamA := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"A线"}`, owner).body)
	teamB := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"B线"}`, owner).body)
	call(h, "PUT", "/t/alpha/api/teams/"+teamA+"/members/"+agent1ID, "", owner)

	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tenants["alpha"]}
	chA, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "渠道A", TeamID: &teamA})
	if err != nil {
		t.Fatal(err)
	}
	chB, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "渠道B", TeamID: &teamB})
	if err != nil {
		t.Fatal(err)
	}

	// 普通成员可以读取本工作区渠道列表（用于视图筛选），看不到其他工作区的渠道
	chResp := call(h, "GET", "/t/alpha/api/channels", "", agent1)
	if chResp.status != http.StatusOK {
		t.Fatalf("渠道列表：%d %s", chResp.status, chResp.body)
	}
	var chOpts []channel.ListItem
	if err := json.Unmarshal([]byte(chResp.body), &chOpts); err != nil || len(chOpts) != 2 {
		t.Fatalf("渠道选项异常：%v %s", err, chResp.body)
	}
	betaChResp := call(h, "GET", "/t/beta/api/channels", "", betaOwner)
	if betaChResp.status != http.StatusOK || strings.TrimSpace(betaChResp.body) != "[]" {
		t.Fatalf("beta 渠道列表应为空：%d %s", betaChResp.status, betaChResp.body)
	}

	// 创建标签与会话自定义属性
	tagVIP, err := tag.NewCreate(env.DB).Handle(ctx, system, tag.Changes{Name: "VIP", Color: tag.Yellow})
	if err != nil {
		t.Fatal(err)
	}
	tagRefund, err := tag.NewCreate(env.DB).Handle(ctx, system, tag.Changes{Name: "退款", Color: tag.Red})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attribute.NewCreate(env.DB).Handle(ctx, system, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "issue_type", Name: "问题类型", Type: attribute.TypeSelect, Options: []string{"退款", "物流"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := attribute.NewCreate(env.DB).Handle(ctx, system, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "amount", Name: "金额", Type: attribute.TypeNumber,
	}); err != nil {
		t.Fatal(err)
	}

	// 准备 3 个会话：
	// c1: A线、未回复、高优、标签 VIP+退款、issue_type=退款、amount=200、语言 en-US、负责人 agent1
	// c2: A线、已回复、普通、标签 VIP、issue_type=物流、amount=50、负责人 solo
	// c3: B线、未回复、高优、标签 退款、issue_type=退款、amount=500（agent1 与 solo 均不可见）
	receive := conversation.NewReceive(env.DB, a.channels, a.downstream())
	r1, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: chA.ID, From: "c1", Text: "refund order alpha"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: chA.ID, From: "c2", Text: "shipping question alpha"})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: chB.ID, From: "c3", Text: "refund order beta"})
	if err != nil {
		t.Fatal(err)
	}
	c1, c2, c3 := r1.ConversationID, r2.ConversationID, r3.ConversationID

	if _, err := conversation.NewSetPriority(env.DB, a.downstream()).Handle(ctx, system, c1, conversation.High); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewSetPriority(env.DB, a.downstream()).Handle(ctx, system, c3, conversation.High); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewSetLanguage(env.DB, a.downstream()).Handle(ctx, system, c1, "en-US"); err != nil {
		t.Fatal(err)
	}
	addTag := conversation.NewAddTag(env.DB, a.downstream())
	for _, pair := range [][2]string{{c1, tagVIP.ID}, {c1, tagRefund.ID}, {c2, tagVIP.ID}, {c3, tagRefund.ID}} {
		if _, err := addTag.Handle(ctx, system, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	patchAttr := conversation.NewPatchAttributes(env.DB, a.downstream())
	if _, err := patchAttr.Handle(ctx, system, c1, map[string]any{"issue_type": "退款", "amount": 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := patchAttr.Handle(ctx, system, c2, map[string]any{"issue_type": "物流", "amount": 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := patchAttr.Handle(ctx, system, c3, map[string]any{"issue_type": "退款", "amount": 500}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE conversations SET assignee_type = 'member', assignee_id = ? WHERE id = ?", agent1ID, c1).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw("UPDATE conversations SET assignee_type = 'member', assignee_id = ? WHERE id = ?", soloID, c2).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if r := call(h, "POST", "/t/alpha/api/conversations/"+c2+"/replies", `{"text":"好的"}`, solo); r.status != http.StatusCreated {
		t.Fatalf("回复 c2：%d %s", r.status, r.body)
	}
	// 同步全文索引用于验证视图内搜索
	var itemIDs []string
	if err := env.DB.NewSelect().Model((*conversation.Item)(nil)).ColumnExpr("id::text").
		Where("tenant_id = ? AND kind = ? AND actor_type <> ?", tenants["alpha"], conversation.Message, conversation.SystemActor).
		Scan(ctx, &itemIDs); err != nil {
		t.Fatal(err)
	}
	if err := conversation.SearchIndex.Refresh(ctx, env.DB, conversation.LoadSearchDocs, itemIDs...); err != nil {
		t.Fatal(err)
	}

	// 1. 权限验证：坐席无 views:manage 不能建共享视图，但可以建个人视图
	if r := call(h, "POST", "/t/alpha/api/views", `{"scope":"shared","name":"越权共享","config":{"statuses":["open"]}}`, agent1); r.status != http.StatusForbidden {
		t.Fatalf("坐席建共享视图应 403，实际 %d %s", r.status, r.body)
	}
	personalResp := call(h, "POST", "/t/alpha/api/views", `{
		"scope":"personal","name":"我的高优双标",
		"config":{"statuses":["open"],"assignee":{"mode":"me"},"tag_ids":["`+tagVIP.ID+`","`+tagRefund.ID+`"],"tag_match":"all","language":"en","sort":"recent"}
	}`, agent1)
	if personalResp.status != http.StatusCreated {
		t.Fatalf("坐席建个人视图失败：%d %s", personalResp.status, personalResp.body)
	}
	personalID := idOf(personalResp.body)

	// 管理员建共享视图：高优大额退款（使用属性过滤 + priority + waiting）
	sharedResp := call(h, "POST", "/t/alpha/api/views", `{
		"scope":"shared","name":"高优大额退款",
		"config":{
			"statuses":["open"],"priority":"high","waiting":true,
			"attributes":[
				{"key":"issue_type","op":"eq","value":"退款"},
				{"key":"amount","op":"gte","value":100}
			],
			"sort":"waiting"
		}
	}`, owner)
	if sharedResp.status != http.StatusCreated {
		t.Fatalf("管理员建共享视图失败：%d %s", sharedResp.status, sharedResp.body)
	}
	sharedID := idOf(sharedResp.body)

	// 管理员再建一个带动态条件 my_teams 的共享视图并验证 Reorder
	myTeamsResp := call(h, "POST", "/t/alpha/api/views", `{
		"scope":"shared","name":"我所在团队全部",
		"config":{"statuses":["open"],"team":{"mode":"my_teams"}}
	}`, owner)
	if myTeamsResp.status != http.StatusCreated {
		t.Fatalf("管理员建 my_teams 视图失败：%d %s", myTeamsResp.status, myTeamsResp.body)
	}
	myTeamsID := idOf(myTeamsResp.body)

	// 个人视图隔离：主管理员访问或使用坐席的个人视图均返回 404
	if r := call(h, "GET", "/t/alpha/api/views/"+personalID, "", owner); r.status != http.StatusNotFound {
		t.Fatalf("他人访问个人视图应 404，实际 %d", r.status)
	}
	if r := call(h, "GET", "/t/alpha/api/conversations?view_id="+personalID, "", owner); r.status != http.StatusNotFound {
		t.Fatalf("他人按个人视图列会话应 404，实际 %d", r.status)
	}
	// 跨工作区隔离：beta 不能访问 alpha 的共享视图
	if r := call(h, "GET", "/t/beta/api/views/"+sharedID, "", betaOwner); r.status != http.StatusNotFound {
		t.Fatalf("跨工作区访问视图应 404，实际 %d", r.status)
	}

	// 2. 校验 /inbox 计数、/conversations?view_id 与 /conversations/search?view_id 在三种可见范围下严格一致
	listIDs := func(cookie *http.Cookie, query string) []string {
		t.Helper()
		r := call(h, "GET", "/t/alpha/api/conversations?"+query, "", cookie)
		if r.status != http.StatusOK {
			t.Fatalf("会话列表 %s：%d %s", query, r.status, r.body)
		}
		var page struct {
			Items []struct{ ID string }
		}
		if err := json.Unmarshal([]byte(r.body), &page); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(page.Items))
		for i, it := range page.Items {
			ids[i] = it.ID
		}
		slices.Sort(ids)
		return ids
	}
	inboxViews := func(cookie *http.Cookie) map[string]conversation.ViewSummary {
		t.Helper()
		r := call(h, "GET", "/t/alpha/api/inbox", "", cookie)
		if r.status != http.StatusOK {
			t.Fatalf("inbox：%d %s", r.status, r.body)
		}
		var dto conversation.InboxDTO
		if err := json.Unmarshal([]byte(r.body), &dto); err != nil {
			t.Fatal(err)
		}
		m := make(map[string]conversation.ViewSummary, len(dto.Views))
		for _, v := range dto.Views {
			m[v.ID] = v
		}
		return m
	}

	// ScopeAll（owner）：高优大额退款看到 c1、c3（count=2, waiting=2），看不到 agent1 的个人视图
	ownerViews := inboxViews(owner)
	if _, ok := ownerViews[personalID]; ok {
		t.Fatal("owner 的 inbox 不应出现他人个人视图")
	}
	if ownerViews[sharedID].Count != 2 || ownerViews[sharedID].Waiting != 2 {
		t.Fatalf("owner 共享视图计数错误：%+v", ownerViews[sharedID])
	}
	wantOwner := []string{c1, c3}
	slices.Sort(wantOwner)
	if got := listIDs(owner, "view_id="+sharedID); !slices.Equal(got, wantOwner) {
		t.Fatalf("owner 共享视图列表 = %v, want %v", got, wantOwner)
	}

	// ScopeTeam（agent1）：高优大额退款只能看到 A 线的 c1（count=1），my_teams 看到 c1+c2（count=2），个人视图看到 c1（count=1）
	agentViews := inboxViews(agent1)
	if agentViews[sharedID].Count != 1 || agentViews[sharedID].Editable {
		t.Fatalf("agent1 共享视图计数/可编辑错误：%+v", agentViews[sharedID])
	}
	if agentViews[myTeamsID].Count != 2 {
		t.Fatalf("agent1 my_teams 视图计数错误：%+v", agentViews[myTeamsID])
	}
	if agentViews[personalID].Count != 1 || !agentViews[personalID].Editable {
		t.Fatalf("agent1 个人视图计数/可编辑错误：%+v", agentViews[personalID])
	}
	if got := listIDs(agent1, "view_id="+sharedID); !slices.Equal(got, []string{c1}) {
		t.Fatalf("agent1 共享视图列表 = %v, want [%s]", got, c1)
	}
	if got := listIDs(agent1, "view_id="+personalID); !slices.Equal(got, []string{c1}) {
		t.Fatalf("agent1 个人视图列表 = %v, want [%s]", got, c1)
	}

	// ScopeAssigned（solo）：高优大额退款（c1 和 c3 都不归 solo）计数 0、列表空
	soloViews := inboxViews(solo)
	if soloViews[sharedID].Count != 0 {
		t.Fatalf("solo 共享视图计数应为 0，实际 %+v", soloViews[sharedID])
	}
	if got := listIDs(solo, "view_id="+sharedID); len(got) != 0 {
		t.Fatalf("solo 共享视图列表应为空，实际 %v", got)
	}

	// 3. 视图内全文搜索：owner 在共享视图内搜 "refund" 命中 c1、c3；agent1 仅命中 c1
	searchIDs := func(cookie *http.Cookie, q, viewID string) []string {
		t.Helper()
		r := call(h, "GET", "/t/alpha/api/conversations/search?q="+url.QueryEscape(q)+"&view_id="+viewID, "", cookie)
		if r.status != http.StatusOK {
			t.Fatalf("search：%d %s", r.status, r.body)
		}
		var page struct {
			Items []struct {
				Conversation struct{ ID string }
			}
		}
		if err := json.Unmarshal([]byte(r.body), &page); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(page.Items))
		for i, it := range page.Items {
			ids[i] = it.Conversation.ID
		}
		slices.Sort(ids)
		return ids
	}
	if got := searchIDs(owner, "refund", sharedID); !slices.Equal(got, wantOwner) {
		t.Fatalf("owner 视图内搜索 = %v, want %v", got, wantOwner)
	}
	if got := searchIDs(agent1, "refund", sharedID); !slices.Equal(got, []string{c1}) {
		t.Fatalf("agent1 视图内搜索 = %v, want [%s]", got, c1)
	}

	// 4. 同构 filter 参数临时微调预览（不修改已保存视图）：仅按渠道 B 过滤，owner 只看到 c3
	tmpFilter := url.QueryEscape(`{"statuses":["open"],"channel_ids":["` + chB.ID + `"]}`)
	if got := listIDs(owner, "view_id="+sharedID+"&filter="+tmpFilter); !slices.Equal(got, []string{c3}) {
		t.Fatalf("临时微调预览 = %v, want [%s]", got, c3)
	}

	// 5. Reorder 共享视图顺序
	if r := call(h, "PUT", "/t/alpha/api/views/reorder", `{"scope":"shared","ids":["`+myTeamsID+`","`+sharedID+`"]}`, owner); r.status != http.StatusNoContent {
		t.Fatalf("reorder：%d %s", r.status, r.body)
	}
	listViewsResp := call(h, "GET", "/t/alpha/api/views?scope=shared", "", owner)
	var sharedList []conversation.ViewDTO
	if err := json.Unmarshal([]byte(listViewsResp.body), &sharedList); err != nil || len(sharedList) != 2 {
		t.Fatalf("列出共享视图异常：%v %s", err, listViewsResp.body)
	}
	if sharedList[0].ID != myTeamsID || sharedList[1].ID != sharedID {
		t.Fatalf("reorder 顺序未生效：%+v", sharedList)
	}

	// 6. 移出成员后其个人视图自动清理，共享视图保留
	if r := call(h, "POST", "/t/alpha/api/members/"+agent1ID+"/remove", "", owner); r.status != http.StatusNoContent {
		t.Fatalf("移出成员：%d %s", r.status, r.body)
	}
	var personalCount int
	if err := env.DB.NewRaw("SELECT count(*) FROM saved_views WHERE tenant = ? AND owner_id = ?", tenants["alpha"], agent1ID).Scan(ctx, &personalCount); err != nil {
		t.Fatal(err)
	}
	if personalCount != 0 {
		t.Fatalf("移出成员后个人视图应清空，实际剩余 %d", personalCount)
	}
	if len(inboxViews(owner)) != 2 {
		t.Fatalf("移出成员后共享视图应保留 2 个，实际 %d", len(inboxViews(owner)))
	}
}
