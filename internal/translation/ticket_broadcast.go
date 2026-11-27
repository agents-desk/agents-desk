package translation

import (
	"context"
	"database/sql"
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
	down       conversation.Downstream
	sendMsg    *conversation.SendSystemMessage
}

// NewTicketBroadcast 返回工单公开进度广播处理器。
func NewTicketBroadcast(db *bun.DB, locker *lock.Locker, translator *Translator, down conversation.Downstream) *TicketBroadcast {
	return &TicketBroadcast{
		db:         db,
		locker:     locker,
		translator: translator,
		down:       down,
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
	if it.Kind != conversation.Message || it.Visibility != conversation.Everyone || it.ActorType.IsExternal() {
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

	targets, err := conversation.ResolveTicketNotifyTargets(ctx, b.db, p.TenantID, it.ConversationID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return conversation.SaveTicketBroadcastCount(ctx, b.db, b.down, p.TenantID, it.ID, 0)
	}

	// 跳过已投递过该条公开进度的关联联系人、外部群或会话，保证队列重试幂等且不重复打扰
	var sentRows []struct {
		ConversationID  string  `bun:"conversation_id"`
		ContactID       *string `bun:"contact_id"`
		ExternalGroupID *string `bun:"external_group_id"`
	}
	err = b.db.NewRaw(`SELECT DISTINCT ti.conversation_id, c.contact_id, c.external_group_id
		FROM timeline_items ti
		JOIN conversations c ON c.id = ti.conversation_id
		WHERE ti.tenant_id = ? AND ti.content->'prompt'->>'kind' = 'ticket_update'
		  AND ti.content->'prompt'->'params'->>'item_id' = ?`, p.TenantID, it.ID).Scan(ctx, &sentRows)
	if err != nil {
		return errors.Wrap(err, "check sent ticket updates")
	}
	sentConv := make(map[string]bool, len(sentRows))
	sentContact := make(map[string]bool, len(sentRows))
	sentGroup := make(map[string]bool, len(sentRows))
	for _, r := range sentRows {
		sentConv[r.ConversationID] = true
		if r.ContactID != nil && *r.ContactID != "" {
			sentContact[*r.ContactID] = true
		}
		if r.ExternalGroupID != nil && *r.ExternalGroupID != "" {
			sentGroup[*r.ExternalGroupID] = true
		}
	}
	pending := make([]conversation.TicketNotifyTarget, 0, len(targets))
	for _, tc := range targets {
		if sentConv[tc.ConversationID] ||
			(tc.ContactID != "" && sentContact[tc.ContactID]) ||
			(tc.ExternalGroupID != "" && sentGroup[tc.ExternalGroupID]) {
			continue
		}
		pending = append(pending, tc)
	}
	if len(pending) == 0 {
		return conversation.SaveTicketBroadcastCount(ctx, b.db, b.down, p.TenantID, it.ID, len(sentConv))
	}

	attachments, err := conversation.LoadItemAttachments(ctx, b.db, p.TenantID, it.ID)
	if err != nil {
		return err
	}
	rawText := strings.TrimSpace(it.Content.Text)
	if rawText == "" && len(attachments) == 0 {
		return nil
	}

	// 汇总待投递会话的去重客户语言，按语言一次批量翻译
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
		translations = map[string]string{}
	)
	if s, _ := it.Derived["language"].(string); s != "" {
		sourceLang = s
	}
	// 先采纳成员发送前已人工校对的译文，避免重复调用模型
	if pre, _ := it.Derived["translations"].(map[string]any); len(pre) > 0 {
		for k, raw := range pre {
			if v, ok := raw.(string); ok && strings.TrimSpace(v) != "" {
				translations[k] = v
			}
		}
	}
	needLangs := make([]string, 0, len(uniqueLangs))
	for _, targetLang := range uniqueLangs {
		if sourceLang != "" && lang.Same(sourceLang, targetLang) {
			continue
		}
		covered := false
		for k := range translations {
			if lang.Same(k, targetLang) {
				covered = true
				break
			}
		}
		if !covered {
			needLangs = append(needLangs, targetLang)
		}
	}
	if b.translator != nil && len(needLangs) > 0 && rawText != "" {
		newTranslations := map[string]string{}
		if sourceLang != "" {
			translatedMap, err := b.translator.Translate(ctx, p.TenantID, &it, sourceLang, needLangs)
			if errors.Is(err, llm.ErrNotConfigured) {
				slog.DebugContext(ctx, "ticket broadcast translation not configured, sending original text", "item", it.ID)
			} else if err != nil {
				return err
			} else {
				for k, v := range translatedMap {
					translations[k] = v
					newTranslations[k] = v
				}
			}
		} else {
			var authorLang string
			if it.ActorType == conversation.MemberActor && it.ActorID != "" {
				_ = b.db.NewSelect().Table("members").Column("language").Where("id = ?", it.ActorID).Scan(ctx, &authorLang)
			}
			detected, translatedMap, err := b.translator.Incoming(ctx, p.TenantID, &it, authorLang, needLangs)
			if errors.Is(err, llm.ErrNotConfigured) {
				slog.DebugContext(ctx, "ticket broadcast translation not configured, sending original text", "item", it.ID)
			} else if err != nil {
				return err
			} else {
				sourceLang = detected
				for k, v := range translatedMap {
					translations[k] = v
					newTranslations[k] = v
				}
			}
		}
		// 把广播生成的客户语言译文同步回写到工单时间线项，使客户在网页聊天插件的工单详情中也能直接看到对应译文
		if sourceLang != "" || len(newTranslations) > 0 {
			if err := conversation.SaveTranslation(ctx, b.db, b.down, p.TenantID, it.ID, conversation.Translated{
				Language:     sourceLang,
				Translations: newTranslations,
			}); err != nil {
				return err
			}
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
		if rawText != "" {
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
		}
		if _, err := b.sendMsg.HandleTranslated(ctx, sys, tc.ConversationID, outText, original, outLang, prompt, attachments...); err != nil {
			return errors.Wrapf(err, "broadcast ticket update to conversation %s", tc.ConversationID)
		}
	}
	totalSent := len(sentConv) + len(pending)
	if err := conversation.SaveTicketBroadcastCount(ctx, b.db, b.down, p.TenantID, it.ID, totalSent); err != nil {
		return err
	}
	slog.InfoContext(ctx, "ticket public update broadcasted",
		"ticket_id", it.ConversationID, "item_id", it.ID, "recipients", len(pending), "attachments", len(attachments), "translated_languages", len(translations))
	return nil
}
