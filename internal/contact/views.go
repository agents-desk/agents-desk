package contact

import (
	"context"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/savedview"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
)

// viewTarget 是联系人已保存筛选视图在 saved_views 表中的业务对象标识。
const viewTarget = "contact"

// NewViews 返回联系人已保存视图的存储组，共享视图的增删改与排序要求 views:manage 权限。
func NewViews(db bun.IDB) *savedview.Group[ViewConfig] {
	return savedview.New[ViewConfig](db, viewTarget, savedview.Options{
		ManageShared: func(sub identity.Subject) bool {
			return sub.Can(string(auth.ViewsManage))
		},
	})
}

// ViewConfig 是联系人保存的筛选视图配置。
type ViewConfig struct {
	Filter
	Sort Sort `json:"sort,omitempty" doc:"默认排序方式：recent 或 created"`
}

// ViewDTO 是接口中的一条联系人视图。
type ViewDTO struct {
	ID        string          `json:"id"`
	Scope     savedview.Scope `json:"scope" doc:"作用域：shared 共享，personal 个人"`
	Name      string          `json:"name"`
	Config    ViewConfig      `json:"config" doc:"保存的筛选条件与默认排序"`
	Position  int             `json:"position" doc:"同作用域内的展示顺序"`
	Editable  bool            `json:"editable" doc:"当前成员是否有权修改或删除这个视图"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ViewSummary 是联系人二级导航中的一条视图：视图定义与当前成员可见的实时联系人数。
type ViewSummary struct {
	ID       string          `json:"id"`
	Scope    savedview.Scope `json:"scope"`
	Name     string          `json:"name"`
	Config   ViewConfig      `json:"config"`
	Position int             `json:"position"`
	Editable bool            `json:"editable"`
	Count    int64           `json:"count"`
}

// ViewOutput 是返回单条联系人视图的响应。
type ViewOutput struct {
	Body ViewDTO
}

// ViewListOutput 是返回联系人视图列表的响应。
type ViewListOutput struct {
	Body struct {
		Items []ViewDTO `json:"items"`
	}
}

// ViewIDInput 是按路径参数 id 读取或删除联系人视图的请求。
type ViewIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// ViewCreateParams 是新建联系人视图的参数。
type ViewCreateParams struct {
	Scope  savedview.Scope `json:"scope" doc:"作用域：shared 共享（需要 views:manage 权限），personal 个人"`
	Name   string          `json:"name" minLength:"1" maxLength:"50" doc:"视图名称"`
	Config ViewConfig      `json:"config" doc:"视图的筛选条件与默认排序"`
}

// ViewCreateInput 是新建联系人视图的 HTTP 请求。
type ViewCreateInput struct {
	Body ViewCreateParams
}

// ViewUpdateParams 是修改联系人视图的参数。
type ViewUpdateParams struct {
	Name   string     `json:"name" minLength:"1" maxLength:"50" doc:"视图名称"`
	Config ViewConfig `json:"config" doc:"视图的筛选条件与默认排序"`
}

// ViewUpdateInput 是修改联系人视图的 HTTP 请求。
type ViewUpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body ViewUpdateParams
}

// ViewReorderInput 是调整联系人视图顺序的 HTTP 请求。
type ViewReorderInput struct {
	Body struct {
		Scope savedview.Scope `json:"scope" doc:"要排序的作用域：shared 或 personal"`
		IDs   []string        `json:"ids" minItems:"1" maxItems:"100" uniqueItems:"true" doc:"按展示顺序排列的视图 ID 列表"`
	}
}

// normalizeViewConfig 清理视图配置中的空白并补齐默认排序。
func normalizeViewConfig(cfg ViewConfig) ViewConfig {
	cfg.Q = strings.TrimSpace(cfg.Q)
	cfg.Language = strings.TrimSpace(cfg.Language)
	if cfg.Sort == "" {
		cfg.Sort = ByRecent
	}
	return cfg
}

// viewToDTO 把框架视图 v 转换为接口 DTO。
func viewToDTO(g *savedview.Group[ViewConfig], sub identity.Subject, v savedview.View[ViewConfig]) ViewDTO {
	return ViewDTO{
		ID:        v.ID,
		Scope:     v.Scope,
		Name:      v.Name,
		Config:    normalizeViewConfig(v.Config),
		Position:  v.Position,
		Editable:  g.CanManage(sub, v),
		CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	}
}

// notifyViewsChanged 在事务 tx 提交后推送视图列表变更事件。
func notifyViewsChanged(ctx context.Context, tx database.Tx, hub *broadcast.Hub, roles *authz.Store, tenantID string, v savedview.View[ViewConfig]) error {
	if hub == nil {
		return nil
	}
	var targets []string
	if v.Scope == savedview.ScopePersonal {
		if v.Owner.Type == member.Type && v.Owner.ID != "" {
			targets = []string{v.Owner.ID}
		}
	} else if roles != nil {
		members, err := roles.Members(ctx, tenantID)
		if err != nil {
			return err
		}
		for _, m := range members {
			if m.Subject.Type == member.Type {
				targets = append(targets, m.Subject.ID)
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	ev := push.Event{Type: push.Views, ID: v.ID}
	tx.AfterCommit(func(ctx context.Context) error {
		return errors.Wrap(push.Send(ctx, hub, tenantID, targets, ev), "push contact views changed")
	})
	return nil
}

// ViewActions 是联系人已保存筛选视图的增删改查与排序动作集合。
type ViewActions struct {
	db    *bun.DB
	hub   *broadcast.Hub
	roles *authz.Store
}

// NewViewActions 返回联系人视图动作集合。
func NewViewActions(db *bun.DB, hub *broadcast.Hub, roles *authz.Store) *ViewActions {
	return &ViewActions{db: db, hub: hub, roles: roles}
}

// List 返回 sub 可见的全部联系人视图（先共享、后个人，按 position 排列）。
func (a *ViewActions) List(ctx context.Context, sub identity.Subject) ([]ViewDTO, error) {
	g := NewViews(a.db)
	rows, err := g.List(ctx, sub, "")
	if err != nil {
		return nil, err
	}
	out := make([]ViewDTO, len(rows))
	for i, v := range rows {
		out[i] = viewToDTO(g, sub, v)
	}
	return out, nil
}

// Create 新建一条共享或个人联系人视图，共享视图需要 views:manage 权限并记录操作日志。
func (a *ViewActions) Create(ctx context.Context, sub identity.Subject, p ViewCreateParams) (*ViewDTO, error) {
	g := NewViews(a.db)
	var created savedview.View[ViewConfig]
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		v, err := g.WithDB(tx).Create(ctx, sub, savedview.CreateInput[ViewConfig]{
			Scope:  p.Scope,
			Name:   p.Name,
			Config: normalizeViewConfig(p.Config),
		})
		if err != nil {
			return err
		}
		created = v
		if v.Scope == savedview.ScopeShared {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "contact_view.created",
				TargetType: "contact_view",
				TargetID:   v.ID,
				Properties: map[string]any{"name": v.Name},
			}); err != nil {
				return err
			}
		}
		return notifyViewsChanged(ctx, tx, a.hub, a.roles, sub.Tenant, v)
	})
	if err != nil {
		return nil, err
	}
	dto := viewToDTO(g, sub, created)
	return &dto, nil
}

// Update 修改联系人视图 id 的名称和筛选配置。
func (a *ViewActions) Update(ctx context.Context, sub identity.Subject, id string, p ViewUpdateParams) (*ViewDTO, error) {
	g := NewViews(a.db)
	var updated savedview.View[ViewConfig]
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		v, err := g.WithDB(tx).Update(ctx, sub, id, savedview.UpdateInput[ViewConfig]{
			Name:   p.Name,
			Config: normalizeViewConfig(p.Config),
		})
		if err != nil {
			return err
		}
		updated = v
		if v.Scope == savedview.ScopeShared {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "contact_view.updated",
				TargetType: "contact_view",
				TargetID:   v.ID,
				Properties: map[string]any{"name": v.Name},
			}); err != nil {
				return err
			}
		}
		return notifyViewsChanged(ctx, tx, a.hub, a.roles, sub.Tenant, v)
	})
	if err != nil {
		return nil, err
	}
	dto := viewToDTO(g, sub, updated)
	return &dto, nil
}

// Delete 删除联系人视图 id。
func (a *ViewActions) Delete(ctx context.Context, sub identity.Subject, id string) error {
	g := NewViews(a.db)
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		v, err := g.WithDB(tx).Delete(ctx, sub, id)
		if err != nil {
			return err
		}
		if v.Scope == savedview.ScopeShared {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event:      "contact_view.deleted",
				TargetType: "contact_view",
				TargetID:   v.ID,
				Properties: map[string]any{"name": v.Name},
			}); err != nil {
				return err
			}
		}
		return notifyViewsChanged(ctx, tx, a.hub, a.roles, sub.Tenant, v)
	})
}

// Reorder 调整指定作用域下联系人视图的展示顺序。
func (a *ViewActions) Reorder(ctx context.Context, sub identity.Subject, scope savedview.Scope, ids []string) error {
	g := NewViews(a.db)
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := g.WithDB(tx).Reorder(ctx, sub, scope, ids); err != nil {
			return err
		}
		dummy := savedview.View[ViewConfig]{ID: ids[0], Scope: scope, Owner: identity.Ref{Type: sub.Type, ID: sub.ID}}
		return notifyViewsChanged(ctx, tx, a.hub, a.roles, sub.Tenant, dummy)
	})
}

// ListHTTP 处理 GET /t/{slug}/api/contacts/views。
func (a *ViewActions) ListHTTP(ctx context.Context, _ *struct{}) (*ViewListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.List(ctx, sub)
	if err != nil {
		return nil, err
	}
	out := &ViewListOutput{}
	out.Body.Items = items
	return out, nil
}

// CreateHTTP 处理 POST /t/{slug}/api/contacts/views。
func (a *ViewActions) CreateHTTP(ctx context.Context, in *ViewCreateInput) (*ViewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Create(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ViewOutput{Body: *dto}, nil
}

// UpdateHTTP 处理 PUT /t/{slug}/api/contacts/views/{id}。
func (a *ViewActions) UpdateHTTP(ctx context.Context, in *ViewUpdateInput) (*ViewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Update(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ViewOutput{Body: *dto}, nil
}

// DeleteHTTP 处理 DELETE /t/{slug}/api/contacts/views/{id}。
func (a *ViewActions) DeleteHTTP(ctx context.Context, in *ViewIDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Delete(ctx, sub, in.ID)
}

// ReorderHTTP 处理 PUT /t/{slug}/api/contacts/views/reorder。
func (a *ViewActions) ReorderHTTP(ctx context.Context, in *ViewReorderInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Reorder(ctx, sub, in.Body.Scope, in.Body.IDs)
}
