package extgroup_test

import (
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/extgroup"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

func TestExternalGroupLifecycleAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner@alpha.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta", Slug: "beta", OwnerEmail: "owner@beta.test", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerAlpha := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID}
	ownerBeta := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID}

	chAlpha, err := channel.NewCreate(env.DB).Handle(ctx, ownerAlpha, channel.CreateParams{
		Kind: channel.WeCom, Audience: channel.AudienceInternal, Name: "IT 企微机器人",
	})
	if err != nil {
		t.Fatal(err)
	}
	teamIT, err := team.NewCreate(env.DB).Handle(ctx, ownerAlpha, team.Changes{Name: "IT 支持"})
	if err != nil {
		t.Fatal(err)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	g, created, err := extgroup.EnsureGroup(ctx, env.DB, alpha.ID, chAlpha.ID, "wr-chat-1", "研发大群", at)
	if err != nil || !created {
		t.Fatalf("首次 EnsureGroup 失败: %+v created=%v err=%v", g, created, err)
	}
	if g.Status != extgroup.StatusPendingSetup || g.Name != "研发大群" {
		t.Fatalf("新建群状态或名称不对: %+v", g)
	}

	// 再次调用更新群名与活跃时间，保持幂等
	g2, created2, err := extgroup.EnsureGroup(ctx, env.DB, alpha.ID, chAlpha.ID, "wr-chat-1", "研发技术大群", at.Add(time.Minute))
	if err != nil || created2 || g2.ID != g.ID || g2.Name != "研发技术大群" {
		t.Fatalf("重复 EnsureGroup 异常: %+v created=%v err=%v", g2, created2, err)
	}

	// 群内发言人记录独立于 contacts
	u1, err := extgroup.EnsureUser(ctx, env.DB, alpha.ID, chAlpha.ID, "zhangsan", "张三", "")
	if err != nil || u1.Name != "张三" {
		t.Fatalf("EnsureUser 失败: %+v %v", u1, err)
	}
	u2, err := extgroup.EnsureUser(ctx, env.DB, alpha.ID, chAlpha.ID, "zhangsan", "张三（研发）", "https://example.com/a.png")
	if err != nil || u2.ID != u1.ID || u2.Name != "张三（研发）" {
		t.Fatalf("重复 EnsureUser 更新异常: %+v %v", u2, err)
	}

	// 管理员启用并配置默认团队与语言
	activeStatus := extgroup.StatusActive
	zhLang := "zh-CN"
	updated, err := extgroup.NewUpdate(env.DB).Handle(ctx, ownerAlpha, g.ID, extgroup.UpdateBody{
		Status:   &activeStatus,
		TeamID:   &teamIT.ID,
		Language: &zhLang,
	})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if updated.Status != extgroup.StatusActive || updated.TeamID == nil || *updated.TeamID != teamIT.ID || updated.Language != "zh-CN" || !updated.LanguageManual {
		t.Fatalf("Update 结果不对: %+v", updated)
	}

	// 列表查询与跨工作区隔离
	list := extgroup.NewList(env.DB)
	items, err := list.Handle(ctx, ownerAlpha, &extgroup.ListInput{Status: extgroup.StatusActive})
	if err != nil || len(items) != 1 || items[0].ID != g.ID {
		t.Fatalf("List 结果不对: %+v %v", items, err)
	}
	betaItems, err := list.Handle(ctx, ownerBeta, &extgroup.ListInput{})
	if err != nil || len(betaItems) != 0 {
		t.Fatalf("其他工作区不应看到 Alpha 的外部群: %+v %v", betaItems, err)
	}
	if _, err := extgroup.NewUpdate(env.DB).Handle(ctx, ownerBeta, g.ID, extgroup.UpdateBody{Status: &activeStatus}); !errors.Is(err, extgroup.ErrNotFound) {
		t.Fatalf("其他工作区修改应返回 ErrNotFound: %v", err)
	}

	// 移出群后再重新入群恢复 pending_setup
	if err := extgroup.SyncRemoved(ctx, env.DB, alpha.ID, chAlpha.ID, "wr-chat-1"); err != nil {
		t.Fatal(err)
	}
	removed, err := extgroup.Find(ctx, env.DB, alpha.ID, g.ID)
	if err != nil || removed.Status != extgroup.StatusRemoved {
		t.Fatalf("移出后状态应为 removed: %+v %v", removed, err)
	}
	rejoined, err := extgroup.SyncJoined(ctx, env.DB, alpha.ID, chAlpha.ID, "wr-chat-1", "研发技术大群")
	if err != nil || rejoined.ID != g.ID || rejoined.Status != extgroup.StatusPendingSetup {
		t.Fatalf("重新拉回群应恢复 pending_setup: %+v %v", rejoined, err)
	}
}
