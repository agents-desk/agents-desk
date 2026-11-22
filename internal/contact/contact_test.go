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
	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
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
	// 按类型筛选
	users, err := list.Handle(ctx, alpha.agent, &contact.ListInput{OffsetParams: page.OffsetParams{Page: 1, PerPage: 20}, Kind: contact.User})
	if err != nil || users.Total != 1 || users.Items[0].ID != userA.ID || users.Items[0].ConversationCount != 1 {
		t.Fatalf("按用户筛选：%+v %v", users, err)
	}
	leads, err := list.Handle(ctx, alpha.agent, &contact.ListInput{OffsetParams: page.OffsetParams{Page: 1, PerPage: 20}, Kind: contact.Lead})
	if err != nil || leads.Total != 1 || leads.Items[0].ID != leadA.ID {
		t.Fatalf("按线索筛选：%+v %v", leads, err)
	}
	// 关键词搜名字、邮箱、外部编号
	for _, kw := range []string{"rahul", "rahul@example.com", "u1"} {
		res, err := list.Handle(ctx, alpha.agent, &contact.ListInput{OffsetParams: page.OffsetParams{Page: 1, PerPage: 20}, Q: kw})
		if err != nil || res.Total != 1 || res.Items[0].ID != userA.ID {
			t.Errorf("关键词 %q：%+v %v", kw, res, err)
		}
	}
	// 按语言筛选
	byLang, err := list.Handle(ctx, alpha.agent, &contact.ListInput{OffsetParams: page.OffsetParams{Page: 1, PerPage: 20}, Language: "hi-latn"})
	if err != nil || byLang.Total != 1 || byLang.Items[0].ID != userA.ID {
		t.Fatalf("按语言筛选：%+v %v", byLang, err)
	}
	// 按渠道筛选
	byCh, err := list.Handle(ctx, alpha.agent, &contact.ListInput{OffsetParams: page.OffsetParams{Page: 1, PerPage: 20}, ChannelID: ch2.ID})
	if err != nil || byCh.Total != 1 || byCh.Items[0].ID != leadA.ID {
		t.Fatalf("按渠道筛选：%+v %v", byCh, err)
	}
	// 跨工作区隔离：beta 看不到 alpha 的联系人
	betaRes, err := list.Handle(ctx, beta.agent, &contact.ListInput{OffsetParams: page.OffsetParams{Page: 1, PerPage: 20}, Q: "Rahul"})
	if err != nil || betaRes.Total != 0 {
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
