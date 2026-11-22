// Package audit 提供管理员端查看平台操作日志的业务动作与接口。
// 操作记录由各业务动作在事务中通过 tenon/activity.Record 写入 activity_log 表，本包只读取平台域（tenant = ""）的记录，
// 补全操作人与目标名称，并按当前请求语言翻译为可供 ActivityTimeline 直接展示的时间线条目。
package audit

import (
	"context"
	"maps"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
)

// ListInput 是平台操作日志的查询参数。
type ListInput struct {
	page.OffsetParams
	TargetType string `query:"target_type" enum:",tenant,platform_admin,settings,feature" doc:"按对象类型筛选；为空时返回全部"`
}

// Item 是操作日志表格中的一条记录，字段与 @go-tenon/ui 的 ActivityLog 约定一致。
type Item struct {
	ID           string    `json:"id"`
	Event        string    `json:"event"`
	TargetType   string    `json:"target_type,omitempty"`
	TargetID     string    `json:"target_id,omitempty"`
	Text         string    `json:"text"`
	Actor        string    `json:"actor,omitempty"`
	Impersonator string    `json:"impersonator,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Href         string    `json:"href,omitempty"`
}

// ListOutput 是平台操作日志的响应（固定分页）。
type ListOutput struct {
	Body page.Offset[Item]
}

// List 读取平台域的操作日志。
type List struct {
	db *bun.DB
}

// NewList 返回平台操作日志查询动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 按条件查询平台操作日志，解析操作人姓名和目标名称并翻译为当前语言的展示文字。
func (a *List) Handle(ctx context.Context, in ListInput) (page.Offset[Item], error) {
	res, err := activity.List(ctx, a.db, activity.Query{
		Tenant:     "",
		TargetType: in.TargetType,
		Page:       in.OffsetParams,
	})
	if err != nil {
		return page.Offset[Item]{}, err
	}
	actors, err := a.loadActorNames(ctx, res.Items)
	if err != nil {
		return page.Offset[Item]{}, err
	}
	targets, err := a.loadTargetNames(ctx, res.Items)
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
		// 将 changed 数组格式化为逗号分隔字符串，便于在模板中直接显示
		if ch, ok := props["changed"].([]any); ok {
			parts := make([]string, 0, len(ch))
			for _, v := range ch {
				if s, ok := v.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			props["changed"] = strings.Join(parts, ", ")
		}
		items = append(items, Item{
			ID:           r.ID.String(),
			Event:        r.Event,
			TargetType:   r.TargetType,
			TargetID:     r.TargetID,
			Text:         i18n.T(ctx, "activity."+r.Event, props),
			Actor:        actors[r.Actor()],
			Impersonator: actors[r.Impersonator()],
			CreatedAt:    r.CreatedAt,
			Href:         targetHref(r.TargetType, r.TargetID),
		})
	}
	return page.NewOffset(items, in.OffsetParams, res.Total), nil
}

// HTTP 处理 GET /audit。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	res, err := a.Handle(ctx, *in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: res}, nil
}

// targetKey 唯一标识一条日志指向的业务对象。
type targetKey struct{ kind, id string }

// loadActorNames 批量读取日志中出现的平台管理员与成员姓名，按 identity.Ref 映射。
func (a *List) loadActorNames(ctx context.Context, rows []activity.Activity) (map[identity.Ref]string, error) {
	var adminIDs, memberIDs []string
	collect := func(ref identity.Ref) {
		if ref.IsZero() {
			return
		}
		switch ref.Type {
		case platformadmin.Type:
			adminIDs = append(adminIDs, ref.ID)
		case member.Type:
			memberIDs = append(memberIDs, ref.ID)
		}
	}
	for _, r := range rows {
		collect(r.Actor())
		collect(r.Impersonator())
	}
	names := map[identity.Ref]string{}
	if len(adminIDs) > 0 {
		var list []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := a.db.NewSelect().Table("platform_admins").Column("id", "name").Where("id IN (?)", bun.List(adminIDs)).Scan(ctx, &list); err != nil {
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
		if err := a.db.NewSelect().Table("members").Column("id", "name").Where("id IN (?)", bun.List(memberIDs)).Scan(ctx, &list); err != nil {
			return nil, errors.Wrap(err, "load member names")
		}
		for _, item := range list {
			names[identity.Ref{Type: member.Type, ID: item.ID}] = item.Name
		}
	}
	return names, nil
}

// loadTargetNames 批量读取日志涉及的工作区和平台管理员名称，供事件译文兜底展示。
func (a *List) loadTargetNames(ctx context.Context, rows []activity.Activity) (map[targetKey]string, error) {
	var tenantIDs, adminIDs []string
	for _, r := range rows {
		if r.TargetID == "" {
			continue
		}
		switch r.TargetType {
		case "tenant":
			tenantIDs = append(tenantIDs, r.TargetID)
		case string(platformadmin.Type):
			adminIDs = append(adminIDs, r.TargetID)
		}
	}
	names := map[targetKey]string{}
	if len(tenantIDs) > 0 {
		var list []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := a.db.NewSelect().Table("tenants").Column("id", "name").Where("id IN (?)", bun.List(tenantIDs)).Scan(ctx, &list); err != nil {
			return nil, errors.Wrap(err, "load tenant names")
		}
		for _, item := range list {
			names[targetKey{"tenant", item.ID}] = item.Name
		}
	}
	if len(adminIDs) > 0 {
		var list []struct {
			ID   string `bun:"id"`
			Name string `bun:"name"`
		}
		if err := a.db.NewSelect().Table("platform_admins").Column("id", "name").Where("id IN (?)", bun.List(adminIDs)).Scan(ctx, &list); err != nil {
			return nil, errors.Wrap(err, "load target platform admin names")
		}
		for _, item := range list {
			names[targetKey{string(platformadmin.Type), item.ID}] = item.Name
		}
	}
	return names, nil
}

// targetHref 返回操作日志条目在管理员端内对应的跳转路径。
func targetHref(targetType, targetID string) string {
	switch targetType {
	case "tenant":
		return "/tenants"
	case string(platformadmin.Type):
		return "/admins"
	case "feature":
		return "/settings/features"
	case "settings":
		switch targetID {
		case "llm":
			return "/settings/model"
		case "general", "mail":
			return "/settings/general"
		default:
			return "/settings"
		}
	default:
		return ""
	}
}
