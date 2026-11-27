package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
)

// CanView 报告 sub 是否能读取会话 id，规则与接口的可见范围相同：内部聊天按参与者，服务型会话和工单按角色的可见范围。
// 供会话以外的模块（例如 AI 生成的实时频道）复用同一套判断，避免各处各写一份。
func CanView(ctx context.Context, db bun.IDB, sub identity.Subject, id string) (bool, error) {
	if _, err := findVisible(ctx, db, sub, id, false); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// AppendAgentMessage 在事务 tx 中把机器人（AI）的一条完成消息追加到会话 conversationID 的时间线：
// 锁住会话、加入机器人参与者、补齐服务型会话的投递目标与首次回复，然后保存并交给下游。
// 调用方在同一个事务里更新自己的状态（例如把生成标记为完成），消息与状态因此一起提交或一起回滚。
// 消息只能由机器人发出；服务型会话的送达状态在这里记为发送中，由投递任务发到对方。
func AppendAgentMessage(ctx context.Context, tx database.Tx, down Downstream, tenantID, conversationID string, item *Item, at time.Time) error {
	c, err := find(ctx, tx, tenantID, conversationID, true)
	if err != nil {
		return err
	}
	if c.Kind != Service && c.Kind != Chat {
		return ErrWrongKind.New()
	}
	if item.Kind != Message || item.ActorType != BotActor || item.ActorID == "" {
		return ErrWrongKind.New()
	}
	if err := join(ctx, tx, c.ID, BotActor, item.ActorID); err != nil {
		return err
	}
	c.LastMessageAt = at
	var delivery *string // 服务型会话发给对方的渠道；内部聊天为空
	if c.Kind == Service {
		delivery = c.DeliveryChannelID()
		item.ChannelID = delivery
		c.WaitingSince = nil
		if c.FirstReplyAt == nil {
			c.FirstReplyAt = &at
			if err := recordFirstReply(ctx, tx, c, BotActor, item.ActorID, at); err != nil {
				return err
			}
		}
	}
	if err := commit(ctx, tx, down, c, item); err != nil {
		return err
	}
	if delivery != nil {
		if _, err := tx.NewInsert().Model(&Delivery{ItemID: item.ID, Status: Sending, UpdatedAt: at}).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert agent delivery")
		}
	}
	return nil
}
