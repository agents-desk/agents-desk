package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// TicketNotifyTarget 是工单公开进度或状态变更通知的客户侧投递目标：
// 按关联联系人一对一解析，优先落到该客户当前的活跃服务型会话。
type TicketNotifyTarget struct {
	ConversationID string
	ContactID      string
	Language       string
}

// ResolveTicketNotifyTargets 解析工单 ticketID 对外通知的全部客户服务型会话目标：
//   - 查出 ticket_contacts 中与工单关联的全部联系人及其语言；
//   - 为每个联系人优先挑选当前处于 open/snoozed 的唯一活跃服务型会话；
//   - 若无活跃会话，回退到该联系人的来源会话（若存在）或最近一条服务型会话；尚无任何服务型会话的联系人跳过。
func ResolveTicketNotifyTargets(ctx context.Context, db bun.IDB, tenantID, ticketID string) ([]TicketNotifyTarget, error) {
	var rows []struct {
		ContactID            string  `bun:"contact_id"`
		SourceConversationID *string `bun:"source_conversation_id"`
		Language             string  `bun:"language"`
	}
	err := db.NewSelect().TableExpr("ticket_contacts AS tc").
		Join("JOIN contacts AS ct ON ct.id = tc.contact_id AND ct.tenant_id = tc.tenant_id").
		ColumnExpr("tc.contact_id, tc.source_conversation_id, COALESCE(ct.language, '') AS language").
		Where("tc.tenant_id = ? AND tc.ticket_id = ?", tenantID, ticketID).
		OrderExpr("tc.created_at ASC, tc.id ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load ticket contacts for notify")
	}
	if len(rows) == 0 {
		return nil, nil
	}

	contactIDs := make([]string, len(rows))
	for i, r := range rows {
		contactIDs[i] = r.ContactID
	}

	// 一次查出所有关联联系人的最佳投递会话：活跃会话（open/snoozed）优先，其次按最近消息倒序
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
		return nil, errors.Wrap(err, "load target conversations for ticket notify")
	}
	bestByContact := make(map[string]struct{ id, lang string }, len(convRows))
	for _, r := range convRows {
		bestByContact[r.ContactID] = struct{ id, lang string }{id: r.ID, lang: r.Language}
	}

	out := make([]TicketNotifyTarget, 0, len(rows))
	for _, r := range rows {
		best, ok := bestByContact[r.ContactID]
		if !ok {
			continue
		}
		lang := r.Language
		if lang == "" {
			lang = best.lang
		}
		out = append(out, TicketNotifyTarget{
			ConversationID: best.id,
			ContactID:      r.ContactID,
			Language:       lang,
		})
	}
	return out, nil
}
