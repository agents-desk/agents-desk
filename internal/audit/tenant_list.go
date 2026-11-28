package audit

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/activity/activityapi"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
)

// TenantListInput 是工作区操作日志的查询参数。
type TenantListInput struct {
	page.OffsetParams
	TargetType string `query:"target_type" enum:",tenant,member,role,team,channel,contact,organization,blocked_ip,tag,attribute,ticket_type,macro,automation_rule,external_group,settings" doc:"按对象类型筛选；为空时返回全部"`
}

// TenantList 读取工作区成员与管理员在当前工作区（sub.Tenant）的操作日志。
type TenantList struct {
	svc *activityapi.Service
}

// NewTenantList 返回工作区操作日志查询动作。
func NewTenantList(db *bun.DB) *TenantList {
	svc := activityapi.New(db, activityapi.Options{
		ActorNames: func(ctx context.Context, refs []identity.Ref) (map[identity.Ref]string, error) {
			return loadActorNames(ctx, db, refs)
		},
		TargetNames: func(ctx context.Context, tenantID string, targets []activityapi.TargetRef) (map[activityapi.TargetRef]string, error) {
			return loadTenantTargetNames(ctx, db, tenantID, targets)
		},
	})
	return &TenantList{svc: svc}
}

// Handle 按当前工作区与筛选条件分页查询操作日志，并补全操作人与工作区业务对象名称。
func (a *TenantList) Handle(ctx context.Context, sub identity.Subject, in TenantListInput) (page.Offset[Item], error) {
	return a.svc.List(ctx, activity.Query{
		Tenant:     sub.Tenant,
		TargetType: in.TargetType,
		Page:       in.OffsetParams,
	})
}

// HTTP 处理 GET /t/{slug}/api/audit-logs。
func (a *TenantList) HTTP(ctx context.Context, in *TenantListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: res}, nil
}

// loadTenantTargetNames 批量加载工作区内部业务对象的显示名称。
func loadTenantTargetNames(ctx context.Context, db bun.IDB, tenantID string, targets []activityapi.TargetRef) (map[activityapi.TargetRef]string, error) {
	return lookupTargetNames(ctx, db, tenantID, targets, []targetLookupSpec{
		{kind: "tenant", table: "tenants", col: "name"},
		{kind: "member", table: "members", col: "name"},
		{kind: "role", table: "authz_roles", col: "name", scopedBy: "tenant"},
		{kind: "team", table: "teams", col: "name", scopedBy: "tenant_id"},
		{kind: "channel", table: "channels", col: "name", scopedBy: "tenant_id"},
		{kind: "contact", table: "contacts", col: "name", scopedBy: "tenant_id"},
		{kind: "organization", table: "organizations", col: "name", scopedBy: "tenant_id"},
		{kind: "tag", table: "tags", col: "name", scopedBy: "tenant_id"},
		{kind: "attribute", table: "attribute_definitions", col: "name", scopedBy: "tenant_id"},
		{kind: "ticket_type", table: "ticket_types", col: "name", scopedBy: "tenant_id"},
		{kind: "macro", table: "macros", col: "title", scopedBy: "tenant_id"},
		{kind: "external_group", table: "external_groups", col: "name", scopedBy: "tenant_id"},
		{kind: "blocked_ip", table: "blocked_ips", col: "cidr", scopedBy: "tenant_id"},
	})
}
