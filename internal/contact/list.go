package contact

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
)

// Sort 是联系人列表的排序方式。
type Sort string

const (
	ByRecent  Sort = "recent"  // 按最近活跃时间从新到旧
	ByCreated Sort = "created" // 按创建时间从新到旧
)

// Sorts 是联系人列表的全部排序，显示文字是翻译文件中 contact.sort.<排序> 的消息。
var Sorts = enum.New("contact.sort", enum.Item(ByRecent), enum.Item(ByCreated))

// Schema 让 OpenAPI 中的 Sort 只接受上面的取值，并带有显示文字。
func (Sort) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Sorts) }

// ListInput 是联系人列表的查询条件。
type ListInput struct {
	page.CursorParams
	ViewID    string `query:"view_id" format:"uuid" doc:"应用已保存联系人视图的筛选条件"`
	Kind      Kind   `query:"kind" doc:"按联系人类型筛选：user 或 lead；不传为全部"`
	Q         string `query:"q" maxLength:"100" doc:"按名字、邮箱、外部用户编号或工作区内编号（如 #128）搜索"`
	Language  string `query:"language" maxLength:"35" doc:"按客户的语言（BCP 47）筛选，传主语言时同时匹配带子标签的写法"`
	ChannelID string `query:"channel_id" format:"uuid" doc:"只列出在该渠道有身份的联系人"`
	TagID     string `query:"tag_id" format:"uuid" doc:"只列出带有该标签的联系人"`
	Sort      Sort   `query:"sort" default:"recent" doc:"排序方式"`
}

// ListOutput 是一页联系人（游标分页）。
type ListOutput struct {
	Body page.Cursor[ContactDTO]
}

// timeKey 是按时间从新到旧排序时的游标位置。
type timeKey struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

// List 列出工作区的联系人。
type List struct {
	db *bun.DB
}

// NewList 返回联系人列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区中 sub 能看到的一页联系人（游标分页），支持按视图、类型、关键词、语言、渠道和标签筛选。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) (*page.Cursor[ContactDTO], error) {
	f := Filter{
		Kind:      in.Kind,
		Q:         in.Q,
		Language:  in.Language,
		ChannelID: in.ChannelID,
		TagID:     in.TagID,
	}
	sort := in.Sort
	if in.ViewID != "" {
		v, err := NewViews(a.db).Get(ctx, sub, in.ViewID)
		if err != nil {
			return nil, err
		}
		cfg := normalizeViewConfig(v.Config)
		f = mergeFilter(cfg.Filter, f)
		if sort == "" {
			sort = cfg.Sort
		}
	}

	var rows []Contact
	q := applyFilter(visible(a.db.NewSelect().Model(&rows).Where("contact.tenant_id = ?", sub.Tenant), sub), f).Limit(in.Limit())

	var key func(ContactDTO) any
	switch sort {
	case ByCreated:
		q = q.OrderExpr("contact.created_at DESC, contact.id DESC")
		var after timeKey
		if ok, err := in.After(&after); err != nil {
			return nil, err
		} else if ok {
			q = q.Where("(contact.created_at, contact.id) < (?, ?)", after.At, after.ID)
		}
		key = func(d ContactDTO) any { return timeKey{At: d.CreatedAt, ID: d.ID} }
	default:
		q = q.OrderExpr("COALESCE(contact.last_seen_at, contact.created_at) DESC, contact.id DESC")
		var after timeKey
		if ok, err := in.After(&after); err != nil {
			return nil, err
		} else if ok {
			q = q.Where("(COALESCE(contact.last_seen_at, contact.created_at), contact.id) < (?, ?)", after.At, after.ID)
		}
		key = func(d ContactDTO) any {
			at := d.CreatedAt
			if d.LastSeenAt != nil {
				at = *d.LastSeenAt
			}
			return timeKey{At: at, ID: d.ID}
		}
	}

	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list contacts")
	}
	items, err := toDTOs(ctx, a.db, sub, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewCursor(items, in.CursorParams, key)
	return &p, nil
}

// parseNumberQuery 从搜索关键词中解析联系人编号：支持 "128"、"#128"、"访客 #128"、"Visitor #128" 等常见写法。
func parseNumberQuery(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "visitor"):
		s = strings.TrimSpace(s[len("visitor"):])
	case strings.HasPrefix(s, "访客"):
		s = strings.TrimSpace(strings.TrimPrefix(s, "访客"))
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "#"))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
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
