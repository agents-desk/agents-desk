// Package tag 是工作区的标签：给会话和联系人加的标记，用于筛选和统计。
package tag

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrNotFound 是标签不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "tag.not_found")
	// ErrNameTaken 是工作区中已经有一个同名的标签（不区分大小写）。
	ErrNameTaken = errorx.Define(errorx.Conflict, "tag.name_taken")
)

// Color 是标签的颜色，取自前端主题（@go-tenon/ui）的预设色板名。
type Color string

const (
	BlueGray   Color = "blue-gray"   // 灰蓝（默认）
	Red        Color = "red"         // 红色
	DeepOrange Color = "deep-orange" // 深橙
	Yellow     Color = "yellow"      // 黄色
	Green      Color = "green"       // 绿色
	SkyBlue    Color = "sky-blue"    // 天蓝
	BrightBlue Color = "bright-blue" // 亮蓝
	Violet     Color = "violet"      // 紫罗兰
	Purple     Color = "purple"      // 紫色
	Magenta    Color = "magenta"     // 洋红
	Brown      Color = "brown"       // 棕色
)

// Colors 是全部可用的标签颜色，显示文字是翻译文件中 tag.color.<颜色> 的消息。
var Colors = enum.New("tag.color", BlueGray, Red, DeepOrange, Yellow, Green, SkyBlue, BrightBlue, Violet, Purple, Magenta, Brown)

// Schema 让 OpenAPI 中的 Color 只接受上面的取值，并带有显示文字。
func (Color) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Colors) }

// Tag 是工作区中的一个标签定义。
type Tag struct {
	bun.BaseModel `bun:"table:tags"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID    string `bun:"tenant_id,notnull"`
	Name        string `bun:"name,notnull"`
	Color       Color  `bun:"color,notnull"`
	Description string `bun:"description,notnull"`
}

// Summary 是接口中标签的概要，显示在会话、联系人和标签选择器中。
type Summary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color Color  `json:"color"`
}

// Summary 返回标签 t 的概要。
func (t *Tag) Summary() Summary {
	return Summary{ID: t.ID, Name: t.Name, Color: t.Color}
}

// TagDTO 是接口中的标签详情，包含使用次数。
type TagDTO struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Color             Color  `json:"color"`
	Description       string `json:"description"`
	ConversationCount int    `json:"conversation_count" doc:"打了这个标签的会话数"`
	ContactCount      int    `json:"contact_count" doc:"打了这个标签的联系人数"`
}

// TagOutput 是返回一个标签的响应。
type TagOutput struct {
	Body TagDTO
}

// IDInput 是按路径参数 id 读取或删除标签的请求。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Find 读取工作区 tenant 中的标签 id；lock 为 true 时加行锁。不存在或属于别的工作区时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*Tag, error) {
	t := new(Tag)
	q := db.NewSelect().Model(t).Where("id = ? AND tenant_id = ?", id, tenant)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return t, errors.Wrap(err, "load tag")
}

// ForConversations 批量读取一组会话已打的标签，按标签名排序。
func ForConversations(ctx context.Context, db bun.IDB, conversationIDs []string) (map[string][]Summary, error) {
	out := make(map[string][]Summary, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ConversationID string `bun:"conversation_id"`
		ID             string `bun:"id"`
		Name           string `bun:"name"`
		Color          Color  `bun:"color"`
	}
	err := db.NewRaw(`SELECT ct.conversation_id, t.id, t.name, t.color
		FROM conversation_tags AS ct
		JOIN tags AS t ON t.id = ct.tag_id
		WHERE ct.conversation_id IN (?)
		ORDER BY lower(t.name), t.id`, bun.In(conversationIDs)).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load conversation tags")
	}
	for _, r := range rows {
		out[r.ConversationID] = append(out[r.ConversationID], Summary{ID: r.ID, Name: r.Name, Color: r.Color})
	}
	return out, nil
}

// ForContacts 批量读取一组联系人已打的标签，按标签名排序。
func ForContacts(ctx context.Context, db bun.IDB, contactIDs []string) (map[string][]Summary, error) {
	out := make(map[string][]Summary, len(contactIDs))
	if len(contactIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ContactID string `bun:"contact_id"`
		ID        string `bun:"id"`
		Name      string `bun:"name"`
		Color     Color  `bun:"color"`
	}
	err := db.NewRaw(`SELECT ct.contact_id, t.id, t.name, t.color
		FROM contact_tags AS ct
		JOIN tags AS t ON t.id = ct.tag_id
		WHERE ct.contact_id IN (?)
		ORDER BY lower(t.name), t.id`, bun.In(contactIDs)).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load contact tags")
	}
	for _, r := range rows {
		out[r.ContactID] = append(out[r.ContactID], Summary{ID: r.ID, Name: r.Name, Color: r.Color})
	}
	return out, nil
}

// toDTOs 把一组标签转换为接口中的形式，一次统计每个标签在会话和联系人中的使用次数。
func toDTOs(ctx context.Context, db bun.IDB, tags []Tag) ([]TagDTO, error) {
	out := make([]TagDTO, len(tags))
	if len(tags) == 0 {
		return out, nil
	}
	ids := make([]string, len(tags))
	for i := range tags {
		ids[i] = tags[i].ID
	}
	var convCounts []struct {
		TagID string `bun:"tag_id"`
		Count int    `bun:"count"`
	}
	if err := db.NewRaw(`SELECT tag_id, count(*) AS count FROM conversation_tags WHERE tag_id IN (?) GROUP BY tag_id`, bun.In(ids)).Scan(ctx, &convCounts); err != nil {
		return nil, errors.Wrap(err, "count conversation tags")
	}
	var contactCounts []struct {
		TagID string `bun:"tag_id"`
		Count int    `bun:"count"`
	}
	if err := db.NewRaw(`SELECT tag_id, count(*) AS count FROM contact_tags WHERE tag_id IN (?) GROUP BY tag_id`, bun.In(ids)).Scan(ctx, &contactCounts); err != nil {
		return nil, errors.Wrap(err, "count contact tags")
	}
	convMap := make(map[string]int, len(convCounts))
	for _, c := range convCounts {
		convMap[c.TagID] = c.Count
	}
	contactMap := make(map[string]int, len(contactCounts))
	for _, c := range contactCounts {
		contactMap[c.TagID] = c.Count
	}
	for i, t := range tags {
		out[i] = TagDTO{
			ID: t.ID, Name: t.Name, Color: t.Color, Description: t.Description,
			ConversationCount: convMap[t.ID], ContactCount: contactMap[t.ID],
		}
	}
	return out, nil
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }

// Now 返回当前时间，精度与 PostgreSQL 的 timestamptz 一致。
func Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
