package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/savedview"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
)

// viewTarget 是会话视图在 saved_views 表中的业务对象标识。
const viewTarget = "conversation"

// NewViews 返回会话已保存视图的存储组，共享视图的增删改与排序要求 views:manage 权限。
func NewViews(db bun.IDB) *savedview.Group[ViewConfig] {
	return savedview.New[ViewConfig](db, viewTarget, savedview.Options{
		ManageShared: func(sub identity.Subject) bool {
			return sub.Can(string(auth.ViewsManage))
		},
	})
}

// ViewDTO 是接口中的一条收件箱视图。
type ViewDTO struct {
	ID        string          `json:"id"`
	Scope     savedview.Scope `json:"scope" doc:"作用域：shared 共享，personal 个人"`
	Name      string          `json:"name"`
	Config    ViewConfig      `json:"config" doc:"视图保存的筛选条件与默认排序"`
	Position  int             `json:"position" doc:"同作用域内的展示顺序"`
	Editable  bool            `json:"editable" doc:"当前成员是否有权修改或删除这个视图"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ViewSummary 是收件箱二级导航中的一条视图：视图定义与实时会话数量/积压情况。
type ViewSummary struct {
	ID       string          `json:"id"`
	Scope    savedview.Scope `json:"scope"`
	Name     string          `json:"name"`
	Config   ViewConfig      `json:"config"`
	Position int             `json:"position"`
	Editable bool            `json:"editable"`
	Summary
}

// ViewOutput 是返回单条视图的响应。
type ViewOutput struct {
	Body ViewDTO
}

// ViewIDInput 是按路径参数 id 读取或删除视图的请求。
type ViewIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// ViewCreateParams 是新建收件箱视图的参数。
type ViewCreateParams struct {
	Scope  savedview.Scope `json:"scope" doc:"作用域：shared 共享（需要 views:manage 权限），personal 个人"`
	Name   string          `json:"name" minLength:"1" maxLength:"50" doc:"视图名称"`
	Config ViewConfig      `json:"config" doc:"视图的筛选条件与默认排序"`
}

// ViewUpdateParams 是修改收件箱视图的参数（作用域创建后不可修改）。
type ViewUpdateParams struct {
	Name   string     `json:"name" minLength:"1" maxLength:"50" doc:"视图名称"`
	Config ViewConfig `json:"config" doc:"视图的筛选条件与默认排序"`
}

// viewToDTO 把框架视图 v 转换为接口 DTO，按 sub 计算可编辑状态。
func viewToDTO(g *savedview.Group[ViewConfig], sub identity.Subject, v savedview.View[ViewConfig]) ViewDTO {
	cfg := v.Config
	if cfg.Sort == "" {
		cfg.Sort = ByWaiting
	}
	return ViewDTO{
		ID:        v.ID,
		Scope:     v.Scope,
		Name:      v.Name,
		Config:    cfg,
		Position:  v.Position,
		Editable:  g.CanManage(sub, v),
		CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	}
}

// notifyViewsChanged 在事务 tx 提交后推送视图列表变更事件：共享视图推送给工作区全体成员，个人视图推送给归属成员本人。
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
		return errors.Wrap(push.Send(ctx, hub, tenantID, targets, ev), "push views changed")
	})
	return nil
}
