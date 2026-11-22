package contact

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/lang"
)

// Sort 是联系人列表的排序方式。
type Sort string

const (
	ByRecent  Sort = "recent"  // 按最近活跃时间从新到旧
	ByCreated Sort = "created" // 按创建时间从新到旧
)

// Sorts 是联系人列表的全部排序，显示文字是翻译文件中 contact.sort.<排序> 的消息。
var Sorts = enum.New("contact.sort", ByRecent, ByCreated)

// Schema 让 OpenAPI 中的 Sort 只接受上面的取值，并带有显示文字。
func (Sort) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Sorts) }

// ListInput 是联系人列表的查询条件。
type ListInput struct {
	page.OffsetParams
	Kind      Kind   `query:"kind" doc:"按联系人类型筛选：user 或 lead；不传为全部"`
	Q         string `query:"q" maxLength:"100" doc:"按名字、邮箱或外部用户编号搜索"`
	Language  string `query:"language" maxLength:"35" doc:"按客户的语言（BCP 47）筛选"`
	ChannelID string `query:"channel_id" format:"uuid" doc:"只列出在该渠道有身份的联系人"`
	Sort      Sort   `query:"sort" default:"recent" doc:"排序方式"`
}

// ListOutput 是一页联系人。
type ListOutput struct {
	Body page.Offset[ContactDTO]
}

// List 列出工作区的联系人。
type List struct {
	db *bun.DB
}

// NewList 返回联系人列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的一页联系人，支持按类型、关键词、语言和渠道筛选。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Offset[ContactDTO], error) {
	var rows []Contact
	q := a.db.NewSelect().Model(&rows).Where("contact.tenant_id = ?", sub.Tenant).
		Limit(in.Limit()).Offset(in.Offset())
	if in.Kind != "" {
		q = q.Where("contact.kind = ?", in.Kind)
	}
	if kw := strings.TrimSpace(in.Q); kw != "" {
		like := "%" + kw + "%"
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("contact.name ILIKE ?", like).
				WhereOr("contact.email ILIKE ?", like).
				WhereOr("contact.external_user_id ILIKE ?", like)
		})
	}
	if l := strings.TrimSpace(in.Language); l != "" {
		if canonical, ok := lang.Canonical(l); ok {
			l = canonical
		}
		q = q.Where("contact.language = ?", l)
	}
	if in.ChannelID != "" {
		q = q.Where("EXISTS (SELECT 1 FROM contact_identities ci WHERE ci.tenant_id = contact.tenant_id AND ci.contact_id = contact.id AND ci.channel_id = ?)", in.ChannelID)
	}
	switch in.Sort {
	case ByCreated:
		q = q.OrderExpr("contact.created_at DESC, contact.id DESC")
	default:
		q = q.OrderExpr("COALESCE(contact.last_seen_at, contact.created_at) DESC, contact.id DESC")
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list contacts")
	}
	items, err := toDTOs(ctx, a.db, sub.Tenant, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /t/{slug}/api/contacts。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: *p}, nil
}
