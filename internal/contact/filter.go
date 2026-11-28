package contact

import (
	"strings"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"

	"agents-desk/internal/lang"
)

// Filter 是联系人列表、保存的筛选视图和联系人导出共用的筛选条件。
type Filter struct {
	Kind           Kind   `json:"kind,omitempty" doc:"按联系人类型筛选：user 或 lead；为空表示全部"`
	Q              string `json:"q,omitempty" maxLength:"100" doc:"按名字、邮箱、外部用户编号或工作区内编号（如 #128）搜索"`
	Language       string `json:"language,omitempty" maxLength:"35" doc:"按客户的语言（BCP 47）筛选，传主语言时同时匹配带子标签的写法"`
	ChannelID      string `json:"channel_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按渠道筛选"`
	TagID          string `json:"tag_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按标签筛选"`
	OrganizationID string `json:"organization_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按所属客户组织筛选"`
}

// applyFilter 把筛选条件 f 叠加到联系人查询 q 上，q 中联系人表的别名是 contact。
// 未显式指定 Kind 时默认返回外部客户（user + lead），与内部联系人（internal）分组隔离。
func applyFilter(q *bun.SelectQuery, f Filter) *bun.SelectQuery {
	if f.Kind != "" {
		q = q.Where("contact.kind = ?", f.Kind)
	} else {
		q = q.Where("contact.kind <> ?", Internal)
	}
	if kw := strings.TrimSpace(f.Q); kw != "" {
		like := "%" + database.EscapeLike(kw) + "%"
		num, hasNum := parseNumberQuery(kw)
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			q = q.Where(`contact.name ILIKE ? ESCAPE '\'`, like).
				WhereOr(`contact.email ILIKE ? ESCAPE '\'`, like).
				WhereOr(`contact.external_user_id ILIKE ? ESCAPE '\'`, like)
			if hasNum {
				q = q.WhereOr("contact.number = ?", num)
			}
			return q
		})
	}
	if l := strings.TrimSpace(f.Language); l != "" {
		if canonical, ok := lang.Canonical(l); ok {
			l = canonical
		}
		if !strings.Contains(l, "-") {
			q = q.Where("(contact.language = ? OR contact.language LIKE ?)", l, l+"-%")
		} else {
			q = q.Where("contact.language = ?", l)
		}
	}
	if f.ChannelID != "" {
		q = q.Where(
			"EXISTS (SELECT 1 FROM contact_identities ci WHERE ci.tenant_id = contact.tenant_id AND ci.contact_id = contact.id AND ci.channel_id = ?)",
			f.ChannelID,
		)
	}
	if f.TagID != "" {
		q = q.Where(
			"EXISTS (SELECT 1 FROM contact_tags ct WHERE ct.tenant_id = contact.tenant_id AND ct.contact_id = contact.id AND ct.tag_id = ?)",
			f.TagID,
		)
	}
	if f.OrganizationID != "" {
		q = q.Where("contact.organization_id = ?", f.OrganizationID)
	}
	return q
}

// mergeFilter 把视图的默认筛选 base 与用户临时覆盖 override 合并（非空覆盖）。
func mergeFilter(base, override Filter) Filter {
	out := base
	if override.Kind != "" {
		out.Kind = override.Kind
	}
	if strings.TrimSpace(override.Q) != "" {
		out.Q = override.Q
	}
	if strings.TrimSpace(override.Language) != "" {
		out.Language = override.Language
	}
	if override.ChannelID != "" {
		out.ChannelID = override.ChannelID
	}
	if override.TagID != "" {
		out.TagID = override.TagID
	}
	if override.OrganizationID != "" {
		out.OrganizationID = override.OrganizationID
	}
	return out
}
