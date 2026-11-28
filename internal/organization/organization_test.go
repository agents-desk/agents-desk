package organization_test

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
	"agents-desk/internal/extgroup"
	"agents-desk/internal/member"
	"agents-desk/internal/organization"
	"agents-desk/internal/tenant"
)

// TestOrganizationLifecycleAssociationsAndIsolation 验证客户组织的 CRUD、同名/同外部编号唯一性、
// 与联系人及外部群外键关联、匿名化清理、合并继承、删除自动置空及跨工作区隔离。
func TestOrganizationLifecycleAssociationsAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "OrgAlpha", Slug: "org-alpha", OwnerEmail: "owner@oa.test", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "OrgBeta", Slug: "org-beta", OwnerEmail: "owner@ob.test", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	perms := auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values()))
	subAlpha := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: perms}
	subBeta := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: perms}
	sysAlpha := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}

	createOrg := organization.NewCreate(env.DB)
	updateOrg := organization.NewUpdate(env.DB)
	listOrg := organization.NewList(env.DB)
	getOrg := organization.NewGet(env.DB)
	delOrg := organization.NewDelete(env.DB)

	// 1. 新建组织（自动规范化域名）与同名 / 同外部编号冲突校验
	acme, err := createOrg.Handle(ctx, subAlpha, organization.CreateBody{
		Name:       "Acme Corp",
		Domain:     "https://Acme.com/about",
		ExternalID: "crm-001",
		Notes:      "年度企业客户",
	})
	if err != nil {
		t.Fatal(err)
	}
	if acme.Domain != "acme.com" || acme.ExternalID != "crm-001" {
		t.Fatalf("域名规范化或字段保存异常: %+v", acme)
	}
	if _, err := createOrg.Handle(ctx, subAlpha, organization.CreateBody{Name: "acme corp"}); !errors.Is(err, organization.ErrNameTaken) {
		t.Fatalf("不区分大小写同名应返回 ErrNameTaken, 实际 %v", err)
	}
	if _, err := createOrg.Handle(ctx, subAlpha, organization.CreateBody{Name: "Other", ExternalID: "crm-001"}); !errors.Is(err, organization.ErrExternalIDTaken) {
		t.Fatalf("同工作区外部编号冲突应返回 ErrExternalIDTaken, 实际 %v", err)
	}
	// Beta 工作区可使用相同名称与外部编号
	betaOrg, err := createOrg.Handle(ctx, subBeta, organization.CreateBody{
		Name: "Acme Corp", ExternalID: "crm-001",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. 跨工作区读写与删除拦截
	if _, err := getOrg.Handle(ctx, subBeta, acme.ID); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("跨工作区读取组织应返回 ErrNotFound, 实际 %v", err)
	}
	if _, err := updateOrg.Handle(ctx, subBeta, acme.ID, organization.UpdateBody{Notes: strPtr("越权")}); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("跨工作区更新组织应返回 ErrNotFound, 实际 %v", err)
	}
	if err := delOrg.Handle(ctx, subBeta, acme.ID); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("跨工作区删除组织应返回 ErrNotFound, 实际 %v", err)
	}

	// 3. 关联到外部群和联系人（验证跨工作区校验 + DTO 概要 + 筛选 + 聚合计数）
	chA, err := channel.NewCreate(env.DB).Handle(ctx, sysAlpha, channel.CreateParams{
		Kind: channel.WeCom, Audience: channel.AudiencePartner, Name: "商务企微",
	})
	if err != nil {
		t.Fatal(err)
	}
	grp, _, err := extgroup.EnsureGroup(ctx, env.DB, alpha.ID, chA.ID, "chat-acme-1", "Acme 对接群", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := extgroup.NewUpdate(env.DB).Handle(ctx, subAlpha, grp.ID, extgroup.UpdateBody{OrganizationID: &betaOrg.ID}); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("外部群绑定其他工作区组织应返回 ErrNotFound, 实际 %v", err)
	}
	updatedGrp, err := extgroup.NewUpdate(env.DB).Handle(ctx, subAlpha, grp.ID, extgroup.UpdateBody{OrganizationID: &acme.ID})
	if err != nil || updatedGrp.Organization == nil || updatedGrp.Organization.Name != "Acme Corp" {
		t.Fatalf("外部群绑定组织并填充 DTO 失败: %+v err=%v", updatedGrp, err)
	}
	filteredGrps, err := extgroup.NewList(env.DB).Handle(ctx, subAlpha, &extgroup.ListInput{OrganizationID: acme.ID})
	if err != nil || len(filteredGrps) != 1 || filteredGrps[0].ID != grp.ID {
		t.Fatalf("按组织筛选外部群失败: %+v err=%v", filteredGrps, err)
	}

	var ctTarget, ctSource, ctAnon *contact.Contact
	if err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		ctTarget, _, err = contact.ResolveIdentity(ctx, tx, alpha.ID, chA.ID, "ext-t", contact.Profile{Name: "Alice"})
		if err != nil {
			return err
		}
		ctSource, _, err = contact.ResolveIdentity(ctx, tx, alpha.ID, chA.ID, "ext-s", contact.Profile{Name: "Alice-Alt"})
		if err != nil {
			return err
		}
		ctAnon, _, err = contact.ResolveIdentity(ctx, tx, alpha.ID, chA.ID, "ext-a", contact.Profile{Name: "Anon"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	ctUpd := contact.NewUpdate(env.DB, env.Roles)
	if _, err := ctUpd.Handle(ctx, subAlpha, ctTarget.ID, contact.UpdateBody{OrganizationID: &betaOrg.ID}); !errors.Is(err, organization.ErrNotFound) {
		t.Fatalf("联系人绑定其他工作区组织应返回 ErrNotFound, 实际 %v", err)
	}
	gotSrc, err := ctUpd.Handle(ctx, subAlpha, ctSource.ID, contact.UpdateBody{OrganizationID: &acme.ID})
	if err != nil || gotSrc.Organization == nil || gotSrc.Organization.ID != acme.ID {
		t.Fatalf("联系人绑定组织失败: %+v err=%v", gotSrc, err)
	}
	if _, err := ctUpd.Handle(ctx, subAlpha, ctAnon.ID, contact.UpdateBody{OrganizationID: &acme.ID}); err != nil {
		t.Fatal(err)
	}

	// 验证组织的 ContactCount == 2, ExternalGroupCount == 1，支持模糊搜索
	pageRes, err := listOrg.Handle(ctx, subAlpha, &organization.ListInput{Q: "acme.com"})
	if err != nil || len(pageRes.Items) != 1 || pageRes.Items[0].ContactCount != 2 || pageRes.Items[0].ExternalGroupCount != 1 {
		t.Fatalf("组织列表搜索或计数异常: %+v err=%v", pageRes, err)
	}
	// 按组织筛选联系人
	ctList, err := contact.NewList(env.DB).Handle(ctx, subAlpha, &contact.ListInput{
		CursorParams:   page.CursorParams{PerPage: 20},
		OrganizationID: acme.ID,
	})
	if err != nil || len(ctList.Items) != 2 {
		t.Fatalf("按组织筛选联系人结果异常: %+v err=%v", ctList, err)
	}

	// 4. 合并联系人时目标无组织则继承来源的 organization_id
	merged, err := contact.NewMerge(env.DB).Handle(ctx, subAlpha, ctTarget.ID, contact.MergeBody{SourceID: ctSource.ID})
	if err != nil || merged.OrganizationID == nil || *merged.OrganizationID != acme.ID {
		t.Fatalf("联系人合并应继承来源组织: %+v err=%v", merged, err)
	}

	// 5. 匿名化联系人时清空 organization_id
	if _, err := env.DB.NewRaw(`INSERT INTO ticket_targets (id, created_at, updated_at, tenant_id, ticket_id, contact_id)
		SELECT gen_random_uuid(), ?, ?, ?, id, ? FROM conversations LIMIT 0`, time.Now().UTC(), time.Now().UTC(), alpha.ID, ctAnon.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 为 ctAnon 造一条会话触发匿名化路径
	if _, err := env.DB.NewRaw(`INSERT INTO conversations (id, created_at, updated_at, tenant_id, kind, audience, contact_id, status, priority, opened_at, last_message_at, last_seq)
		VALUES (gen_random_uuid(), ?, ?, ?, 'service', 'partner', ?, 'open', 'normal', ?, ?, 0)`,
		time.Now().UTC(), time.Now().UTC(), alpha.ID, ctAnon.ID, time.Now().UTC(), time.Now().UTC()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour})
	delRes, err := contact.NewDelete(env.DB, tokens).Handle(ctx, subAlpha, ctAnon.ID)
	if err != nil || delRes.Mode != "anonymized" {
		t.Fatalf("ctAnon 应走匿名化: %+v err=%v", delRes, err)
	}
	anonRow, err := contact.Find(ctx, env.DB, alpha.ID, ctAnon.ID)
	if err != nil || anonRow.OrganizationID != nil {
		t.Fatalf("匿名化后应清空 organization_id: %+v err=%v", anonRow, err)
	}

	// 6. 删除组织后外键自动将联系人与外部群 organization_id 置为 NULL
	if err := delOrg.Handle(ctx, subAlpha, acme.ID); err != nil {
		t.Fatal(err)
	}
	afterDelContact, err := contact.Find(ctx, env.DB, alpha.ID, ctTarget.ID)
	if err != nil || afterDelContact.OrganizationID != nil {
		t.Fatalf("删除组织后联系人 organization_id 应置为 nil: %+v err=%v", afterDelContact, err)
	}
	afterDelGroup, err := extgroup.Find(ctx, env.DB, alpha.ID, grp.ID)
	if err != nil || afterDelGroup.OrganizationID != nil {
		t.Fatalf("删除组织后外部群 organization_id 应置为 nil: %+v err=%v", afterDelGroup, err)
	}
}

func strPtr(s string) *string { return &s }
