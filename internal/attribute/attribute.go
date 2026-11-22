// Package attribute 是工作区的自定义属性定义与值校验：支持会话和联系人的自定义字段，后续扩展到工单和组织。
package attribute

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"
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
	// ErrNotFound 是属性定义不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "attribute.not_found")
	// ErrKeyTaken 是同一个对象下已经存在相同的属性标识。
	ErrKeyTaken = errorx.Define(errorx.Conflict, "attribute.key_taken")
	// ErrOptionsRequired 是单选或多选属性没有填写任何选项。
	ErrOptionsRequired = errorx.Define(errorx.InvalidArgument, "attribute.options_required")
	// ErrDuplicateOption 是属性的选项列表中有重复项。
	ErrDuplicateOption = errorx.Define(errorx.InvalidArgument, "attribute.duplicate_option")
	// ErrUnknownKey 是写入的属性标识没有在当前工作区的对应对象中定义。
	ErrUnknownKey = errorx.Define(errorx.InvalidArgument, "attribute.unknown_key")
	// ErrInvalidValue 是写入的属性值不符合该属性的类型或选项约束。
	ErrInvalidValue = errorx.Define(errorx.InvalidArgument, "attribute.invalid_value")
)

// Target 是属性定义所属的对象。
type Target string

const (
	TargetConversation Target = "conversation" // 会话
	TargetContact      Target = "contact"      // 联系人
)

// Targets 是全部可用的属性对象，显示文字是翻译文件中 attribute.target.<对象> 的消息。
var Targets = enum.New("attribute.target", TargetConversation, TargetContact)

// Schema 让 OpenAPI 中的 Target 只接受上面的取值，并带有显示文字。
func (Target) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Targets) }

// Type 是自定义属性的数据类型。
type Type string

const (
	TypeText        Type = "text"         // 文本
	TypeNumber      Type = "number"       // 数字
	TypeBoolean     Type = "boolean"      // 开关
	TypeDate        Type = "date"         // 日期（YYYY-MM-DD）
	TypeSelect      Type = "select"       // 单选
	TypeMultiSelect Type = "multi_select" // 多选
)

// Types 是全部可用的属性类型，显示文字是翻译文件中 attribute.type.<类型> 的消息。
var Types = enum.New("attribute.type", TypeText, TypeNumber, TypeBoolean, TypeDate, TypeSelect, TypeMultiSelect)

// Schema 让 OpenAPI 中的 Type 只接受上面的取值，并带有显示文字。
func (Type) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Types) }

// Definition 是工作区中的一个自定义属性定义。
type Definition struct {
	bun.BaseModel `bun:"table:attribute_definitions"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID    string   `bun:"tenant_id,notnull"`
	Target      Target   `bun:"target,notnull"`
	Key         string   `bun:"key,notnull"`
	Name        string   `bun:"name,notnull"`
	Description string   `bun:"description,notnull"`
	Type        Type     `bun:"type,notnull"`
	Options     []string `bun:"options,type:jsonb,notnull"`
	Position    int      `bun:"position,notnull"`
}

// AttributeDTO 是接口中的自定义属性定义。
type AttributeDTO struct {
	ID          string   `json:"id"`
	Target      Target   `json:"target"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        Type     `json:"type"`
	Options     []string `json:"options"`
	Position    int      `json:"position"`
}

// DTO 把属性定义转换为接口中的形式。
func (d *Definition) DTO() AttributeDTO {
	opts := d.Options
	if opts == nil {
		opts = []string{}
	}
	return AttributeDTO{
		ID: d.ID, Target: d.Target, Key: d.Key, Name: d.Name,
		Description: d.Description, Type: d.Type, Options: opts, Position: d.Position,
	}
}

// AttributeOutput 是返回一个属性定义的响应。
type AttributeOutput struct {
	Body AttributeDTO
}

// IDInput 是按路径参数 id 读取或删除属性定义的请求。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Change 描述某一个属性的值变更，用来写入时间线系统事件。
type Change struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	From any    `json:"from"`
	To   any    `json:"to"`
}

// Find 读取工作区 tenant 中的属性定义 id；lock 为 true 时加行锁。不存在或属于别的工作区时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*Definition, error) {
	d := new(Definition)
	q := db.NewSelect().Model(d).Where("id = ? AND tenant_id = ?", id, tenant)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return d, errors.Wrap(err, "load attribute definition")
}

// normalizeOptions 整理并校验选项列表：仅 select 和 multi_select 使用 options，其他类型统一存为空切片。
func normalizeOptions(typ Type, raw []string) ([]string, error) {
	if typ != TypeSelect && typ != TypeMultiSelect {
		return []string{}, nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		v := strings.TrimSpace(s)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			return nil, ErrDuplicateOption.New(errorx.Field("options"), errorx.Public("option", v))
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, ErrOptionsRequired.New(errorx.Field("options"))
	}
	return out, nil
}

// ApplyPatch 按工作区 tenant 下对象 target 的属性定义校验并合并 patch 到 current，返回新的属性表和实际发生变化的列表。
// patch 中未出现的 key 保持原样；值为 nil（或文本/日期/单选为空串、多选为空数组）时清空该属性。
func ApplyPatch(ctx context.Context, db bun.IDB, tenant string, target Target, current, patch map[string]any) (map[string]any, []Change, error) {
	next := make(map[string]any, len(current))
	for k, v := range current {
		next[k] = v
	}
	if len(patch) == 0 {
		return next, nil, nil
	}

	var defs []Definition
	if err := db.NewSelect().Model(&defs).Where("tenant_id = ? AND target = ?", tenant, target).Scan(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "load attribute definitions")
	}
	byKey := make(map[string]Definition, len(defs))
	for _, d := range defs {
		byKey[d.Key] = d
	}

	// 按 key 排序处理，让系统事件中的变更顺序稳定
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var changes []Change
	for _, k := range keys {
		def, ok := byKey[k]
		if !ok {
			return nil, nil, ErrUnknownKey.New(errorx.Field("attributes."+k), errorx.Public("key", k))
		}
		val, cleared, err := normalizeValue(def, patch[k])
		if err != nil {
			return nil, nil, err
		}
		prev, existed := next[k]
		if cleared {
			if !existed {
				continue
			}
			delete(next, k)
			changes = append(changes, Change{Key: k, Name: def.Name, From: prev, To: nil})
			continue
		}
		if existed && equalValue(prev, val) {
			continue
		}
		next[k] = val
		changes = append(changes, Change{Key: k, Name: def.Name, From: prev, To: val})
	}
	return next, changes, nil
}

// normalizeValue 按属性定义 def 校验和规范化单个属性值。cleared 为 true 表示清空该属性。
func normalizeValue(def Definition, raw any) (val any, cleared bool, err error) {
	if raw == nil {
		return nil, true, nil
	}
	invalid := func() error {
		return ErrInvalidValue.New(errorx.Field("attributes."+def.Key), errorx.Public("name", def.Name))
	}
	switch def.Type {
	case TypeText:
		s, ok := raw.(string)
		if !ok || len([]rune(s)) > 1000 {
			return nil, false, invalid()
		}
		if s == "" {
			return nil, true, nil
		}
		return s, false, nil
	case TypeNumber:
		f, ok := toFloat(raw)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false, invalid()
		}
		return f, false, nil
	case TypeBoolean:
		b, ok := raw.(bool)
		if !ok {
			return nil, false, invalid()
		}
		return b, false, nil
	case TypeDate:
		s, ok := raw.(string)
		if !ok {
			return nil, false, invalid()
		}
		if s == "" {
			return nil, true, nil
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, false, invalid()
		}
		return s, false, nil
	case TypeSelect:
		s, ok := raw.(string)
		if !ok {
			return nil, false, invalid()
		}
		if s == "" {
			return nil, true, nil
		}
		if !slices.Contains(def.Options, s) {
			return nil, false, invalid()
		}
		return s, false, nil
	case TypeMultiSelect:
		items, ok := toStrings(raw)
		if !ok {
			return nil, false, invalid()
		}
		if len(items) == 0 {
			return nil, true, nil
		}
		seen := make(map[string]struct{}, len(items))
		out := make([]string, 0, len(items))
		for _, s := range items {
			if !slices.Contains(def.Options, s) {
				return nil, false, invalid()
			}
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		return out, false, nil
	default:
		return nil, false, invalid()
	}
}

// toFloat 把 JSON 解码出来的数字或 Go 数值类型统一转换为 float64。
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// toStrings 把 []string 或 JSON 解码出的 []any 转换为 []string。
func toStrings(v any) ([]string, bool) {
	switch x := v.(type) {
	case []string:
		return x, true
	case []any:
		out := make([]string, len(x))
		for i, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	default:
		return nil, false
	}
}

// equalValue 比较两个规范化后的属性值是否相等，兼容 JSONB 读出的 []any 与 []string、float64 与 int。
func equalValue(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if af, aok := toFloat(a); aok {
		bf, bok := toFloat(b)
		return bok && af == bf
	}
	if as, aok := toStrings(a); aok {
		bs, bok := toStrings(b)
		return bok && slices.Equal(as, bs)
	}
	return a == b
}

// newID 返回按时间递增的 UUIDv7。
func newID() string { return uuid.Must(uuid.NewV7()).String() }
