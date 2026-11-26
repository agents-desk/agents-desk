package translation

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
	"agents-desk/internal/lang"
	"agents-desk/internal/llm"
	"agents-desk/internal/tickettype"
)

// TicketBroadcastTopic 是工单公开进度向关联服务型会话同步的下游主题。
var TicketBroadcastTopic = queue.Topic[conversation.Appended]("ticket.broadcast")

// TicketBroadcast 消费客户可见工单上的公开进度消息：按关联服务型会话的客户语言去重批量翻译，
// 再分别写入各关联服务型会话并经其活跃出站渠道（c.DeliveryChannelID()）投递，不改变会话负责人。
type TicketBroadcast struct {
	db         *bun.DB
	locker     *lock.Locker
	translator *Translator
	sendMsg    *conversation.SendSystemMessage
}

// NewTicketBroadcast 返回工单公开进度广播处理器。
func NewTicketBroadcast(db *bun.DB, locker *lock.Locker, translator *Translator, down conversation.Downstream) *TicketBroadcast {
	return &TicketBroadcast{
		db:         db,
		locker:     locker,
		translator: translator,
		sendMsg:    conversation.NewSendSystemMessage(db, down),
	}
}

// Job 处理 TicketBroadcastTopic 的队列任务。
func (b *TicketBroadcast) Job(ctx context.Context, p conversation.Appended) error {
	return b.Handle(ctx, p)
}

// Handle 读取工单时间线项 p.ItemID：仅当它属于客户可见工单（kind=ticket, visibility=all）且为公开进度消息
// （kind=message, visibility=all, actor!=contact）时，按关联服务型会话的客户语言去重翻译并逐一投递。
// 通过检查目标会话中已写入的 ticket_update 提示实现幂等去重，重试时只补发尚未送达的会话。
func (b *TicketBroadcast) Handle(ctx context.Context, p conversation.Appended) error {
	var it conversation.Item
	err := b.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load ticket update item")
	}
	if it.Kind != conversation.Message || it.Visibility != conversation.Everyone || it.ActorType == conversation.ContactActor {
		return nil
	}

	var tk struct {
		Kind       conversation.Kind     `bun:"kind"`
		Number     int64                 `bun:"number"`
		Title      string                `bun:"title"`
		Visibility tickettype.Visibility `bun:"visibility"`
	}
	err = b.db.NewSelect().TableExpr("conversations c").
		Join("JOIN tickets t ON t.conversation_id = c.id").
		ColumnExpr("c.kind, t.number, t.title, t.visibility").
		Where("c.id = ? AND c.tenant_id = ?", it.ConversationID, p.TenantID).
		Scan(ctx, &tk)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load ticket for broadcast")
	}
	if tk.Kind != conversation.Ticket || tk.Visibility != tickettype.VisibilityAll {
		return nil
	}

	if b.locker != nil {
		lk, err := b.locker.Wait(ctx, "ticket_broadcast:"+it.ID, lockTTL, lockWait)
		if err != nil {
			return errors.Wrap(err, "lock ticket broadcast")
		}
		defer func() { _ = lk.Release(context.WithoutCancel(ctx)) }()
	}

	var targets []broadcastTarget
	err = b.db.NewSelect().TableExpr("conversation_links cl").
		Join("JOIN conversations sc ON sc.id = cl.conversation_id").
		Join("LEFT JOIN contacts ct ON ct.id = sc.contact_id AND ct.tenant_id = sc.tenant_id").
		ColumnExpr("sc.id, COALESCE(NULLIF(ct.language, ''), sc.language, '') AS language").
		Where("cl.tenant_id = ? AND cl.ticket_id = ? AND sc.tenant_id = ? AND sc.kind = ?",
			p.TenantID, it.ConversationID, p.TenantID, conversation.Service).
		OrderExpr("cl.created_at ASC, sc.id ASC").
		Scan(ctx, &targets)
	if err != nil {
		return errors.Wrap(err, "load linked conversations for broadcast")
	}
	if len(targets) == 0 {
		return nil
	}

	// 跳过已投递过该条公开进度的关联会话，保证队列重试幂等
	var alreadySent []string
	err = b.db.NewRaw(`SELECT DISTINCT conversation_id FROM timeline_items
		WHERE tenant_id = ? AND conversation_id IN (?) AND content->'prompt'->>'kind' = 'ticket_update'
		AND content->'prompt'->'params'->>'item_id' = ?`, p.TenantID, bun.In(convIDsOf(targets)), it.ID).Scan(ctx, &alreadySent)
	if err != nil {
		return errors.Wrap(err, "check sent ticket updates")
	}
	sentSet := make(map[string]bool, len(alreadySent))
	for _, cid := range alreadySent {
		sentSet[cid] = true
	}
	pending := make([]broadcastTarget, 0, len(targets))
	for _, tc := range targets {
		if !sentSet[tc.ID] {
			pending = append(pending, tc)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	// 汇总待投递会话的去重客户语言，按语言一次批量翻译
	rawText := strings.TrimSpace(it.Content.Text)
	if rawText == "" {
		rawText = fmt.Sprintf("工单 #%d 有新的公开进度，请在工单详情中查看。", tk.Number)
	}
	langSet := map[string]struct{}{}
	for _, tc := range pending {
		if l := strings.TrimSpace(tc.Language); l != "" {
			langSet[l] = struct{}{}
		}
	}
	uniqueLangs := make([]string, 0, len(langSet))
	for l := range langSet {
		uniqueLangs = append(uniqueLangs, l)
	}
	sort.Strings(uniqueLangs)

	var (
		sourceLang   string
		translations map[string]string
	)
	if b.translator != nil && len(uniqueLangs) > 0 && strings.TrimSpace(it.Content.Text) != "" {
		var authorLang string
		if it.ActorType == conversation.MemberActor && it.ActorID != "" {
			_ = b.db.NewSelect().Table("members").Column("language").Where("id = ?", it.ActorID).Scan(ctx, &authorLang)
		}
		detected, translatedMap, err := b.translator.Incoming(ctx, p.TenantID, &it, authorLang, uniqueLangs)
		if errors.Is(err, llm.ErrNotConfigured) {
			slog.DebugContext(ctx, "ticket broadcast translation not configured, sending original text", "item", it.ID)
		} else if err != nil {
			return err
		} else {
			sourceLang = detected
			translations = translatedMap
		}
	}

	sys := identity.Subject{Type: identity.System, ID: "ticket-broadcast", Tenant: p.TenantID}
	prompt := &conversation.Prompt{
		Kind: "ticket_update",
		Params: map[string]any{
			"ticket_id": it.ConversationID,
			"number":    tk.Number,
			"title":     tk.Title,
			"item_id":   it.ID,
		},
	}

	for _, tc := range pending {
		outText := rawText
		original := ""
		outLang := ""
		if targetLang := strings.TrimSpace(tc.Language); targetLang != "" && len(translations) > 0 {
			for k, v := range translations {
				if lang.Same(k, targetLang) && strings.TrimSpace(v) != "" && !lang.Same(sourceLang, targetLang) {
					outText = v
					original = rawText
					outLang = targetLang
					break
				}
			}
		}
		if _, err := b.sendMsg.HandleTranslated(ctx, sys, tc.ID, outText, original, outLang, prompt); err != nil {
			return errors.Wrapf(err, "broadcast ticket update to conversation %s", tc.ID)
		}
	}
	slog.InfoContext(ctx, "ticket public update broadcasted",
		"ticket_id", it.ConversationID, "item_id", it.ID, "recipients", len(pending), "translated_languages", len(translations))
	return nil
}

// broadcastTarget 是工单公开进度需要同步投递的一条关联服务型会话及其活跃客户语言。
type broadcastTarget struct {
	ID       string `bun:"id"`
	Language string `bun:"language"`
}

// convIDsOf 从目标会话切片提取 ID 列表。
func convIDsOf(items []broadcastTarget) []string {
	ids := make([]string, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	return ids
}
