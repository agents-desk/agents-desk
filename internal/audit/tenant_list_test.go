package audit_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/audit"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
	"agents-desk/locales"
)

// TestTenantAuditListAndActivityLocales 验证工作区操作日志严格按工作区隔离、支持按对象类型筛选、解析操作人/代登人/目标名称，
// 并验证新增 activity.* 事件消息在简体中文和英文下均存在有效译文。
func TestTenantAuditListAndActivityLocales(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	bundle := i18n.MustNew("zh-CN", locales.FS)
	ctx := i18n.WithLocale(context.Background(), bundle, "zh-CN")

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "audit-a", Slug: "audit-a", OwnerEmail: "owner@audita.com", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "audit-b", Slug: "audit-b", OwnerEmail: "owner@auditb.com", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, identity.Subject{Type: identity.System, ID: "cli"}, "运维代登员", "ops-imp@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}

	alphaSub := identity.Subject{
		Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID,
		Impersonator: identity.Ref{Type: platformadmin.Type, ID: ops.ID},
		Permissions:  auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}
	betaSub := identity.Subject{
		Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID,
		Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values())),
	}

	tm, err := team.NewCreate(env.DB).Handle(ctx, alphaSub, team.Changes{Name: "售后团队", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, alphaSub, channel.CreateParams{Kind: channel.Test, Name: "售后渠道"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewCreate(env.DB).Handle(ctx, betaSub, team.Changes{Name: "别家团队", Assignment: team.Manual}); err != nil {
		t.Fatal(err)
	}

	tenantAudit := audit.NewTenantList(env.DB)
	allAlpha, err := tenantAudit.Handle(ctx, alphaSub, audit.TenantListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(allAlpha.Items) != 2 || allAlpha.Total != 2 {
		t.Fatalf("alpha 应只见到本工作区 2 条日志（不混入 beta 或平台日志），实际 %d: %+v", len(allAlpha.Items), allAlpha.Items)
	}
	if allAlpha.Items[0].Actor != "OwnerA" || allAlpha.Items[0].Impersonator != "运维代登员" {
		t.Fatalf("操作人与代登人解析异常：%+v", allAlpha.Items[0])
	}
	if !strings.Contains(allAlpha.Items[0].Text, "售后渠道") && !strings.Contains(allAlpha.Items[1].Text, "售后团队") {
		t.Fatalf("工作区对象名称未补全到译文：%+v", allAlpha.Items)
	}
	_ = tm
	_ = ch

	onlyTeam, err := tenantAudit.Handle(ctx, alphaSub, audit.TenantListInput{TargetType: "team"})
	if err != nil || len(onlyTeam.Items) != 1 || onlyTeam.Items[0].TargetType != "team" {
		t.Fatalf("按 team 筛选工作区审计异常：%+v err=%v", onlyTeam, err)
	}

	// 验证全部工作区与平台 activity.* 译文在 zh-CN 与 en 下均有效
	events := []string{
		"member.added", "member.removed", "member.role_changed", "member.status_changed",
		"member.preferences_updated", "member.profile_updated", "member.notifications_updated",
		"member.password_changed", "member.handed_over", "member.disabled", "member.enabled",
		"role.created", "role.updated", "role.deleted",
		"team.created", "team.updated", "team.deleted", "team.disabled", "team.enabled",
		"team.member_added", "team.member_removed",
		"channel.created", "channel.updated", "channel.archived", "channel.deleted",
		"channel.identity_secret_rotated", "channel.webhook_secret_rotated",
		"business_policy.updated", "business_channel.enabled_changed", "business_channel.collaborators_added",
		"contact.updated", "contact.deleted", "contact.anonymized",
		"contact_view.created", "contact_view.updated", "contact_view.deleted",
		"tag.created", "tag.updated", "tag.deleted",
		"attribute.created", "attribute.updated", "attribute.deleted", "attribute.reordered",
		"ticket_type.created", "ticket_type.updated", "ticket_type.deleted", "ticket_type.reordered",
		"ticket_type.disabled", "ticket_type.enabled",
		"macro.created", "macro.updated", "macro.deleted",
		"view.created", "view.updated", "view.deleted",
		"external_group.updated", "tenant_settings.changed", "translation_settings.updated",
		"assignment_settings.updated", "automation.updated", "conversation.merged",
	}
	props := map[string]any{
		"name": "测试对象", "role": "坐席", "from": "A", "to": "B", "status": "online",
		"changed": "name", "kind": "widget", "part": "policy", "builtin_key": "reply_time",
		"transferred_conversations": 2, "transferred_tickets": 1,
	}
	for _, locale := range []string{"zh-CN", "en"} {
		lctx := i18n.WithLocale(context.Background(), bundle, locale)
		for _, ev := range events {
			key := "activity." + ev
			txt := i18n.T(lctx, key, props)
			if txt == "" || txt == key {
				t.Fatalf("locale=%s 缺失或未翻译事件 %s -> %q", locale, key, txt)
			}
		}
	}
	_ = activity.Record
}
