package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/channel"
)

// DeliverTopic 是把发给对方的消息交给连接器发出的下游，在入口中加入 Downstream。
var DeliverTopic = queue.Topic[Appended]("conversation.deliver")

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

// Job 处理 DeliverTopic 的任务：时间线的新一项是成员或 AI 发给对方的消息、送达状态还是发送中时，
// 交给连接器发出，发出即送达的记为已送达并实时推送给能看到会话的成员。其他的项不处理。
// 重复的任务不会重复发出已送达的消息。渠道已停用时返回永久错误；临时失败时返回错误等待重试，
// 最终放弃（永久错误、业务错误或重试次数耗尽）由 queue.OnDead 统一回调 OnDead 记为失败并推送。
func (a *Deliver) Job(ctx context.Context, p Appended) error {
	var it Item
	err := a.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load timeline item")
	}
	if it.Kind != Message || it.Visibility != Everyone || it.ActorType == ContactActor || it.ChannelID == nil {
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
	var to, contactID string
	if c.ContactID != nil {
		contactID = *c.ContactID
		err = a.db.NewSelect().Table("contact_identities").Column("external_id").
			Where("channel_id = ? AND contact_id = ?", ch.ID, *c.ContactID).Limit(1).Scan(ctx, &to)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errors.Wrap(err, "load contact identity")
		}
	}
	// 外部商务聊天（含 1:1 与外部群聊）：优先使用 chats.external_chat_id 作为目标会话地址
	if c.Kind == Chat {
		var extChatID string
		err = a.db.NewSelect().Table("chats").Column("external_chat_id").Where("conversation_id = ?", c.ID).Scan(ctx, &extChatID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errors.Wrap(err, "load chat external id")
		}
		if extChatID != "" && (to == "" || ch.Kind == channel.TelegramAccount) {
			to = extChatID
		}
	}
	attachmentsByItem, err := loadAttachments(ctx, a.db, a.down.Media, []string{it.ID})
	if err != nil {
		return err
	}
	externalID, delivered, err := connector.Send(ctx, channel.Outgoing{
		Channel: ch, ConversationID: c.ID, ItemID: it.ID, Seq: it.Seq, ContactID: contactID, ExternalTo: to,
		Text: it.Content.Text, Attachments: toChannelAttachments(attachmentsByItem[it.ID]),
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
		if !notify || affected == 0 {
			return nil
		}
		return NotifyUpdated(ctx, tx, a.down, tenantID, []string{conversationID})
	})
}
