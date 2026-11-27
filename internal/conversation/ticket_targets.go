package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// TicketNotifyTarget 是工单公开进度或状态变更通知的对外投递目标：
// 按关联联系人或外部群一对一解析，优先落到该对象当前的活跃服务型会话。
type TicketNotifyTarget struct {
	ConversationID  string
	ContactID       string
	ExternalGroupID string
	Language        string
}

// ResolveTicketNotifyTargets 解析工单 ticketID 对外通知的全部服务型会话目标（联系人与外部群）：
//   - 查出 ticket_targets 中与工单关联的全部联系人及外部群及其语言；
//   - 为每个对象优先挑选当前处于 open/snoozed 的活跃服务型会话，其次按最近消息倒序回退；
//   - 尚无任何服务型会话的对象跳过。
func ResolveTicketNotifyTargets(ctx context.Context, db bun.IDB, tenantID, ticketID string) ([]TicketNotifyTarget, error) {
	var rows []struct {
		ContactID            *string `bun:"contact_id"`
		ExternalGroupID      *string `bun:"external_group_id"`
		SourceConversationID *string `bun:"source_conversation_id"`
		ContactLanguage      string  `bun:"contact_language"`
		GroupLanguage        string  `bun:"group_language"`
	}
	err := db.NewSelect().TableExpr("ticket_targets AS tc").
		Join("LEFT JOIN contacts AS ct ON ct.id = tc.contact_id AND ct.tenant_id = tc.tenant_id").
		Join("LEFT JOIN external_groups AS eg ON eg.id = tc.external_group_id AND eg.tenant_id = tc.tenant_id").
		ColumnExpr("tc.contact_id, tc.external_group_id, tc.source_conversation_id, COALESCE(ct.language, '') AS contact_language, COALESCE(eg.language, '') AS group_language").
		Where("tc.tenant_id = ? AND tc.ticket_id = ?", tenantID, ticketID).
		OrderExpr("tc.created_at ASC, tc.id ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load ticket targets for notify")
	}
	if len(rows) == 0 {
		return nil, nil
	}

	var contactIDs, groupIDs []string
	for _, r := range rows {
		if r.ContactID != nil && *r.ContactID != "" {
			contactIDs = append(contactIDs, *r.ContactID)
		} else if r.ExternalGroupID != nil && *r.ExternalGroupID != "" {
			groupIDs = append(groupIDs, *r.ExternalGroupID)
		}
	}

	bestByContact := map[string]struct{ id, lang string }{}
	if len(contactIDs) > 0 {
		var convRows []struct {
			ID        string `bun:"id"`
			ContactID string `bun:"contact_id"`
			Language  string `bun:"language"`
		}
		err = db.NewRaw(`SELECT DISTINCT ON (contact_id) id, contact_id, COALESCE(language, '') AS language
			FROM conversations
			WHERE tenant_id = ? AND kind = ? AND contact_id IN (?)
			ORDER BY contact_id, (status IN ('open', 'snoozed')) DESC, last_message_at DESC, id DESC`,
			tenantID, Service, bun.In(contactIDs)).Scan(ctx, &convRows)
		if err != nil {
			return nil, errors.Wrap(err, "load contact target conversations for ticket notify")
		}
		for _, r := range convRows {
			bestByContact[r.ContactID] = struct{ id, lang string }{id: r.ID, lang: r.Language}
		}
	}

	bestByGroup := map[string]struct{ id, lang string }{}
	if len(groupIDs) > 0 {
		var convRows []struct {
			ID              string `bun:"id"`
			ExternalGroupID string `bun:"external_group_id"`
			Language        string `bun:"language"`
		}
		err = db.NewRaw(`SELECT DISTINCT ON (external_group_id) id, external_group_id, COALESCE(language, '') AS language
			FROM conversations
			WHERE tenant_id = ? AND kind = ? AND external_group_id IN (?)
			ORDER BY external_group_id, (status IN ('open', 'snoozed')) DESC, last_message_at DESC, id DESC`,
			tenantID, Service, bun.In(groupIDs)).Scan(ctx, &convRows)
		if err != nil {
			return nil, errors.Wrap(err, "load group target conversations for ticket notify")
		}
		for _, r := range convRows {
			bestByGroup[r.ExternalGroupID] = struct{ id, lang string }{id: r.ID, lang: r.Language}
		}
	}

	out := make([]TicketNotifyTarget, 0, len(rows))
	for _, r := range rows {
		if r.ContactID != nil && *r.ContactID != "" {
			best, ok := bestByContact[*r.ContactID]
			if !ok {
				continue
			}
			lang := r.ContactLanguage
			if lang == "" {
				lang = best.lang
			}
			out = append(out, TicketNotifyTarget{
				ConversationID: best.id,
				ContactID:      *r.ContactID,
				Language:       lang,
			})
		} else if r.ExternalGroupID != nil && *r.ExternalGroupID != "" {
			best, ok := bestByGroup[*r.ExternalGroupID]
			if !ok {
				continue
			}
			lang := r.GroupLanguage
			if lang == "" {
				lang = best.lang
			}
			out = append(out, TicketNotifyTarget{
				ConversationID:  best.id,
				ExternalGroupID: *r.ExternalGroupID,
				Language:        lang,
			})
		}
	}
	return out, nil
}
