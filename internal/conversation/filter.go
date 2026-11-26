package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/lang"
	"agents-desk/internal/member"
	"agents-desk/internal/tag"
)

// ErrInvalidFilter 是视图或列表传入的筛选条件不合法。
var ErrInvalidFilter = errorx.Define(errorx.InvalidArgument, "conversation.invalid_filter")

// TeamFilterMode 是团队收件箱筛选的模式。
type TeamFilterMode string

const (
	TeamFilterMyTeams    TeamFilterMode = "my_teams"   // 我所在的团队收件箱
	TeamFilterUnassigned TeamFilterMode = "unassigned" // 未分配到任何团队收件箱
	TeamFilterIDs        TeamFilterMode = "ids"        // 指定的团队收件箱列表
)

// TeamFilterModes 是团队筛选的全部模式，显示文字是翻译文件中 conversation.team_filter_mode.<模式> 的消息。
var TeamFilterModes = enum.New("conversation.team_filter_mode",
	enum.Item(TeamFilterMyTeams), enum.Item(TeamFilterUnassigned), enum.Item(TeamFilterIDs))

// Schema 让 OpenAPI 中的 TeamFilterMode 只接受上面的取值，并带有显示文字。
func (TeamFilterMode) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, TeamFilterModes)
}

// TeamFilter 是按团队收件箱筛选会话的条件。
type TeamFilter struct {
	Mode TeamFilterMode `json:"mode" doc:"团队筛选模式：my_teams（我所在的团队）、unassigned（未分配团队）、ids（指定团队）"`
	IDs  []string       `json:"ids,omitempty" maxItems:"50" doc:"mode 为 ids 时必填：团队收件箱 ID 列表"`
}

// AssigneeFilterMode 是负责人筛选的模式。
type AssigneeFilterMode string

const (
	AssigneeFilterMe         AssigneeFilterMode = "me"         // 当前登录成员
	AssigneeFilterUnassigned AssigneeFilterMode = "unassigned" // 无负责人
	AssigneeFilterMember     AssigneeFilterMode = "member"     // 指定成员
)

// AssigneeFilterModes 是负责人筛选的全部模式，显示文字是翻译文件中 conversation.assignee_filter_mode.<模式> 的消息。
var AssigneeFilterModes = enum.New("conversation.assignee_filter_mode",
	enum.Item(AssigneeFilterMe), enum.Item(AssigneeFilterUnassigned), enum.Item(AssigneeFilterMember))

// Schema 让 OpenAPI 中的 AssigneeFilterMode 只接受上面的取值，并带有显示文字。
func (AssigneeFilterMode) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, AssigneeFilterModes)
}

// AssigneeFilter 是按负责人筛选会话的条件。
type AssigneeFilter struct {
	Mode     AssigneeFilterMode `json:"mode" doc:"负责人筛选模式：me（我）、unassigned（无负责人）、member（指定成员）"`
	MemberID string             `json:"member_id,omitempty" format:"uuid" doc:"mode 为 member 时必填：成员 ID"`
}

// TagMatch 是多标签筛选时的匹配方式。
type TagMatch string

const (
	TagMatchAny TagMatch = "any" // 命中任一标签
	TagMatchAll TagMatch = "all" // 同时命中全部标签
)

// TagMatches 是全部标签匹配方式，显示文字是翻译文件中 conversation.tag_match.<方式> 的消息。
var TagMatches = enum.New("conversation.tag_match", enum.Item(TagMatchAny), enum.Item(TagMatchAll))

// Schema 让 OpenAPI 中的 TagMatch 只接受上面的取值，并带有显示文字。
func (TagMatch) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, TagMatches) }

// AttributeOp 是自定义属性筛选的操作符。
type AttributeOp string

const (
	AttrOpEq       AttributeOp = "eq"       // 等于
	AttrOpNeq      AttributeOp = "neq"      // 不等于
	AttrOpContains AttributeOp = "contains" // 包含（文本，不区分大小写）
	AttrOpGt       AttributeOp = "gt"       // 大于（数字、日期）
	AttrOpGte      AttributeOp = "gte"      // 大于等于（数字、日期）
	AttrOpLt       AttributeOp = "lt"       // 小于（数字、日期）
	AttrOpLte      AttributeOp = "lte"      // 小于等于（数字、日期）
	AttrOpIn       AttributeOp = "in"       // 包含任一选项（单选、多选）
	AttrOpIsSet    AttributeOp = "is_set"   // 已填写
	AttrOpIsEmpty  AttributeOp = "is_empty" // 未填写
)

// AttributeOps 是全部自定义属性操作符，显示文字是翻译文件中 conversation.attribute_op.<操作符> 的消息。
var AttributeOps = enum.New("conversation.attribute_op",
	enum.Item(AttrOpEq), enum.Item(AttrOpNeq), enum.Item(AttrOpContains),
	enum.Item(AttrOpGt), enum.Item(AttrOpGte), enum.Item(AttrOpLt), enum.Item(AttrOpLte),
	enum.Item(AttrOpIn), enum.Item(AttrOpIsSet), enum.Item(AttrOpIsEmpty))

// Schema 让 OpenAPI 中的 AttributeOp 只接受上面的取值，并带有显示文字。
func (AttributeOp) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, AttributeOps) }

// AttributeFilter 是一条会话自定义属性筛选条件。
type AttributeFilter struct {
	Key   string      `json:"key" minLength:"1" maxLength:"64" doc:"会话自定义属性标识"`
	Op    AttributeOp `json:"op" doc:"比较操作符"`
	Value any         `json:"value,omitempty" doc:"比较值；is_set 与 is_empty 时忽略，in 时为字符串数组"`
}

// Filter 是服务型会话的统一筛选条件：收件箱列表、全文搜索与已保存视图共享同一套条件模型。
type Filter struct {
	Statuses   []Status          `json:"statuses,omitempty" maxItems:"3" doc:"会话状态（多选）；为空表示不限状态"`
	Team       *TeamFilter       `json:"team,omitempty" doc:"团队收件箱筛选"`
	Assignee   *AssigneeFilter   `json:"assignee,omitempty" doc:"负责人筛选"`
	Waiting    *bool             `json:"waiting,omitempty" doc:"是否等待回复：true 等待回复，false 已回复，为空不限"`
	Priority   Priority          `json:"priority,omitempty" doc:"优先级筛选"`
	ChannelIDs []string          `json:"channel_ids,omitempty" maxItems:"50" doc:"渠道 ID（匹配其中任一）"`
	TagIDs     []string          `json:"tag_ids,omitempty" maxItems:"50" doc:"标签 ID 列表"`
	TagMatch   TagMatch          `json:"tag_match,omitempty" doc:"多标签匹配方式：any（任一，默认）或 all（全部）"`
	Language   string            `json:"language,omitempty" maxLength:"35" doc:"客户语言（BCP 47，传主语言时同时匹配子标签）"`
	Attributes []AttributeFilter `json:"attributes,omitempty" maxItems:"20" doc:"会话自定义属性筛选条件（全部满足）"`
}

// ViewConfig 是保存进视图的完整配置：筛选条件与默认排序。
type ViewConfig struct {
	Filter
	Sort Sort `json:"sort,omitempty" doc:"默认排序：waiting（按等待时长）或 recent（按最近活动）"`
}

// validateViewConfig 校验并规范化视图配置（创建、更新视图时调用）：验证所有引用的团队、成员、渠道、标签与属性定义属于当前工作区。
func validateViewConfig(ctx context.Context, db bun.IDB, tenantID string, in ViewConfig) (ViewConfig, error) {
	f, err := validateFilter(ctx, db, tenantID, in.Filter, true)
	if err != nil {
		return ViewConfig{}, err
	}
	sort := in.Sort
	if sort == "" {
		sort = ByWaiting
	} else if !slices.Contains(Sorts.Values(), sort) {
		return ViewConfig{}, ErrInvalidFilter.New(errorx.Field("config.sort"))
	}
	return ViewConfig{Filter: f, Sort: sort}, nil
}

// parseFilterParam 解析并校验查询参数中的 JSON 筛选条件（用于收件箱临时筛选或视图内微调预览）。
func parseFilterParam(ctx context.Context, db bun.IDB, tenantID, raw string) (Filter, error) {
	var in Filter
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return Filter{}, ErrInvalidFilter.New(errorx.Field("filter"))
	}
	return validateFilter(ctx, db, tenantID, in, false)
}

// validateFilter 规范化并校验会话筛选条件；strict 为 true 时校验引用的团队、成员、渠道和标签在当前工作区中存在。
func validateFilter(ctx context.Context, db bun.IDB, tenantID string, in Filter, strict bool) (Filter, error) {
	var out Filter

	// 状态多选：校验取值并按固定顺序去重，保证等价条件规范化后一致
	if len(in.Statuses) > 0 {
		seen := make(map[Status]bool, len(in.Statuses))
		for _, s := range in.Statuses {
			if !slices.Contains(Statuses.Values(), s) {
				return Filter{}, ErrInvalidFilter.New(errorx.Field("config.statuses"))
			}
			seen[s] = true
		}
		for _, s := range Statuses.Values() {
			if seen[s] {
				out.Statuses = append(out.Statuses, s)
			}
		}
	}

	// 团队筛选
	if in.Team != nil {
		switch in.Team.Mode {
		case TeamFilterMyTeams, TeamFilterUnassigned:
			out.Team = &TeamFilter{Mode: in.Team.Mode}
		case TeamFilterIDs:
			ids, err := dedupeUUIDs(in.Team.IDs, "config.team.ids")
			if err != nil {
				return Filter{}, err
			}
			if len(ids) == 0 {
				return Filter{}, ErrInvalidFilter.New(errorx.Field("config.team.ids"))
			}
			if strict {
				count, err := db.NewSelect().Table("teams").
					Where("tenant_id = ? AND id IN (?)", tenantID, bun.In(ids)).Count(ctx)
				if err != nil {
					return Filter{}, errors.Wrap(err, "validate filter teams")
				}
				if count != int64(len(ids)) {
					return Filter{}, ErrInvalidFilter.New(errorx.Field("config.team.ids"))
				}
			}
			out.Team = &TeamFilter{Mode: TeamFilterIDs, IDs: ids}
		default:
			return Filter{}, ErrInvalidFilter.New(errorx.Field("config.team.mode"))
		}
	}

	// 负责人筛选
	if in.Assignee != nil {
		switch in.Assignee.Mode {
		case AssigneeFilterMe, AssigneeFilterUnassigned:
			out.Assignee = &AssigneeFilter{Mode: in.Assignee.Mode}
		case AssigneeFilterMember:
			if _, err := uuid.Parse(in.Assignee.MemberID); err != nil {
				return Filter{}, ErrInvalidFilter.New(errorx.Field("config.assignee.member_id"))
			}
			if strict {
				ref := identity.Ref{Type: member.Type, ID: in.Assignee.MemberID}
				if _, err := member.RoleOf(ctx, authz.NewStore(db, auth.Catalog), ref, tenantID); err != nil {
					if errors.Is(err, member.ErrNotFound) {
						return Filter{}, ErrInvalidFilter.New(errorx.Field("config.assignee.member_id"))
					}
					return Filter{}, err
				}
			}
			out.Assignee = &AssigneeFilter{Mode: AssigneeFilterMember, MemberID: in.Assignee.MemberID}
		default:
			return Filter{}, ErrInvalidFilter.New(errorx.Field("config.assignee.mode"))
		}
	}

	// 回复状态
	if in.Waiting != nil {
		w := *in.Waiting
		out.Waiting = &w
	}

	// 优先级
	if in.Priority != "" {
		if !slices.Contains(Priorities.Values(), in.Priority) {
			return Filter{}, ErrInvalidFilter.New(errorx.Field("config.priority"))
		}
		out.Priority = in.Priority
	}

	// 渠道
	if len(in.ChannelIDs) > 0 {
		ids, err := dedupeUUIDs(in.ChannelIDs, "config.channel_ids")
		if err != nil {
			return Filter{}, err
		}
		if strict {
			count, err := db.NewSelect().Model((*channel.Channel)(nil)).
				Where("tenant_id = ? AND id IN (?)", tenantID, bun.In(ids)).Count(ctx)
			if err != nil {
				return Filter{}, errors.Wrap(err, "validate filter channels")
			}
			if count != int64(len(ids)) {
				return Filter{}, ErrInvalidFilter.New(errorx.Field("config.channel_ids"))
			}
		}
		out.ChannelIDs = ids
	}

	// 标签
	if len(in.TagIDs) > 0 {
		ids, err := dedupeUUIDs(in.TagIDs, "config.tag_ids")
		if err != nil {
			return Filter{}, err
		}
		if strict {
			for _, id := range ids {
				if _, err := tag.Find(ctx, db, tenantID, id, false); err != nil {
					if errors.Is(err, tag.ErrNotFound) {
						return Filter{}, ErrInvalidFilter.New(errorx.Field("config.tag_ids"))
					}
					return Filter{}, err
				}
			}
		}
		out.TagIDs = ids
		match := in.TagMatch
		if match == "" {
			match = TagMatchAny
		}
		if !slices.Contains(TagMatches.Values(), match) {
			return Filter{}, ErrInvalidFilter.New(errorx.Field("config.tag_match"))
		}
		out.TagMatch = match
	}

	// 客户语言
	if l := strings.TrimSpace(in.Language); l != "" {
		if canonical, ok := lang.Canonical(l); ok {
			out.Language = canonical
		} else {
			out.Language = l
		}
	}

	// 会话自定义属性条件
	if len(in.Attributes) > 0 {
		defs, err := loadConversationAttrDefs(ctx, db, tenantID)
		if err != nil {
			return Filter{}, err
		}
		out.Attributes = make([]AttributeFilter, 0, len(in.Attributes))
		for _, cond := range in.Attributes {
			key := strings.TrimSpace(cond.Key)
			def, ok := defs[key]
			if !ok {
				if strict {
					return Filter{}, ErrInvalidFilter.New(errorx.Field("config.attributes"))
				}
				continue
			}
			norm, err := normalizeAttrCondition(def, cond)
			if err != nil {
				return Filter{}, err
			}
			out.Attributes = append(out.Attributes, norm)
		}
	}

	return out, nil
}

// loadConversationAttrDefs 加载工作区中全部会话自定义属性定义，按 key 索引。
func loadConversationAttrDefs(ctx context.Context, db bun.IDB, tenantID string) (map[string]attribute.Definition, error) {
	var defs []attribute.Definition
	err := db.NewSelect().Model(&defs).
		Where("tenant_id = ? AND target = ?", tenantID, attribute.TargetConversation).
		Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "load conversation attribute definitions")
	}
	byKey := make(map[string]attribute.Definition, len(defs))
	for _, d := range defs {
		byKey[d.Key] = d
	}
	return byKey, nil
}

// normalizeAttrCondition 按属性定义 def 校验和规范化单条属性筛选条件。
func normalizeAttrCondition(def attribute.Definition, cond AttributeFilter) (AttributeFilter, error) {
	invalid := func() error { return ErrInvalidFilter.New(errorx.Field("config.attributes")) }
	if !slices.Contains(AttributeOps.Values(), cond.Op) {
		return AttributeFilter{}, invalid()
	}
	if cond.Op == AttrOpIsSet || cond.Op == AttrOpIsEmpty {
		return AttributeFilter{Key: def.Key, Op: cond.Op}, nil
	}
	switch def.Type {
	case attribute.TypeText:
		if cond.Op != AttrOpEq && cond.Op != AttrOpNeq && cond.Op != AttrOpContains {
			return AttributeFilter{}, invalid()
		}
		s, ok := cond.Value.(string)
		s = strings.TrimSpace(s)
		if !ok || s == "" || len([]rune(s)) > 200 {
			return AttributeFilter{}, invalid()
		}
		return AttributeFilter{Key: def.Key, Op: cond.Op, Value: s}, nil

	case attribute.TypeNumber:
		if cond.Op != AttrOpEq && cond.Op != AttrOpNeq && cond.Op != AttrOpGt && cond.Op != AttrOpGte && cond.Op != AttrOpLt && cond.Op != AttrOpLte {
			return AttributeFilter{}, invalid()
		}
		n, ok := parseNumber(cond.Value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return AttributeFilter{}, invalid()
		}
		return AttributeFilter{Key: def.Key, Op: cond.Op, Value: n}, nil

	case attribute.TypeBoolean:
		if cond.Op != AttrOpEq && cond.Op != AttrOpNeq {
			return AttributeFilter{}, invalid()
		}
		b, ok := cond.Value.(bool)
		if !ok {
			return AttributeFilter{}, invalid()
		}
		return AttributeFilter{Key: def.Key, Op: cond.Op, Value: b}, nil

	case attribute.TypeDate:
		if cond.Op != AttrOpEq && cond.Op != AttrOpNeq && cond.Op != AttrOpGt && cond.Op != AttrOpGte && cond.Op != AttrOpLt && cond.Op != AttrOpLte {
			return AttributeFilter{}, invalid()
		}
		s, ok := cond.Value.(string)
		if !ok {
			return AttributeFilter{}, invalid()
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return AttributeFilter{}, invalid()
		}
		return AttributeFilter{Key: def.Key, Op: cond.Op, Value: s}, nil

	case attribute.TypeSelect:
		switch cond.Op {
		case AttrOpEq, AttrOpNeq:
			s, ok := cond.Value.(string)
			if !ok || s == "" || !slices.Contains(def.Options, s) {
				return AttributeFilter{}, invalid()
			}
			return AttributeFilter{Key: def.Key, Op: cond.Op, Value: s}, nil
		case AttrOpIn:
			vals, ok := parseStrings(cond.Value)
			if !ok || len(vals) == 0 {
				return AttributeFilter{}, invalid()
			}
			for _, v := range vals {
				if !slices.Contains(def.Options, v) {
					return AttributeFilter{}, invalid()
				}
			}
			return AttributeFilter{Key: def.Key, Op: AttrOpIn, Value: vals}, nil
		default:
			return AttributeFilter{}, invalid()
		}

	case attribute.TypeMultiSelect:
		switch cond.Op {
		case AttrOpEq:
			s, ok := cond.Value.(string)
			if !ok || s == "" || !slices.Contains(def.Options, s) {
				return AttributeFilter{}, invalid()
			}
			return AttributeFilter{Key: def.Key, Op: AttrOpIn, Value: []string{s}}, nil
		case AttrOpIn:
			vals, ok := parseStrings(cond.Value)
			if !ok || len(vals) == 0 {
				return AttributeFilter{}, invalid()
			}
			for _, v := range vals {
				if !slices.Contains(def.Options, v) {
					return AttributeFilter{}, invalid()
				}
			}
			return AttributeFilter{Key: def.Key, Op: AttrOpIn, Value: vals}, nil
		default:
			return AttributeFilter{}, invalid()
		}
	default:
		return AttributeFilter{}, invalid()
	}
}

// buildFilterPredicate 将统一 Filter 编译为针对 conversations（别名 conversation）的参数化 SQL 布尔表达式。
// 该表达式同时供 List、Search 的 WHERE 子句以及 GetInbox 的单次扫描 FILTER (WHERE ...) 聚合复用，保证统计与列表口径绝对一致。
func buildFilterPredicate(sub identity.Subject, f Filter, attrDefs map[string]attribute.Definition) (string, []any) {
	var parts []string
	var args []any

	if len(f.Statuses) > 0 {
		parts = append(parts, "conversation.status IN (?)")
		args = append(args, bun.In(f.Statuses))
	}

	if f.Team != nil {
		switch f.Team.Mode {
		case TeamFilterMyTeams:
			if sub.Type == member.Type {
				parts = append(parts, "conversation.team_id IN (SELECT team_id FROM team_members WHERE member_id = ?)")
				args = append(args, sub.ID)
			} else {
				parts = append(parts, "FALSE")
			}
		case TeamFilterUnassigned:
			parts = append(parts, "conversation.team_id IS NULL")
		case TeamFilterIDs:
			if len(f.Team.IDs) > 0 {
				parts = append(parts, "conversation.team_id IN (?)")
				args = append(args, bun.In(f.Team.IDs))
			} else {
				parts = append(parts, "FALSE")
			}
		}
	}

	if f.Assignee != nil {
		switch f.Assignee.Mode {
		case AssigneeFilterMe:
			if sub.Type == member.Type {
				parts = append(parts, "(conversation.assignee_type = ? AND conversation.assignee_id = ?)")
				args = append(args, MemberActor, sub.ID)
			} else {
				parts = append(parts, "FALSE")
			}
		case AssigneeFilterUnassigned:
			parts = append(parts, "conversation.assignee_type = ''")
		case AssigneeFilterMember:
			parts = append(parts, "(conversation.assignee_type = ? AND conversation.assignee_id = ?)")
			args = append(args, MemberActor, f.Assignee.MemberID)
		}
	}

	if f.Waiting != nil {
		if *f.Waiting {
			parts = append(parts, "conversation.waiting_since IS NOT NULL")
		} else {
			parts = append(parts, "conversation.waiting_since IS NULL")
		}
	}

	if f.Priority != "" {
		parts = append(parts, "conversation.priority = ?")
		args = append(args, f.Priority)
	}

	if len(f.ChannelIDs) > 0 {
		parts = append(parts, "(conversation.channel_id IN (?) OR conversation.reply_channel_id IN (?) OR EXISTS (SELECT 1 FROM timeline_items AS ti WHERE ti.conversation_id = conversation.id AND ti.channel_id IN (?)))")
		args = append(args, bun.In(f.ChannelIDs), bun.In(f.ChannelIDs), bun.In(f.ChannelIDs))
	}

	if len(f.TagIDs) > 0 {
		if f.TagMatch == TagMatchAll && len(f.TagIDs) > 1 {
			parts = append(parts, "(SELECT count(DISTINCT ct.tag_id) FROM conversation_tags AS ct WHERE ct.tenant_id = ? AND ct.conversation_id = conversation.id AND ct.tag_id IN (?)) = ?")
			args = append(args, sub.Tenant, bun.In(f.TagIDs), len(f.TagIDs))
		} else {
			parts = append(parts, "EXISTS (SELECT 1 FROM conversation_tags AS ct WHERE ct.tenant_id = ? AND ct.conversation_id = conversation.id AND ct.tag_id IN (?))")
			args = append(args, sub.Tenant, bun.In(f.TagIDs))
		}
	}

	if l := strings.TrimSpace(f.Language); l != "" {
		if canonical, ok := lang.Canonical(l); ok {
			l = canonical
		}
		if !strings.Contains(l, "-") {
			parts = append(parts, "(conversation.language = ? OR conversation.language LIKE ?)")
			args = append(args, l, l+"-%")
		} else {
			parts = append(parts, "conversation.language = ?")
			args = append(args, l)
		}
	}

	for _, cond := range f.Attributes {
		def, ok := attrDefs[cond.Key]
		if !ok {
			// 已保存视图引用的属性若后来被删除，容错返回 FALSE（或 is_empty 返回 TRUE），不导致视图报错
			if cond.Op == AttrOpIsEmpty {
				continue
			}
			parts = append(parts, "FALSE")
			continue
		}
		expr, a := buildAttrPredicate(def, cond)
		if expr != "" {
			parts = append(parts, expr)
			args = append(args, a...)
		}
	}

	if len(parts) == 0 {
		return "TRUE", nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", args
}

// buildAttrPredicate 编译单条会话自定义属性的 SQL 谓词，所有键名和值均通过参数绑定传入。
func buildAttrPredicate(def attribute.Definition, cond AttributeFilter) (string, []any) {
	k := def.Key
	if cond.Op == AttrOpIsSet {
		return "jsonb_exists(conversation.attributes, ?)", []any{k}
	}
	if cond.Op == AttrOpIsEmpty {
		return "NOT jsonb_exists(conversation.attributes, ?)", []any{k}
	}

	switch def.Type {
	case attribute.TypeText:
		s, ok := cond.Value.(string)
		if !ok {
			return "FALSE", nil
		}
		switch cond.Op {
		case AttrOpEq:
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) = ?)", []any{k, k, s}
		case AttrOpNeq:
			return "(NOT jsonb_exists(conversation.attributes, ?) OR (conversation.attributes->>?) <> ?)", []any{k, k, s}
		case AttrOpContains:
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) ILIKE ? ESCAPE '\\')", []any{k, k, "%" + database.EscapeLike(s) + "%"}
		}

	case attribute.TypeNumber:
		n, ok := parseNumber(cond.Value)
		if !ok {
			return "FALSE", nil
		}
		numExpr := "(CASE WHEN jsonb_typeof(conversation.attributes->?) = 'number' THEN (conversation.attributes->>?)::double precision END)"
		switch cond.Op {
		case AttrOpEq:
			return "(" + numExpr + " = ?)", []any{k, k, n}
		case AttrOpNeq:
			return "(jsonb_typeof(conversation.attributes->?) <> 'number' OR (conversation.attributes->>?)::double precision <> ?)", []any{k, k, n}
		case AttrOpGt:
			return "(" + numExpr + " > ?)", []any{k, k, n}
		case AttrOpGte:
			return "(" + numExpr + " >= ?)", []any{k, k, n}
		case AttrOpLt:
			return "(" + numExpr + " < ?)", []any{k, k, n}
		case AttrOpLte:
			return "(" + numExpr + " <= ?)", []any{k, k, n}
		}

	case attribute.TypeBoolean:
		b, ok := cond.Value.(bool)
		if !ok {
			return "FALSE", nil
		}
		switch cond.Op {
		case AttrOpEq:
			return "(jsonb_typeof(conversation.attributes->?) = 'boolean' AND (conversation.attributes->>?)::boolean = ?)", []any{k, k, b}
		case AttrOpNeq:
			return "(jsonb_typeof(conversation.attributes->?) <> 'boolean' OR (conversation.attributes->>?)::boolean <> ?)", []any{k, k, b}
		}

	case attribute.TypeDate:
		s, ok := cond.Value.(string)
		if !ok {
			return "FALSE", nil
		}
		switch cond.Op {
		case AttrOpEq:
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) = ?)", []any{k, k, s}
		case AttrOpNeq:
			return "(NOT jsonb_exists(conversation.attributes, ?) OR (conversation.attributes->>?) <> ?)", []any{k, k, s}
		case AttrOpGt:
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) > ?)", []any{k, k, s}
		case AttrOpGte:
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) >= ?)", []any{k, k, s}
		case AttrOpLt:
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) < ?)", []any{k, k, s}
		case AttrOpLte:
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) <= ?)", []any{k, k, s}
		}

	case attribute.TypeSelect:
		switch cond.Op {
		case AttrOpEq:
			s, ok := cond.Value.(string)
			if !ok {
				return "FALSE", nil
			}
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) = ?)", []any{k, k, s}
		case AttrOpNeq:
			s, ok := cond.Value.(string)
			if !ok {
				return "FALSE", nil
			}
			return "(NOT jsonb_exists(conversation.attributes, ?) OR (conversation.attributes->>?) <> ?)", []any{k, k, s}
		case AttrOpIn:
			vals, ok := parseStrings(cond.Value)
			if !ok || len(vals) == 0 {
				return "FALSE", nil
			}
			return "(jsonb_exists(conversation.attributes, ?) AND (conversation.attributes->>?) IN (?))", []any{k, k, bun.In(vals)}
		}

	case attribute.TypeMultiSelect:
		vals, ok := parseStrings(cond.Value)
		if !ok {
			if s, ok := cond.Value.(string); ok && s != "" {
				vals = []string{s}
			}
		}
		if len(vals) == 0 {
			return "FALSE", nil
		}
		return "EXISTS (SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(conversation.attributes->?) = 'array' THEN conversation.attributes->? ELSE '[]'::jsonb END) AS elem WHERE elem IN (?))",
			[]any{k, k, bun.In(vals)}
	}
	return "FALSE", nil
}

// dedupeUUIDs 校验并按顺序去重 UUID 字符串列表。
func dedupeUUIDs(raw []string, field string) ([]string, error) {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		if _, err := uuid.Parse(id); err != nil {
			return nil, ErrInvalidFilter.New(errorx.Field(field))
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// parseNumber 把 JSON 解码后的数值统一转为 float64。
func parseNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// parseStrings 把 []string 或 JSON 解码出的 []any 规范化去重为 []string。
func parseStrings(v any) ([]string, bool) {
	var raw []string
	switch x := v.(type) {
	case []string:
		raw = x
	case []any:
		raw = make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			raw = append(raw, s)
		}
	default:
		return nil, false
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, true
}
