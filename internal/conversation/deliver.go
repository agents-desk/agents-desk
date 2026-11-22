package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/channel"
)

// DeliverTopic 是把发给对方的消息交给连接器发出的下游，在入口中加入 Downstream。
var DeliverTopic = queue.Topic[Appended]("conversation.deliver")

// Deliver 把时间线中发给对方的消息交给渠道的连接器发出，并记录送达状态。
type Deliver struct {
	db       *bun.DB
	registry channel.Registry
	media    Media
}

// NewDeliver 返回发出消息的动作；registry 按渠道的种类找到连接器，media 用于解析消息附带的附件地址（测试中不涉及附件时可省略）。
func NewDeliver(db *bun.DB, registry channel.Registry, media ...Media) *Deliver {
	var m Media
	if len(media) > 0 {
		m = media[0]
	}
	return &Deliver{db: db, registry: registry, media: m}
}

// Job 处理 DeliverTopic 的任务：时间线的新一项是成员或 AI 发给对方的消息、送达状态还是发送中时，
// 交给连接器发出，发出即送达的记为已送达。其他的项不处理。重复的任务不会重复发出已送达的消息。
// 连接器失败时返回错误，任务稍后重试；最后一次也失败时记为失败。
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
	connector, err := a.registry.Connector(ch.Kind)
	if err != nil {
		return err
	}
	var to, contactID string
	if c.ContactID != nil {
		contactID = *c.ContactID
		err = a.db.NewSelect().Table("contact_identities").Column("external_id").
			Where("channel_id = ? AND contact_id = ?", ch.ID, *c.ContactID).Limit(1).Scan(ctx, &to)
		if err != nil {
			return errors.Wrap(err, "load contact identity")
		}
	}
	attachmentsByItem, err := loadAttachments(ctx, a.db, a.media, []string{it.ID})
	if err != nil {
		return err
	}
	externalID, delivered, err := connector.Send(ctx, channel.Outgoing{
		Channel: ch, ConversationID: c.ID, ItemID: it.ID, Seq: it.Seq, ContactID: contactID, ExternalTo: to,
		Text: it.Content.Text, Attachments: toChannelAttachments(attachmentsByItem[it.ID]),
	})
	if err != nil {
		if _, final := queue.AttemptFromContext(ctx); final {
			if markErr := a.mark(ctx, it.ID, Failed, "", err.Error()); markErr != nil {
				return markErr
			}
		}
		return err
	}
	status := Sending
	if delivered {
		status = Delivered
	}
	return a.mark(ctx, it.ID, status, externalID, "")
}

// mark 把消息 itemID 的送达状态改为 status，记下渠道中的编号和失败的原因。
func (a *Deliver) mark(ctx context.Context, itemID string, status DeliveryStatus, externalID, reason string) error {
	_, err := a.db.NewUpdate().Model((*Delivery)(nil)).
		Set("status = ?", status).Set("external_id = ?", externalID).Set("error = ?", reason).Set("updated_at = ?", now()).
		Where("item_id = ?", itemID).Exec(ctx)
	return errors.Wrap(err, "update delivery")
}
