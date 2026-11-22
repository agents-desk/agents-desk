package macro

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/member"
)

// ListInput 是查询快捷回复列表的参数。
type ListInput struct {
	page.OffsetParams
	Q     string `query:"q" maxLength:"100" doc:"按标题、快捷指令码或正文模糊搜索"`
	Scope Scope  `query:"scope" doc:"按作用域筛选：shared 共享，personal 个人；为空返回全部可见快捷回复"`
}

// ListOutput 是快捷回复分页列表的响应。
type ListOutput struct {
	Body page.Offset[MacroDTO]
}

// List 查询当前工作区中调用者可见的快捷回复（工作区共享 + 本人个人）。
type List struct {
	db *bun.DB
}

// NewList 返回快捷回复列表查询动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 在所在工作区可见的快捷回复分页列表，按最近更新时间倒序排列。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in ListInput) (page.Offset[MacroDTO], error) {
	var rows []Macro
	q := a.db.NewSelect().Model(&rows).Where("tenant_id = ?", sub.Tenant)
	switch in.Scope {
	case ScopeShared:
		q = q.Where("owner_id IS NULL")
	case ScopePersonal:
		if sub.Type == member.Type {
			q = q.Where("owner_id = ?", sub.ID)
		} else {
			q = q.Where("FALSE")
		}
	default:
		if sub.Type == member.Type {
			q = q.Where("(owner_id IS NULL OR owner_id = ?)", sub.ID)
		} else {
			q = q.Where("owner_id IS NULL")
		}
	}
	if kw := strings.TrimSpace(in.Q); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(title ILIKE ? OR shortcut ILIKE ? OR body ILIKE ?)", like, like, like)
	}
	total, err := q.OrderExpr("updated_at DESC, id DESC").Offset(in.Offset()).Limit(in.Limit()).ScanAndCount(ctx)
	if err != nil {
		return page.Offset[MacroDTO]{}, errors.Wrap(err, "list macros")
	}
	items := make([]MacroDTO, len(rows))
	for i := range rows {
		items[i] = rows[i].DTO(sub)
	}
	return page.NewOffset(items, in.OffsetParams, total), nil
}

// HTTP 处理 GET /t/{slug}/api/macros。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: res}, nil
}
