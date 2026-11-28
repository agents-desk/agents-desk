// Package audit 提供平台与工作区操作日志的查询接口与对象名称解析。
package audit

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/activity/activityapi"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
)

// ListInput 是平台操作日志的查询参数。
type ListInput struct {
	page.OffsetParams
	TargetType string `query:"target_type" enum:",tenant,platform_admin,member,settings,feature" doc:"按对象类型筛选；为空时返回全部"`
}

// Item 是操作日志表格中的一条记录。
type Item = activityapi.Item

// ListOutput 是平台操作日志的响应（固定分页）。
type ListOutput struct {
	Body page.Offset[Item]
}

// List 读取平台域的操作日志。
type List struct {
	svc *activityapi.Service
}

// NewList 返回平台操作日志查询动作。
func NewList(db *bun.DB) *List {
	svc := activityapi.New(db, activityapi.Options{
		ActorNames: func(ctx context.Context, refs []identity.Ref) (map[identity.Ref]string, error) {
			return loadActorNames(ctx, db, refs)
		},
		TargetNames: func(ctx context.Context, _ string, targets []activityapi.TargetRef) (map[activityapi.TargetRef]string, error) {
			return loadPlatformTargetNames(ctx, db, targets)
		},
		TargetHref: targetHref,
	})
	return &List{svc: svc}
}

// Handle 按条件分页查询平台操作日志，解析操作人姓名和目标名称并翻译为当前语言的展示文字。
func (a *List) Handle(ctx context.Context, in ListInput) (page.Offset[Item], error) {
	return a.svc.List(ctx, activity.Query{
		Tenant:     "",
		ActorTypes: []identity.SubjectType{platformadmin.Type, identity.System},
		TargetType: in.TargetType,
		Page:       in.OffsetParams,
	})
}

// HTTP 处理 GET /audit。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	res, err := a.Handle(ctx, *in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: res}, nil
}

// loadActorNames 批量读取日志中出现的平台管理员与成员姓名。
func loadActorNames(ctx context.Context, db bun.IDB, refs []identity.Ref) (map[identity.Ref]string, error) {
	var adminIDs, memberIDs []string
	for _, ref := range refs {
		switch ref.Type {
		case platformadmin.Type:
			adminIDs = append(adminIDs, ref.ID)
		case member.Type:
			memberIDs = append(memberIDs, ref.ID)
		}
	}
	names := map[identity.Ref]string{}
	if len(adminIDs) > 0 {
		var list []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("platform_admins").Column("id", "name").Where("id IN (?)", bun.List(adminIDs)).Scan(ctx, &list); err != nil {
			return nil, errors.Wrap(err, "load platform admin names")
		}
		for _, item := range list {
			names[identity.Ref{Type: platformadmin.Type, ID: item.ID}] = item.Name
		}
	}
	if len(memberIDs) > 0 {
		var list []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := db.NewSelect().Table("members").Column("id", "name").Where("id IN (?)", bun.List(memberIDs)).Scan(ctx, &list); err != nil {
			return nil, errors.Wrap(err, "load member names")
		}
		for _, item := range list {
			names[identity.Ref{Type: member.Type, ID: item.ID}] = item.Name
		}
	}
	return names, nil
}

// targetLookupSpec 描述一种日志目标对象的名称查询表结构。
type targetLookupSpec struct {
	kind     string
	table    string
	col      string
	scopedBy string
}

// lookupTargetNames 按 targets 批量查询各类型目标对象的显示名称。
func lookupTargetNames(ctx context.Context, db bun.IDB, tenantID string, targets []activityapi.TargetRef, specs []targetLookupSpec) (map[activityapi.TargetRef]string, error) {
	idsByKind := map[string][]string{}
	for _, t := range targets {
		if t.ID != "" {
			idsByKind[t.Type] = append(idsByKind[t.Type], t.ID)
		}
	}
	names := map[activityapi.TargetRef]string{}
	for _, sp := range specs {
		ids := idsByKind[sp.kind]
		if len(ids) == 0 {
			continue
		}
		var list []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		q := db.NewSelect().Table(sp.table).Column("id").ColumnExpr(sp.col+" AS name").Where("id IN (?)", bun.List(ids))
		if sp.scopedBy != "" {
			q = q.Where(sp.scopedBy+" = ?", tenantID)
		}
		if err := q.Scan(ctx, &list); err != nil {
			return nil, errors.Wrapf(err, "load audit target %s names", sp.kind)
		}
		for _, item := range list {
			names[activityapi.TargetRef{Type: sp.kind, ID: item.ID}] = item.Name
		}
	}
	return names, nil
}

// loadPlatformTargetNames 批量读取平台日志涉及的工作区、成员与平台管理员名称。
func loadPlatformTargetNames(ctx context.Context, db bun.IDB, targets []activityapi.TargetRef) (map[activityapi.TargetRef]string, error) {
	return lookupTargetNames(ctx, db, "", targets, []targetLookupSpec{
		{kind: "tenant", table: "tenants", col: "name"},
		{kind: string(platformadmin.Type), table: "platform_admins", col: "name"},
		{kind: string(member.Type), table: "members", col: "name"},
	})
}

// targetHref 返回操作日志条目在管理员端内对应的跳转路径。
func targetHref(targetType, targetID string) string {
	switch targetType {
	case "tenant":
		return "/tenants"
	case string(platformadmin.Type):
		return "/admins"
	case string(member.Type):
		return "/members"
	case "feature":
		return "/settings/features"
	case "settings":
		if targetID == "llm" {
			return "/settings/model"
		}
		return "/settings/general"
	default:
		return ""
	}
}
