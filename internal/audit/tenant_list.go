package audit

import (
	"context"
	"maps"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/i18n"
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
	db     *bun.DB
	loader *List
}

// NewTenantList 返回工作区操作日志查询动作。
func NewTenantList(db *bun.DB) *TenantList {
	return &TenantList{db: db, loader: NewList(db)}
}

// Handle 按当前工作区与筛选条件分页查询操作日志，并补全操作人与工作区业务对象名称。
func (a *TenantList) Handle(ctx context.Context, sub identity.Subject, in TenantListInput) (page.Offset[Item], error) {
	res, err := activity.List(ctx, a.db, activity.Query{
		Tenant:     sub.Tenant,
		TargetType: in.TargetType,
		Page:       in.OffsetParams,
	})
	if err != nil {
		return page.Offset[Item]{}, err
	}
	actors, err := a.loader.loadActorNames(ctx, res.Items)
	if err != nil {
		return page.Offset[Item]{}, err
	}
	targets, err := a.loadTenantTargetNames(ctx, sub.Tenant, res.Items)
	if err != nil {
		return page.Offset[Item]{}, err
	}
	items := make([]Item, 0, len(res.Items))
	for _, r := range res.Items {
		props := maps.Clone(r.Properties)
		if props == nil {
			props = map[string]any{}
		}
		if _, ok := props["name"]; !ok {
			if name := targets[targetKey{r.TargetType, r.TargetID}]; name != "" {
				props["name"] = name
			} else if r.TargetID != "" {
				props["name"] = r.TargetID
			}
		}
		if ch, ok := props["changed"].([]any); ok {
			parts := make([]string, 0, len(ch))
			for _, v := range ch {
				if s, ok := v.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			props["changed"] = strings.Join(parts, ", ")
		}
		actorName := actors[r.Actor()]
		if actorName == "" && r.Actor().Type == identity.System {
			actorName = i18n.T(ctx, "activity.actor.system", nil)
		}
		items = append(items, Item{
			ID:           r.ID.String(),
			Event:        r.Event,
			TargetType:   r.TargetType,
			TargetID:     r.TargetID,
			Text:         i18n.T(ctx, "activity."+r.Event, props),
			Actor:        actorName,
			Impersonator: actors[r.Impersonator()],
			CreatedAt:    r.CreatedAt,
		})
	}
	return page.NewOffset(items, in.OffsetParams, res.Total), nil
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
func (a *TenantList) loadTenantTargetNames(ctx context.Context, tenantID string, rows []activity.Activity) (map[targetKey]string, error) {
	idsByKind := map[string][]string{}
	for _, r := range rows {
		if r.TargetID != "" {
			idsByKind[r.TargetType] = append(idsByKind[r.TargetType], r.TargetID)
		}
	}
	names := map[targetKey]string{}
	type lookupSpec struct {
		kind     string
		table    string
		col      string
		scopedBy string
	}
	specs := []lookupSpec{
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
	}
	for _, sp := range specs {
		ids := idsByKind[sp.kind]
		if len(ids) == 0 {
			continue
		}
		var list []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		q := a.db.NewSelect().Table(sp.table).Column("id").ColumnExpr(sp.col+" AS name").Where("id IN (?)", bun.List(ids))
		if sp.scopedBy != "" {
			q = q.Where(sp.scopedBy+" = ?", tenantID)
		}
		if err := q.Scan(ctx, &list); err != nil {
			return nil, errors.Wrapf(err, "load audit target %s names", sp.kind)
		}
		for _, item := range list {
			names[targetKey{sp.kind, item.ID}] = item.Name
		}
	}
	return names, nil
}
