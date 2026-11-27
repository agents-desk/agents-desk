package conversation

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/channel"
	"agents-desk/internal/extgroup"
)

// DeliverTopic 是把发给对方的消息交给连接器发出的下游，在入口中加入 Downstream。
var DeliverTopic = queue.Topic[Appended]("conversation.deliver")

// autoTranslateBudget 是一次自动翻译的最长等待时间：翻译是为发出服务的，模型接口慢或不可用不该把消息堵在发送中。
const autoTranslateBudget = 20 * time.Second

// Delivers 报告时间线项 it 是否需要交给投递任务：只有发给对方、并且指定了渠道的消息才需要。
// commit 用它过滤下游，内部聊天、内部备注和系统事件因此不会给投递任务产生空任务。
func Delivers(it *Item) bool {
	return it.Kind == Message && it.Visibility == Everyone && !it.ActorType.IsExternal() && it.ChannelID != nil
}

// Deliver 把时间线中发给对方的消息交给渠道的连接器发出，并记录送达状态。
type Deliver struct {
	db       *bun.DB
	registry channel.Registry
	down     Downstream
}

// NewDeliver 返回发出消息的动作；registry 按渠道的种类找到连接器，down 提供附件解析与送达状态实时推送依赖。
func NewDeliver(db *bun.DB, registry channel.Registry, down ...Downstream) *Deliver {
	var d Downstream
	if len(down) > 0 {
		d = down[0]
	}
	return &Deliver{db: db, registry: registry, down: d}
}

// Job 处理 DeliverTopic 的任务：时间线的新一项是成员或系统/AI 发给对方的消息、送达状态还是发送中时，
// 解析投递目标与回复协议上下文，按需完成后台自动翻译后交给连接器发出。
func (a *Deliver) Job(ctx context.Context, p Appended) error {
	var it Item
	err := a.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load timeline item")
	}
	if it.Kind != Message || it.Visibility != Everyone || it.ActorType.IsExternal() || it.ChannelID == nil {
		return nil
	}
	var d Delivery
	err = a.db.NewSelect().Model(&d).Where("item_id = ?", it.ID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && d.Status != Sending) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load delivery")
	}
	c, err := find(ctx, a.db, p.TenantID, p.ConversationID, false)
	if err != nil {
		return err
	}
	ch, err := channel.Find(ctx, a.db, p.TenantID, *it.ChannelID)
	if err != nil {
		return err
	}
	if !ch.Enabled {
		return queue.Permanent(channel.ErrDisabled.New())
	}
	connector, err := a.registry.Connector(ch.Kind)
	if err != nil {
		return err
	}
	target, replyToExtID, replyCtx, err := resolveDeliveryTarget(ctx, a.db, c, ch, &it)
	if err != nil {
		return err
	}
	// 若消息带有后台自动翻译标记，在真正调用连接器发出前先完成翻译并原子更新正文与语言
	if pending := pendingTranslationOf(it.Derived); pending != nil {
		if err := a.applyPendingTranslation(ctx, &it, c, pending); err != nil {
			return err
		}
	}
	attachmentsByItem, err := loadAttachments(ctx, a.db, a.down.Media, []string{it.ID})
	if err != nil {
		return err
	}
	externalID, delivered, err := connector.Send(ctx, channel.Outgoing{
		Channel:           ch,
		ConversationID:    c.ID,
		ConversationKind:  string(c.Kind),
		ItemID:            it.ID,
		Seq:               it.Seq,
		Target:            target,
		ContactID:         target.ContactID,
		ExternalTo:        target.ExternalTo,
		ReplyToExternalID: replyToExtID,
		Mentions:          it.Content.ExternalMentions,
		ReplyContext:      replyCtx,
		Text:              it.Content.Text,
		Attachments:       toChannelAttachments(attachmentsByItem[it.ID]),
	})
	if err != nil {
		return err
	}
	status := Sending
	if delivered {
		status = Delivered
	}
	return a.markAndNotify(ctx, p.TenantID, c.ID, it.ID, status, externalID, "", delivered)
}

// resolveDeliveryTarget 按统一模型解析出站目标地址、引用外部消息 ID 与短期回复协议上下文，不针对具体渠道种类特判。
func resolveDeliveryTarget(ctx context.Context, db bun.IDB, c *Conversation, ch *channel.Channel, it *Item) (channel.DeliveryTarget, string, map[string]any, error) {
	var target channel.DeliveryTarget

	switch {
	case c.ExternalGroupID != nil && *c.ExternalGroupID != "":
		g, err := extgroup.Find(ctx, db, c.TenantID, *c.ExternalGroupID)
		if err != nil {
			return target, "", nil, err
		}
		if g.Status == extgroup.StatusRemoved {
			return target, "", nil, queue.Permanent(extgroup.ErrRemoved.New())
		}
		target = channel.DeliveryTarget{
			Kind:            channel.TargetGroup,
			ExternalGroupID: g.ID,
			ExternalTo:      g.ExternalID,
		}
	case c.Kind == Chat:
		var row struct {
			Mode           ChatMode `bun:"mode"`
			ExternalChatID string   `bun:"external_chat_id"`
		}
		err := db.NewSelect().Table("chats").Column("mode", "external_chat_id").Where("conversation_id = ?", c.ID).Scan(ctx, &row)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return target, "", nil, errors.Wrap(err, "load chat delivery target")
		}
		if row.Mode == ChatGroup {
			target = channel.DeliveryTarget{Kind: channel.TargetGroup, ExternalTo: row.ExternalChatID}
		} else {
			target = channel.DeliveryTarget{Kind: channel.TargetContact, ExternalTo: row.ExternalChatID}
			if c.ContactID != nil {
				target.ContactID = *c.ContactID
				if target.ExternalTo == "" {
					if err := db.NewSelect().Table("contact_identities").Column("external_id").
						Where("channel_id = ? AND contact_id = ?", ch.ID, *c.ContactID).Limit(1).Scan(ctx, &target.ExternalTo); err != nil && !errors.Is(err, sql.ErrNoRows) {
						return target, "", nil, errors.Wrap(err, "load chat contact identity")
					}
				}
			}
		}
	case c.ContactID != nil && *c.ContactID != "":
		target = channel.DeliveryTarget{Kind: channel.TargetContact, ContactID: *c.ContactID}
		if err := db.NewSelect().Table("contact_identities").Column("external_id").
			Where("channel_id = ? AND contact_id = ?", ch.ID, *c.ContactID).Limit(1).Scan(ctx, &target.ExternalTo); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return target, "", nil, errors.Wrap(err, "load contact identity")
		}
	}

	// 解析引用回复（或更正原消息）的外部消息 ID 与对应的短期协议回复上下文（如企微长连接 req_id）
	var replyToExtID string
	var replyCtx map[string]any
	refItemID := ""
	if it.ReplyToItemID != nil && *it.ReplyToItemID != "" {
		refItemID = *it.ReplyToItemID
	} else {
		var origID string
		err := db.NewSelect().Table("message_corrections").Column("original_item_id").
			Where("correction_item_id = ?", it.ID).Scan(ctx, &origID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return target, "", nil, errors.Wrap(err, "load correction original item")
		}
		refItemID = origID
	}
	if refItemID != "" {
		var ref Item
		err := db.NewSelect().Model(&ref).Where("id = ? AND tenant_id = ?", refItemID, c.TenantID).Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return target, "", nil, errors.Wrap(err, "load reply_to item")
		}
		if err == nil {
			if ref.ExternalID != nil && *ref.ExternalID != "" {
				replyToExtID = *ref.ExternalID
			} else {
				var delExt string
				if derr := db.NewSelect().Table("message_deliveries").Column("external_id").
					Where("item_id = ?", ref.ID).Scan(ctx, &delExt); derr == nil {
					replyToExtID = delExt
				}
			}
			if rc, ok := ref.Derived["reply_context"].(map[string]any); ok && len(rc) > 0 {
				replyCtx = rc
			}
		}
	}
	if len(replyCtx) == 0 {
		var latest Item
		err := db.NewSelect().Model(&latest).
			Where("tenant_id = ? AND conversation_id = ? AND channel_id = ? AND kind = ? AND actor_type IN (?) AND seq < ?",
				c.TenantID, c.ID, ch.ID, Message, bun.In([]ActorType{ContactActor, ExternalUserActor}), it.Seq).
			OrderExpr("seq DESC").Limit(1).Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return target, "", nil, errors.Wrap(err, "load latest inbound reply context")
		}
		if err == nil {
			if rc, ok := latest.Derived["reply_context"].(map[string]any); ok && len(rc) > 0 {
				replyCtx = rc
			}
		}
	}
	return target, replyToExtID, replyCtx, nil
}

// pendingTranslationOf 从 timeline_items.derived 中读取待异步翻译参数；不存在时返回 nil。
func pendingTranslationOf(derived map[string]any) *AutoTranslateRequest {
	if derived == nil {
		return nil
	}
	raw, ok := derived["pending_translation"].(map[string]any)
	if !ok {
		return nil
	}
	lang, _ := raw["language"].(string)
	tone, _ := raw["tone"].(string)
	return &AutoTranslateRequest{Language: lang, Tone: tone}
}

// applyPendingTranslation 调用注入的 AutoTranslator 为待投递消息生成目标译文，并原位更新 timeline_items.content
// 同时从 derived 中移除 pending_translation（用 JSONB 减法避免覆盖并发写入的语言/译文派生字段）。
// 翻译失败或超时时按成员原文发出并留下 translation_failed 标记：投递的可用性不依赖翻译服务。
// 只有进程被取消（例如退出）时返回错误，交给队列重试。
func (a *Deliver) applyPendingTranslation(ctx context.Context, it *Item, c *Conversation, req *AutoTranslateRequest) error {
	if a.down.AutoTranslate == nil || strings.TrimSpace(it.Content.Text) == "" {
		return a.clearPendingTranslation(ctx, it, c)
	}
	tctx, cancel := context.WithTimeout(ctx, autoTranslateBudget)
	defer cancel()
	translated, resolvedLang, needed, err := a.down.AutoTranslate(tctx, AutoTranslateJob{
		TenantID:       c.TenantID,
		ConversationID: c.ID,
		MemberID:       it.ActorID,
		Text:           it.Content.Text,
		Language:       req.Language,
		Tone:           req.Tone,
	})
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		slog.WarnContext(ctx, "auto translate failed; sending original text",
			"tenant_id", c.TenantID, "conversation_id", c.ID, "item_id", it.ID, "error", err)
		return a.markTranslationFailed(ctx, it, c)
	}
	if needed && strings.TrimSpace(translated) != "" {
		it.Content = Content{
			Text:             translated,
			Original:         it.Content.Text,
			Language:         resolvedLang,
			Mentions:         it.Content.Mentions,
			ExternalMentions: it.Content.ExternalMentions,
		}
		return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
			if _, err := tx.NewUpdate().Model((*Item)(nil)).
				Set("content = ?", it.Content).
				Set("derived = derived - 'pending_translation'").
				Where("id = ? AND tenant_id = ?", it.ID, c.TenantID).Exec(ctx); err != nil {
				return errors.Wrap(err, "save auto-translated reply content")
			}
			return errors.Wrap(SearchIndex.Sync(ctx, tx, it.ID), "reindex auto-translated reply")
		})
	}
	return a.clearPendingTranslation(ctx, it, c)
}

// clearPendingTranslation 从 derived 中移除待翻译标记。
func (a *Deliver) clearPendingTranslation(ctx context.Context, it *Item, c *Conversation) error {
	_, err := a.db.NewUpdate().Table("timeline_items").
		Set("derived = derived - 'pending_translation'").
		Where("id = ? AND tenant_id = ?", it.ID, c.TenantID).Exec(ctx)
	return errors.Wrap(err, "clear pending translation flag")
}

// markTranslationFailed 移除待翻译标记并记下这次自动翻译没有成功，消息按原文发出。
func (a *Deliver) markTranslationFailed(ctx context.Context, it *Item, c *Conversation) error {
	_, err := a.db.NewRaw(`UPDATE timeline_items
		SET derived = (derived - 'pending_translation') || jsonb_build_object('translation_failed', true)
		WHERE id = ? AND tenant_id = ?`, it.ID, c.TenantID).Exec(ctx)
	return errors.Wrap(err, "mark translation failed")
}

// OnDead 在消息投递任务因永久错误、业务错误或重试次数耗尽而进入失败任务表前执行一次：
// 将仍处于发送中状态的送达记录标记为失败并写入原因，同时推送给能看到会话的成员。
func (a *Deliver) OnDead(ctx context.Context, p Appended, cause error) error {
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	return a.markAndNotify(ctx, p.TenantID, p.ConversationID, p.ItemID, Failed, "", reason, true)
}

// markAndNotify 在事务中更新消息 itemID 的送达状态，需要时在提交后向能看到会话的成员推送变更事件。
func (a *Deliver) markAndNotify(ctx context.Context, tenantID, conversationID, itemID string, status DeliveryStatus, externalID, reason string, notify bool) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		q := tx.NewUpdate().Model((*Delivery)(nil)).
			Set("status = ?", status).Set("external_id = ?", externalID).Set("error = ?", reason).Set("updated_at = ?", now()).
			Where("item_id = ?", itemID)
		if status == Failed {
			q = q.Where("status = ?", Sending)
		}
		res, err := q.Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "update delivery")
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "update delivery")
		}
		if affected == 0 {
			return nil
		}
		if status == Delivered && a.down.NotifyOfflineReply != nil {
			if err := a.down.NotifyOfflineReply(ctx, tx, tenantID, conversationID, itemID); err != nil {
				slog.WarnContext(ctx, "notify offline reply failed", "tenant_id", tenantID, "conversation_id", conversationID, "item_id", itemID, "error", err)
			}
		}
		if !notify {
			return nil
		}
		return NotifyUpdated(ctx, tx, a.down, tenantID, []string{conversationID})
	})
}
