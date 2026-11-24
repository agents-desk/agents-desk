package contact_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

type testWorkspace struct {
	env     *apptest.Env
	tenant  string
	system  identity.Subject
	agent   identity.Subject
	channel *channel.Channel
}

func setupWorkspace(t *testing.T, env *apptest.Env, slug string) testWorkspace {
	t.Helper()
	ctx := context.Background()
	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	ag := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}
	return testWorkspace{env: env, tenant: tn.ID, system: sys, agent: ag, channel: ch}
}

func strPtr(s string) *string { return &s }

// TestListAndSummary 检查联系人列表筛选、搜索、排序、分页、概览计数，以及跨工作区隔离。
func TestListAndSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setupWorkspace(t, env, "alpha")
	beta := setupWorkspace(t, env, "beta")

	ch2, err := channel.NewCreate(env.DB).Handle(ctx, alpha.system, channel.CreateParams{Kind: channel.Test, Name: "渠道二"})
	if err != nil {
		t.Fatal(err)
	}
	var userA, leadA *contact.Contact
	err = database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		userA, err = contact.ResolveUser(ctx, tx, alpha.tenant, alpha.channel.ID, "user:u1", "u1", contact.Profile{Name: "Rahul Sharma", Email: "rahul@example.com"})
		if err != nil {
			return err
		}
		leadA, _, err = contact.ResolveIdentity(ctx, tx, alpha.tenant, ch2.ID, "v1", contact.Profile{Name: "Visitor One"})
		if err != nil {
			return err
		}
		_, _, err = contact.ResolveIdentity(ctx, tx, beta.tenant, beta.channel.ID, "v-beta", contact.Profile{Name: "Beta Only"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// 设置语言并给 userA 造一个会话
	if _, err := contact.NewUpdate(env.DB).Handle(ctx, alpha.agent, userA.ID, contact.UpdateBody{Language: strPtr("hi-Latn")}); err != nil {
		t.Fatal(err)
	}
	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{})
	if _, err := receive.Handle(ctx, alpha.system, conversation.ReceiveParams{ChannelID: alpha.channel.ID, From: "user:u1", Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	// 概览：只计当前工作区
	sum, err := contact.NewGetSummary(env.DB).Handle(ctx, alpha.agent)
	if err != nil || sum.All != 2 || sum.Users != 1 || sum.Leads != 1 || len(sum.Channels) != 2 {
		t.Fatalf("alpha 概览：%+v %v", sum, err)
	}
	betaSum, err := contact.NewGetSummary(env.DB).Handle(ctx, beta.agent)
	if err != nil || betaSum.All != 1 || betaSum.Leads != 1 || betaSum.Users != 0 {
		t.Fatalf("beta 概览：%+v %v", betaSum, err)
	}

	list := contact.NewList(env.DB)
	// 游标分页：每页 1 条，翻两页正好不重不漏
	p1, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 1}})
	if err != nil || len(p1.Items) != 1 || p1.NextCursor == "" {
		t.Fatalf("游标第 1 页：%+v %v", p1, err)
	}
	p2, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 1, Cursor: p1.NextCursor}})
	if err != nil || len(p2.Items) != 1 || p2.Items[0].ID == p1.Items[0].ID || p2.NextCursor != "" {
		t.Fatalf("游标第 2 页：%+v %v", p2, err)
	}
	// 按类型筛选
	users, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Kind: contact.User})
	if err != nil || len(users.Items) != 1 || users.Items[0].ID != userA.ID || users.Items[0].ConversationCount != 1 {
		t.Fatalf("按用户筛选：%+v %v", users, err)
	}
	leads, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Kind: contact.Lead})
	if err != nil || len(leads.Items) != 1 || leads.Items[0].ID != leadA.ID {
		t.Fatalf("按线索筛选：%+v %v", leads, err)
	}
	// 关键词搜名字、邮箱、外部编号
	for _, kw := range []string{"rahul", "rahul@example.com", "u1"} {
		res, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Q: kw})
		if err != nil || len(res.Items) != 1 || res.Items[0].ID != userA.ID {
			t.Errorf("关键词 %q：%+v %v", kw, res, err)
		}
	}
	// 按语言筛选：支持精确标签和主语言前缀（例如 hi 命中 hi-Latn）
	for _, wantLang := range []string{"hi-latn", "hi"} {
		byLang, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Language: wantLang})
		if err != nil || len(byLang.Items) != 1 || byLang.Items[0].ID != userA.ID {
			t.Fatalf("按语言 %q 筛选：%+v %v", wantLang, byLang, err)
		}
	}
	// 按渠道筛选
	byCh, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, ChannelID: ch2.ID})
	if err != nil || len(byCh.Items) != 1 || byCh.Items[0].ID != leadA.ID {
		t.Fatalf("按渠道筛选：%+v %v", byCh, err)
	}
	// 跨工作区隔离：beta 看不到 alpha 的联系人
	betaRes, err := list.Handle(ctx, beta.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "Rahul"})
	if err != nil || len(betaRes.Items) != 0 {
		t.Fatalf("beta 搜索 alpha 的联系人：%+v %v", betaRes, err)
	}
}

// TestGetAndUpdate 检查联系人资料读取、修改、字段校验、语言同步和跨工作区隔离。
func TestGetAndUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setupWorkspace(t, env, "alpha")
	beta := setupWorkspace(t, env, "beta")

	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{})
	rec, err := receive.Handle(ctx, alpha.system, conversation.ReceiveParams{ChannelID: alpha.channel.ID, From: "v-1", FromName: "Guest", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := conversation.NewGet(env.DB).Handle(ctx, alpha.agent, rec.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	cid := conv.Contact.ID

	// 读取详情包含渠道身份和会话数
	detail, err := contact.NewGet(env.DB).Handle(ctx, alpha.agent, cid)
	if err != nil || detail.ConversationCount != 1 || len(detail.Identities) != 1 || detail.Identities[0].ExternalID != "v-1" || detail.Identities[0].Channel.Name != "测试渠道" {
		t.Fatalf("读取详情：%+v %v", detail, err)
	}
	// 跨工作区读取和修改都返回 ErrNotFound
	if _, err := contact.NewGet(env.DB).Handle(ctx, beta.agent, cid); !errors.Is(err, contact.ErrNotFound) {
		t.Errorf("跨工作区读取：%v", err)
	}
	if _, err := contact.NewUpdate(env.DB).Handle(ctx, beta.agent, cid, contact.UpdateBody{Notes: strPtr("hack")}); !errors.Is(err, contact.ErrNotFound) {
		t.Errorf("跨工作区修改：%v", err)
	}

	// 字段校验
	upd := contact.NewUpdate(env.DB)
	if _, err := upd.Handle(ctx, alpha.agent, cid, contact.UpdateBody{Email: strPtr("not-an-email")}); !errors.Is(err, contact.ErrInvalidEmail) {
		t.Errorf("无效邮箱：%v", err)
	}
	if _, err := upd.Handle(ctx, alpha.agent, cid, contact.UpdateBody{Language: strPtr("1234567890-bad")}); !errors.Is(err, contact.ErrInvalidLanguage) {
		t.Errorf("无效语言：%v", err)
	}
	if _, err := upd.Handle(ctx, alpha.agent, cid, contact.UpdateBody{TimeZone: strPtr("Local")}); !errors.Is(err, contact.ErrInvalidTimeZone) {
		t.Errorf("无效时区 Local：%v", err)
	}
	if _, err := upd.Handle(ctx, alpha.agent, cid, contact.UpdateBody{TimeZone: strPtr("Mars/Phobos")}); !errors.Is(err, contact.ErrInvalidTimeZone) {
		t.Errorf("不存在的时区：%v", err)
	}

	// 修改资料并同步会话语言
	saved, err := upd.Handle(ctx, alpha.agent, cid, contact.UpdateBody{
		Name: strPtr("Ananya"), Email: strPtr("ananya@example.com"),
		Language: strPtr("hi-latn"), Location: strPtr("Mumbai"), TimeZone: strPtr("Asia/Kolkata"), Notes: strPtr("VIP 客户"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "Ananya" || saved.Email != "ananya@example.com" || saved.Language != "hi-Latn" || !saved.LanguageManual ||
		saved.Location != "Mumbai" || saved.TimeZone != "Asia/Kolkata" || saved.Notes != "VIP 客户" {
		t.Fatalf("保存后：%+v", saved)
	}
	convAfter, err := conversation.NewGet(env.DB).Handle(ctx, alpha.agent, rec.ConversationID)
	if err != nil || convAfter.Language != "hi-Latn" || !convAfter.Contact.LanguageManual {
		t.Fatalf("同步到会话：%+v %v", convAfter, err)
	}
	// 只改备注不动其他字段
	onlyNotes, err := upd.Handle(ctx, alpha.agent, cid, contact.UpdateBody{Notes: strPtr("更新备注")})
	if err != nil || onlyNotes.Name != "Ananya" || onlyNotes.Notes != "更新备注" {
		t.Fatalf("只改备注：%+v %v", onlyNotes, err)
	}
	// 恢复自动识别
	auto, err := upd.Handle(ctx, alpha.agent, cid, contact.UpdateBody{Language: strPtr("")})
	if err != nil || auto.LanguageManual || auto.Language != "" {
		t.Fatalf("恢复自动识别：%+v %v", auto, err)
	}
}

// TestMergeLead 检查线索合并到用户：资料回填、备注拼接、多渠道身份迁移、消费旧访客身份、吊销旧令牌和幂等性。
func TestMergeLead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	w := setupWorkspace(t, env, "alpha")
	otherCh, err := channel.NewCreate(env.DB).Handle(ctx, w.system, channel.CreateParams{Kind: channel.Test, Name: "其他渠道"})
	if err != nil {
		t.Fatal(err)
	}
	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour})

	var lead, user *contact.Contact
	err = database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		lead, _, err = contact.ResolveIdentity(ctx, tx, w.tenant, w.channel.ID, "visitor:abc", contact.Profile{
			Name: "Visitor Name", Email: "lead@example.com", TimeZone: "Asia/Shanghai", Referrer: "https://example.com/start",
		})
		if err != nil {
			return err
		}
		// 线索还在另一个渠道有身份
		if _, err := tx.NewInsert().Model(&contact.Identity{
			TenantID: w.tenant, ChannelID: otherCh.ID, ExternalID: "tg-123", ContactID: lead.ID, CreatedAt: time.Now().UTC(),
		}).Exec(ctx); err != nil {
			return err
		}
		user, err = contact.ResolveUser(ctx, tx, w.tenant, w.channel.ID, "user:u7", "u7", contact.Profile{Name: "Official User"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// 给线索和用户分别写备注
	if _, err := contact.NewUpdate(env.DB).Handle(ctx, w.agent, lead.ID, contact.UpdateBody{Notes: strPtr("线索备注"), Language: strPtr("en")}); err != nil {
		t.Fatal(err)
	}
	if _, err := contact.NewUpdate(env.DB).Handle(ctx, w.agent, user.ID, contact.UpdateBody{Notes: strPtr("用户备注")}); err != nil {
		t.Fatal(err)
	}
	// 给线索发一条消息建会话，并签一个联系人令牌
	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{})
	rec, err := receive.Handle(ctx, w.system, conversation.ReceiveParams{ChannelID: w.channel.ID, From: "visitor:abc", Text: "help me"})
	if err != nil {
		t.Fatal(err)
	}
	leadTok, err := tokens.Login(ctx, identity.Subject{Type: contact.Type, ID: lead.ID, Tenant: w.tenant}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}

	var convIDs []string
	err = database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		_, convIDs, err = contact.MergeLead(ctx, tx, w.tenant, lead.ID, user.ID, w.channel.ID, "visitor:abc")
		return err
	})
	if err != nil || len(convIDs) != 1 || convIDs[0] != rec.ConversationID {
		t.Fatalf("合并失败：convIDs=%v err=%v", convIDs, err)
	}

	// 线索令牌已吊销
	if _, err := tokens.Validate(ctx, leadTok); !errors.Is(err, apitoken.ErrInvalid) {
		t.Errorf("旧令牌应当失效：%v", err)
	}
	// 检查合并后的用户：保留 Official User，回填 lead@example.com / 时区 / 来源页面 / 手动语言，拼接备注，消费 visitor:abc 但保留 tg-123
	detail, err := contact.NewGet(env.DB).Handle(ctx, w.agent, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "Official User" || detail.Email != "lead@example.com" || detail.TimeZone != "Asia/Shanghai" ||
		detail.Referrer != "https://example.com/start" || detail.Notes != "用户备注\n\n线索备注" ||
		detail.Language != "en" || !detail.LanguageManual || detail.ConversationCount != 1 {
		t.Fatalf("合并后资料不对：%+v", detail)
	}
	if len(detail.Identities) != 2 {
		t.Fatalf("期望保留 user:u7 和 tg-123 两个身份：%+v", detail.Identities)
	}
	// 再次合并同一个 leadID 幂等不报错
	err = database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		_, ids, err := contact.MergeLead(ctx, tx, w.tenant, lead.ID, user.ID, w.channel.ID, "visitor:abc")
		if len(ids) != 0 {
			t.Errorf("重复合并不应再有会话迁移：%v", ids)
		}
		return err
	})
	if err != nil {
		t.Fatalf("重复合并报错：%v", err)
	}
}

// TestVisibilityScope 检查联系人的可见范围隔离：同一工作区多个品牌（渠道 + 团队收件箱）下，
// ScopeAll / ScopeTeam / ScopeAssigned / 系统主体在列表、概览、读取、修改和会话数统计上的隔离与协作规则。
func TestVisibilityScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	w := setupWorkspace(t, env, "brands")
	owner := identity.Subject{
		Type: member.Type, ID: w.agent.ID, Tenant: w.tenant,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}

	// 两个品牌团队收件箱：品牌 A 和品牌 B
	createTeam := team.NewCreate(env.DB)
	teamA, err := createTeam.Handle(ctx, owner, team.Changes{Name: "品牌 A 客服"})
	if err != nil {
		t.Fatal(err)
	}
	teamB, err := createTeam.Handle(ctx, owner, team.Changes{Name: "品牌 B 客服"})
	if err != nil {
		t.Fatal(err)
	}

	// 三个渠道：chA 绑 teamA、chB 绑 teamB、w.channel 未绑定团队
	createCh := channel.NewCreate(env.DB)
	chA, err := createCh.Handle(ctx, w.system, channel.CreateParams{Kind: channel.Test, Name: "品牌 A 官网", TeamID: &teamA.ID})
	if err != nil {
		t.Fatal(err)
	}
	chB, err := createCh.Handle(ctx, w.system, channel.CreateParams{Kind: channel.Test, Name: "品牌 B 官网", TeamID: &teamB.ID})
	if err != nil {
		t.Fatal(err)
	}

	// 创建角色和成员：agentA（teamA, ScopeTeam）、agentB/张三（teamB, ScopeTeam）、assignedOnly（teamB, ScopeAssigned）、homeless（无团队, ScopeTeam）
	createRole := role.NewCreate(env.DB, env.Roles)
	teamRole, err := createRole.Handle(ctx, owner, role.Params{Name: "品牌坐席", ConversationScope: auth.ScopeTeam})
	if err != nil {
		t.Fatal(err)
	}
	assignedRole, err := createRole.Handle(ctx, owner, role.Params{Name: "仅指派坐席", ConversationScope: auth.ScopeAssigned})
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, nil)
	putMember := team.NewPutMember(env.DB, env.Roles, env.MemberLogins, nil)
	newAgent := func(email, name, roleID string, scope auth.ConversationScope, teamID string) identity.Subject {
		t.Helper()
		m, err := addMember.Handle(ctx, owner, member.AddParams{Email: email, Name: name, Password: "password", RoleID: roleID})
		if err != nil {
			t.Fatal(err)
		}
		if teamID != "" {
			if _, err := putMember.Handle(ctx, owner, teamID, m.ID); err != nil {
				t.Fatal(err)
			}
		}
		return identity.Subject{Type: member.Type, ID: m.ID, Tenant: w.tenant, Permissions: auth.WithBase(auth.RolePermissions(scope, nil))}
	}
	agentA := newAgent("a@brands.com", "品牌A坐席", teamRole.ID, auth.ScopeTeam, teamA.ID)
	agentB := newAgent("b@brands.com", "张三", teamRole.ID, auth.ScopeTeam, teamB.ID)
	assignedOnly := newAgent("assigned@brands.com", "仅指派", assignedRole.ID, auth.ScopeAssigned, teamB.ID)
	homeless := newAgent("homeless@brands.com", "无团队", teamRole.ID, auth.ScopeTeam, "")

	// 创建 5 个联系人：品牌 A（无会话/有会话）、品牌 B（无会话/有会话）、未绑定渠道（有会话）
	var cAOnly, cAConv, cBOnly, cBConv, cUnassigned *contact.Contact
	err = database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if cAOnly, err = contact.ResolveUser(ctx, tx, w.tenant, chA.ID, "user:a-only", "a-only", contact.Profile{Name: "BrandA Silent"}); err != nil {
			return err
		}
		if cAConv, err = contact.ResolveUser(ctx, tx, w.tenant, chA.ID, "user:a-conv", "a-conv", contact.Profile{Name: "BrandA Active"}); err != nil {
			return err
		}
		if cBOnly, _, err = contact.ResolveIdentity(ctx, tx, w.tenant, chB.ID, "visitor:b-only", contact.Profile{Name: "BrandB Lead"}); err != nil {
			return err
		}
		if cBConv, err = contact.ResolveUser(ctx, tx, w.tenant, chB.ID, "user:b-conv", "b-conv", contact.Profile{Name: "BrandB Active"}); err != nil {
			return err
		}
		cUnassigned, _, err = contact.ResolveIdentity(ctx, tx, w.tenant, w.channel.ID, "visitor:u-1", contact.Profile{Name: "Unassigned Lead"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{})
	recA, err := receive.Handle(ctx, w.system, conversation.ReceiveParams{ChannelID: chA.ID, From: "user:a-conv", Text: "hello from brand A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receive.Handle(ctx, w.system, conversation.ReceiveParams{ChannelID: chB.ID, From: "user:b-conv", Text: "hello from brand B"}); err != nil {
		t.Fatal(err)
	}
	recUnassigned, err := receive.Handle(ctx, w.system, conversation.ReceiveParams{ChannelID: w.channel.ID, From: "visitor:u-1", Text: "hello unassigned"})
	if err != nil {
		t.Fatal(err)
	}

	list := contact.NewList(env.DB)
	summary := contact.NewGetSummary(env.DB)
	get := contact.NewGet(env.DB)
	upd := contact.NewUpdate(env.DB)
	idsOf := func(sub identity.Subject, in *contact.ListInput) map[string]int {
		t.Helper()
		if in == nil {
			in = &contact.ListInput{CursorParams: page.CursorParams{PerPage: 50}}
		}
		res, err := list.Handle(ctx, sub, in)
		if err != nil {
			t.Fatal(err)
		}
		m := make(map[string]int, len(res.Items))
		for _, it := range res.Items {
			m[it.ID] = it.ConversationCount
		}
		return m
	}

	// 1. ScopeAll 与系统主体可以看到全部 5 个联系人和 3 个渠道
	for _, sub := range []identity.Subject{owner, w.system} {
		if got := idsOf(sub, nil); len(got) != 5 {
			t.Fatalf("%s 应看到全部 5 个联系人，实际 %d", sub.Type, len(got))
		}
		if sum, err := summary.Handle(ctx, sub); err != nil || sum.All != 5 || sum.Users != 3 || sum.Leads != 2 || len(sum.Channels) != 3 {
			t.Fatalf("%s 概览不对：%+v %v", sub.Type, sum, err)
		}
	}

	// 2. 品牌 B 的张三（ScopeTeam）只能看到品牌 B 的 2 个联系人（含未发消息的线索）和品牌 B 渠道，看不到品牌 A 和未绑定渠道
	bVisible := idsOf(agentB, nil)
	if len(bVisible) != 2 || bVisible[cBOnly.ID] != 0 || bVisible[cBConv.ID] != 1 {
		t.Fatalf("张三看到的联系人不对：%v", bVisible)
	}
	if sum, err := summary.Handle(ctx, agentB); err != nil || sum.All != 2 || sum.Users != 1 || sum.Leads != 1 || len(sum.Channels) != 1 || sum.Channels[0].ID != chB.ID {
		t.Fatalf("张三概览不对：%+v %v", sum, err)
	}
	// 张三即使用品牌 A 的渠道 ID 或关键词搜索也搜不到品牌 A 联系人
	if got := idsOf(agentB, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, ChannelID: chA.ID}); len(got) != 0 {
		t.Fatalf("张三按品牌 A 渠道筛选不应看到联系人：%v", got)
	}
	if got := idsOf(agentB, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "BrandA"}); len(got) != 0 {
		t.Fatalf("张三搜索 BrandA 不应看到联系人：%v", got)
	}
	// 张三读取或修改品牌 A 联系人都返回 ErrNotFound；读取和修改品牌 B 联系人正常
	for _, forbiddenID := range []string{cAOnly.ID, cAConv.ID, cUnassigned.ID} {
		if _, err := get.Handle(ctx, agentB, forbiddenID); !errors.Is(err, contact.ErrNotFound) {
			t.Errorf("张三读取无权联系人 %s：期望 ErrNotFound，实际 %v", forbiddenID, err)
		}
		if _, err := upd.Handle(ctx, agentB, forbiddenID, contact.UpdateBody{Notes: strPtr("越权备注")}); !errors.Is(err, contact.ErrNotFound) {
			t.Errorf("张三修改无权联系人 %s：期望 ErrNotFound，实际 %v", forbiddenID, err)
		}
	}
	if _, err := get.Handle(ctx, agentB, cBOnly.ID); err != nil {
		t.Fatalf("张三读取品牌 B 联系人：%v", err)
	}
	if _, err := upd.Handle(ctx, agentB, cBOnly.ID, contact.UpdateBody{Notes: strPtr("品牌 B 备注")}); err != nil {
		t.Fatalf("张三修改品牌 B 联系人：%v", err)
	}

	// 3. 品牌 A 坐席只能看到品牌 A 的 2 个联系人和品牌 A 渠道
	aVisible := idsOf(agentA, nil)
	if len(aVisible) != 2 || aVisible[cAOnly.ID] != 0 || aVisible[cAConv.ID] != 1 {
		t.Fatalf("品牌 A 坐席看到的联系人不对：%v", aVisible)
	}
	if sum, err := summary.Handle(ctx, agentA); err != nil || sum.All != 2 || len(sum.Channels) != 1 || sum.Channels[0].ID != chA.ID {
		t.Fatalf("品牌 A 坐席概览不对：%+v %v", sum, err)
	}

	// 4. ScopeAssigned（即使在 teamB 中）和无团队的 ScopeTeam 初始都看不到任何联系人和渠道
	for _, sub := range []identity.Subject{assignedOnly, homeless} {
		if got := idsOf(sub, nil); len(got) != 0 {
			t.Fatalf("%s 初始不应看到联系人：%v", sub.ID, got)
		}
		if sum, err := summary.Handle(ctx, sub); err != nil || sum.All != 0 || len(sum.Channels) != 0 {
			t.Fatalf("%s 初始概览不对：%+v %v", sub.ID, sum, err)
		}
		if _, err := get.Handle(ctx, sub, cBConv.ID); !errors.Is(err, contact.ErrNotFound) {
			t.Errorf("%s 读取品牌 B 联系人期望 ErrNotFound，实际 %v", sub.ID, err)
		}
	}

	// 5. 指派给 ScopeAssigned：把品牌 A 活跃联系人的一个会话指派给 assignedOnly，再为该联系人建第二个仅 teamA 可见的会话
	assign := conversation.NewAssign(env.DB, conversation.Downstream{})
	if _, err := assign.Handle(ctx, owner, recA.ConversationID, conversation.Assignment{AssigneeID: &assignedOnly.ID}); err != nil {
		t.Fatal(err)
	}
	secondConv := &conversation.Conversation{
		ID: "01950000-0000-7000-8000-000000000002", TenantID: w.tenant, Kind: conversation.Service,
		ChannelID: &chA.ID, ContactID: &cAConv.ID, TeamID: &teamA.ID,
		Status: conversation.Open, Priority: conversation.Normal,
		OpenedAt: time.Now().UTC(), LastMessageAt: time.Now().UTC(), Attributes: map[string]any{},
	}
	if _, err := env.DB.NewInsert().Model(secondConv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if d, err := get.Handle(ctx, owner, cAConv.ID); err != nil || d.ConversationCount != 2 {
		t.Fatalf("owner 看到的 cAConv 会话数应为 2：%+v %v", d, err)
	}
	if d, err := get.Handle(ctx, assignedOnly, cAConv.ID); err != nil || d.ConversationCount != 1 {
		t.Fatalf("assignedOnly 看到的 cAConv 会话数应为 1：%+v %v", d, err)
	}
	if sum, err := summary.Handle(ctx, assignedOnly); err != nil || sum.All != 1 || len(sum.Channels) != 1 || sum.Channels[0].ID != chA.ID {
		t.Fatalf("assignedOnly 概览不对：%+v %v", sum, err)
	}

	// 6. 内部备注 @ 协作：在未绑定渠道的会话中 @ 张三，张三可以看到 cUnassigned 和该渠道
	if _, err := conversation.NewAddNote(env.DB, conversation.Downstream{}).Handle(ctx, owner, recUnassigned.ConversationID, "请张三协助", []string{agentB.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := get.Handle(ctx, agentB, cUnassigned.ID); err != nil {
		t.Fatalf("被 @ 后张三应能看到 cUnassigned：%v", err)
	}

	// 7. 跨团队转派：给 cAOnly 建一条会话并转派给 teamB 后，张三可以看到 cAOnly
	recAOnly, err := receive.Handle(ctx, w.system, conversation.ReceiveParams{ChannelID: chA.ID, From: "user:a-only", Text: "need brand B help"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assign.Handle(ctx, owner, recAOnly.ConversationID, conversation.Assignment{TeamID: &teamB.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := get.Handle(ctx, agentB, cAOnly.ID); err != nil {
		t.Fatalf("会话转派到 teamB 后张三应能看到 cAOnly：%v", err)
	}
}

// TestContactNumberAndSearch 检查联系人在工作区内的递增编号：按工作区独立从 1 开始递增，
// 同时暴露在 Summary/ContactDTO 中，并支持按 2、#2、访客 #2、Visitor #2 搜索命中。
func TestContactNumberAndSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setupWorkspace(t, env, "alpha")
	beta := setupWorkspace(t, env, "beta")

	var lead1, user2, lead3, beta1 *contact.Contact
	err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if lead1, _, err = contact.ResolveIdentity(ctx, tx, alpha.tenant, alpha.channel.ID, "visitor:1", contact.Profile{}); err != nil {
			return err
		}
		if user2, err = contact.ResolveUser(ctx, tx, alpha.tenant, alpha.channel.ID, "user:u2", "u2", contact.Profile{Name: "Alice"}); err != nil {
			return err
		}
		if lead3, _, err = contact.ResolveIdentity(ctx, tx, alpha.tenant, alpha.channel.ID, "visitor:3", contact.Profile{}); err != nil {
			return err
		}
		beta1, _, err = contact.ResolveIdentity(ctx, tx, beta.tenant, beta.channel.ID, "visitor:b1", contact.Profile{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if lead1.Number != 1 || user2.Number != 2 || lead3.Number != 3 {
		t.Fatalf("alpha 联系人编号应依次为 1,2,3，实际 %d,%d,%d", lead1.Number, user2.Number, lead3.Number)
	}
	if beta1.Number != 1 {
		t.Fatalf("beta 联系人编号应独立从 1 开始，实际 %d", beta1.Number)
	}
	if lead1.Summary().Number != 1 {
		t.Fatalf("Summary 应包含编号 1：%+v", lead1.Summary())
	}

	// 按编号搜索支持多种常见写法
	list := contact.NewList(env.DB)
	for _, kw := range []string{"3", "#3", "访客 #3", "访客#3", "Visitor #3", "visitor #3"} {
		res, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Q: kw})
		if err != nil || len(res.Items) != 1 || res.Items[0].ID != lead3.ID || res.Items[0].Number != 3 {
			t.Errorf("关键词 %q 期望命中 #3：%+v %v", kw, res, err)
		}
	}
	// 无效编号写法不会报错也不误匹配
	res, err := list.Handle(ctx, alpha.agent, &contact.ListInput{CursorParams: page.CursorParams{PerPage: 20}, Q: "#0"})
	if err != nil || len(res.Items) != 0 {
		t.Errorf("#0 不应命中：%+v %v", res, err)
	}
}
