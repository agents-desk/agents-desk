package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// TicketNotifyTarget 是工单公开进度或状态变更通知的客户侧投递目标：
// 按联系人去重，每个关联客户仅投递一次，优先落到该客户当前的活跃服务型会话。
type TicketNotifyTarget struct {
	ConversationID string
	ContactID      string
	Language       string
}

// ResolveTicketNotifyTargets 解析工单 ticketID 对外通知的全部服务型会话目标：
//   - 查出工单在 conversation_links 中关联的全部服务型会话；
//   - 对带有 contact_id 的关联会话按 contact_id 去重，确保同一客户即使多条历史会话都关联了该工单也只收到一次通知；
//   - 对每个客户，优先选择其当前处于 open/snoozed 的唯一活跃服务型会话，无活跃会话时回退到与该工单关联的最新一条服务型会话；
//   - 保留无 contact_id 的特殊服务型会话原样投递。
func ResolveTicketNotifyTargets(ctx context.Context, db bun.IDB, tenantID, ticketID string) ([]TicketNotifyTarget, error) {
	var linked []struct {
		ConversationID string  `bun:"conversation_id"`
		ContactID      *string `bun:"contact_id"`
		Language       string  `bun:"language"`
	}
	err := db.NewSelect().TableExpr("conversation_links AS cl").
		Join("JOIN conversations AS sc ON sc.id = cl.conversation_id").
		Join("LEFT JOIN contacts AS ct ON ct.id = sc.contact_id AND ct.tenant_id = sc.tenant_id").
		ColumnExpr("sc.id AS conversation_id, sc.contact_id, COALESCE(NULLIF(ct.language, ''), sc.language, '') AS language").
		Where("cl.tenant_id = ? AND cl.ticket_id = ? AND sc.tenant_id = ? AND sc.kind = ?",
			tenantID, ticketID, tenantID, Service).
		OrderExpr("sc.last_message_at DESC, sc.id DESC").
		Scan(ctx, &linked)
	if err != nil {
		return nil, errors.Wrap(err, "load linked conversations for ticket notify")
	}
	if len(linked) == 0 {
		return nil, nil
	}

	// 按 contact_id 去重，保留每名客户与工单关联的最新一条服务型会话作为回退目标
	seenContacts := map[string]bool{}
	contactIDs := make([]string, 0, len(linked))
	fallback := make([]TicketNotifyTarget, 0, len(linked))
	for _, row := range linked {
		if row.ContactID == nil || *row.ContactID == "" {
			fallback = append(fallback, TicketNotifyTarget{
				ConversationID: row.ConversationID,
				Language:       row.Language,
			})
			continue
		}
		cid := *row.ContactID
		if seenContacts[cid] {
			continue
		}
		seenContacts[cid] = true
		contactIDs = append(contactIDs, cid)
		fallback = append(fallback, TicketNotifyTarget{
			ConversationID: row.ConversationID,
			ContactID:      cid,
			Language:       row.Language,
		})
	}

	// 查询这些客户当前正在进行中或稍后处理的唯一活跃服务型会话，有则覆盖为投递目标
	activeByContact := map[string]string{}
	if len(contactIDs) > 0 {
		var activeRows []struct {
			ID        string `bun:"id"`
			ContactID string `bun:"contact_id"`
		}
		err := db.NewSelect().Table("conversations").
			Column("id", "contact_id").
			Where("tenant_id = ? AND kind = ? AND contact_id IN (?) AND status IN (?)",
				tenantID, Service, bun.In(contactIDs), bun.In([]Status{Open, Snoozed})).
			OrderExpr("last_message_at DESC, id DESC").
			Scan(ctx, &activeRows)
		if err != nil {
			return nil, errors.Wrap(err, "load active conversations for ticket notify")
		}
		for _, r := range activeRows {
			if _, exists := activeByContact[r.ContactID]; !exists {
				activeByContact[r.ContactID] = r.ID
			}
		}
	}

	out := make([]TicketNotifyTarget, 0, len(fallback))
	for _, item := range fallback {
		if item.ContactID != "" {
			if activeID, ok := activeByContact[item.ContactID]; ok {
				item.ConversationID = activeID
			}
		}
		out = append(out, item)
	}
	return out, nil
}
