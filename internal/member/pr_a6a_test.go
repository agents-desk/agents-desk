package member_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestHeartbeatAndExpireIdleOnline 验证坐席心跳仅在在线时刷新活跃时间，且超时无心跳会自动置为离开并保留转派配置。
func TestHeartbeatAndExpireIdleOnline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Heartbeat", Slug: "heartbeat", OwnerEmail: "owner@hb.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	freshMember, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, sys, member.AddParams{
		Email: "fresh@hb.test", Name: "Fresh", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	subOwner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	subFresh := identity.Subject{Type: member.Type, ID: freshMember.ID, Tenant: tn.ID}
	setStatus := member.NewSetMyStatus(env.DB)
	hb := member.NewHeartbeat(env.DB)

	if _, err := setStatus.Handle(ctx, subOwner, member.StatusSetting{Status: member.Online, Reassign: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := setStatus.Handle(ctx, subFresh, member.StatusSetting{Status: member.Online, Reassign: false}); err != nil {
		t.Fatal(err)
	}

	staleAt := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := env.DB.ExecContext(ctx, "UPDATE member_statuses SET last_seen_at = ?, updated_at = ? WHERE tenant_id = ? AND member_id = ?", staleAt, staleAt, tn.ID, tn.OwnerID); err != nil {
		t.Fatal(err)
	}

	// 心跳刷新在线成员的 last_seen_at
	if err := hb.Handle(ctx, subOwner); err != nil {
		t.Fatal(err)
	}
	var refreshed time.Time
	if err := env.DB.NewRaw("SELECT last_seen_at FROM member_statuses WHERE tenant_id = ? AND member_id = ?", tn.ID, tn.OwnerID).Scan(ctx, &refreshed); err != nil {
		t.Fatal(err)
	}
	if !refreshed.After(staleAt) {
		t.Fatalf("心跳未刷新 last_seen_at：got=%v stale=%v", refreshed, staleAt)
	}

	// 离开状态发心跳不能把状态改回在线
	if _, err := setStatus.Handle(ctx, subOwner, member.StatusSetting{Status: member.Away, Reassign: true}); err != nil {
		t.Fatal(err)
	}
	if err := hb.Handle(ctx, subOwner); err != nil {
		t.Fatal(err)
	}
	statuses, err := member.StatusesOf(ctx, env.DB, tn.ID, []string{tn.OwnerID, freshMember.ID})
	if err != nil {
		t.Fatal(err)
	}
	if statuses[tn.OwnerID] != member.Away || statuses[freshMember.ID] != member.Online {
		t.Fatalf("状态异常：%+v", statuses)
	}

	// 重新切回在线并超时，验证 ExpireIdleOnline 将其置为离开并保留 reassign=true，同时不误伤近期有心跳的成员
	if _, err := setStatus.Handle(ctx, subOwner, member.StatusSetting{Status: member.Online, Reassign: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.ExecContext(ctx, "UPDATE member_statuses SET last_seen_at = ?, updated_at = ? WHERE tenant_id = ? AND member_id = ?", staleAt, staleAt, tn.ID, tn.OwnerID); err != nil {
		t.Fatal(err)
	}

	var notified []string
	expired, err := member.ExpireIdleOnline(ctx, env.DB, 5*time.Minute, 100, func(_ context.Context, tenantID, memberID string) error {
		notified = append(notified, tenantID+":"+memberID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 {
		t.Fatalf("应过期 1 名成员，实际 %d", expired)
	}
	if len(notified) != 1 || notified[0] != tn.ID+":"+tn.OwnerID {
		t.Fatalf("通知回调不符：%v", notified)
	}

	var row struct {
		Status   member.Status `bun:"status"`
		Reassign bool          `bun:"reassign"`
	}
	if err := env.DB.NewRaw("SELECT status, reassign FROM member_statuses WHERE tenant_id = ? AND member_id = ?", tn.ID, tn.OwnerID).Scan(ctx, &row); err != nil {
		t.Fatal(err)
	}
	if row.Status != member.Away || !row.Reassign {
		t.Fatalf("自动离开后应保持 reassign=true：%+v", row)
	}
	statuses, err = member.StatusesOf(ctx, env.DB, tn.ID, []string{tn.OwnerID, freshMember.ID})
	if err != nil {
		t.Fatal(err)
	}
	if statuses[freshMember.ID] != member.Online {
		t.Fatalf("近期活跃成员不应被自动离开：%+v", statuses)
	}
}
