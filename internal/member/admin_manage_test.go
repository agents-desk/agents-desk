package member_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/role"
	"agents-desk/internal/tenant"
)

// TestAdminMemberManagement 验证管理员端全局成员列表（搜姓名与邮箱、统计工作区数、状态筛选）与启停（令牌撤销、解析拦截、平台审计）。
func TestAdminMemberManagement(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	creds := broadcast.NewCredentials(hub)

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	tn1, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "gm-a", Slug: "gm-a", OwnerEmail: "owner1@gm.com", OwnerName: "主管理一", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	tn2, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "gm-b", Slug: "gm-b", OwnerEmail: "owner2@gm.com", OwnerName: "主管理二", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 将主管理一再加入第二个工作区以验证 tenant_count == 2
	owner2Sub := identity.Subject{
		Type: member.Type, ID: tn2.OwnerID, Tenant: tn2.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	role2, err := role.NewCreate(env.DB, env.Roles).Handle(ctx, owner2Sub, role.Params{Name: "协作员", ConversationScope: auth.ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, owner2Sub, member.AddParams{
		Email:  "owner1@gm.com",
		RoleID: role2.ID,
	}); err != nil {
		t.Fatal(err)
	}

	adminList := member.NewAdminList(env.DB, env.Roles, env.MemberLogins)
	adminGet := member.NewAdminGet(env.DB, env.Roles, env.MemberLogins)
	disableAct := member.NewAdminDisable(env.DB, env.Roles, env.MemberLogins, creds)
	enableAct := member.NewAdminEnable(env.DB, env.Roles, env.MemberLogins)

	// 按邮箱关键词搜索主管理一
	found, err := adminList.Handle(ctx, &member.AdminListInput{Q: "owner1@gm.com"})
	if err != nil || len(found.Items) != 1 || found.Items[0].ID != tn1.OwnerID || found.Items[0].TenantCount != 2 || found.Items[0].Status != member.AccountActive {
		t.Fatalf("按邮箱搜索结果异常：%+v err=%v", found, err)
	}

	// 签发成员令牌后全局停用：令牌立即被吊销，Resolver 返回 ErrAccountDisabled
	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour})
	tok, err := tokens.Login(ctx, identity.Subject{Type: member.Type, ID: tn1.OwnerID}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disableAct.Handle(ctx, apptest.Admin, tn1.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Validate(ctx, tok); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("停用成员后令牌应立即失效，实际 %v", err)
	}
	if _, err := member.Resolve(env.DB, env.Roles)(ctx, identity.Subject{Type: member.Type, ID: tn1.OwnerID, Tenant: tn1.ID}); !errors.Is(err, member.ErrAccountDisabled) {
		t.Fatalf("Resolve 应拒绝已停用成员，实际 %v", err)
	}

	disabledList, err := adminList.Handle(ctx, &member.AdminListInput{Status: member.AccountDisabled})
	if err != nil || len(disabledList.Items) != 1 || disabledList.Items[0].ID != tn1.OwnerID {
		t.Fatalf("筛选已停用成员异常：%+v err=%v", disabledList, err)
	}

	// 重新启用
	if _, err := enableAct.Handle(ctx, apptest.Admin, tn1.OwnerID); err != nil {
		t.Fatal(err)
	}
	got, err := adminGet.Handle(ctx, tn1.OwnerID)
	if err != nil || got.Status != member.AccountActive || got.DisabledAt != nil {
		t.Fatalf("重新启用后状态异常：%+v err=%v", got, err)
	}

	// 确认平台审计日志中写入停用与启用记录
	var count int
	if err := env.DB.NewRaw("SELECT COUNT(*) FROM activity_log WHERE tenant = '' AND target_type = 'member' AND target_id = ? AND event IN ('member.disabled', 'member.enabled')", tn1.OwnerID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("期望写入 2 条平台审计日志，实际 %d", count)
	}
}
