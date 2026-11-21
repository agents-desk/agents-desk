package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
)

// ReceiveParams 是渠道收到的一条消息。
type ReceiveParams struct {
	ChannelID      string
	From           string // 对方在渠道中的外部编号
	FromName       string // 对方在渠道中显示的名字，新建联系人时使用
	ExternalID     string // 消息在渠道中的编号，用来去重；渠道没有编号时为空
	Text           string
	ConversationID string // 对方选定继续的会话；只在可以有多个会话的渠道中使用，为空时新建会话
}

// Received 是写入收到的消息的结果。
type Received struct {
	ConversationID string
	Item           ItemDTO
	Duplicate      bool // 这条消息之前已经收到过，这次没有写入
}

// Receive 写入渠道收到的一条消息：找到或创建联系人，把消息归入会话，追加到时间线。由连接器以系统的身份调用；
// 客户直接调用接口的渠道（网页聊天插件）以联系人的身份调用，只能以自己的身份发消息。
type Receive struct {
	db       *bun.DB
	registry channel.Registry
	down     Downstream
}

// NewReceive 返回写入收到的消息的动作；registry 用来读取渠道的能力，down 是时间线有新内容时通知的下游。
func NewReceive(db *bun.DB, registry channel.Registry, down Downstream) *Receive {
	return &Receive{db: db, registry: registry, down: down}
}

// Handle 在 sub 所在的工作区中写入渠道 p.ChannelID 收到的消息。消息按渠道的规则归入会话：
//   - 可以有多个会话的渠道：归入对方选定的会话 p.ConversationID，没有选定时新建会话；
//   - 只有一个聊天窗口的渠道：归入对方最近的会话，还没有会话时新建。
//
// 已关闭或稍后处理的会话重新打开。然后自动指派：负责人离开并选了重新分配时，会话放回团队收件箱；会话没有负责人时，
// 按团队收件箱的分配方式分配（新会话进入渠道的默认团队收件箱）。同一个 p.ExternalID 重复收到时，返回之前写入的那一项，Duplicate 为 true。
// 系统主体可以以任何人的身份写入；联系人主体只能写入自己的消息（p.From 是他在这个渠道中的身份），否则返回 ErrForbidden，
// 他的 p.ExternalID 由入口加上联系人的前缀，不会与别人的重复。渠道不存在时返回 channel.ErrNotFound，已停用时返回 channel.ErrDisabled。
func (a *Receive) Handle(ctx context.Context, sub identity.Subject, p ReceiveParams) (*Received, error) {
	if sub.Type != contact.Type {
		if err := authorize(sub, ""); err != nil {
			return nil, err
		}
	}
	if prev, err := a.previous(ctx, sub, p); prev != nil || err != nil {
		return prev, err
	}
	var res Received
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := channel.Find(ctx, tx, sub.Tenant, p.ChannelID)
		if err != nil {
			return err
		}
		if !ch.Enabled {
			return channel.ErrDisabled.New()
		}
		connector, err := a.registry.Connector(ch.Kind)
		if err != nil {
			return err
		}
		person, _, err := contact.ResolveIdentity(ctx, tx, sub.Tenant, ch.ID, p.From, contact.Profile{Name: p.FromName})
		if err != nil {
			return err
		}
		if sub.Type == contact.Type && person.ID != sub.ID {
			return ErrForbidden.New()
		}
		c, opened, err := a.conversationFor(ctx, tx, ch, person, p, connector.Capabilities().MultipleConversations)
		if err != nil {
			return err
		}
		items := make([]*Item, 0, 2)
		if opened != "" {
			items = append(items, &Item{
				Kind: Event, ActorType: SystemActor, Visibility: MembersOnly,
				Content: Content{Event: opened, Data: map[string]any{"from": string(c.Status)}},
			})
			c.Status, c.SnoozedUntil = Open, nil
		}
		msg := &Item{
			Kind: Message, ActorType: ContactActor, ActorID: person.ID, Visibility: Everyone,
			Content: Content{Text: p.Text}, ChannelID: &ch.ID,
		}
		if p.ExternalID != "" {
			msg.ExternalID = &p.ExternalID
		}
		items = append(items, msg)
		// 自动指派：负责人离开并选了重新分配时放回团队收件箱；没有负责人时按团队收件箱的分配方式分配
		auto, err := releaseIfAway(ctx, tx, c)
		if err != nil {
			return err
		}
		if len(auto) == 0 {
			next, err := roundRobin(ctx, tx, c)
			if err != nil {
				return err
			}
			if next != nil {
				auto = append(auto, next)
			}
		}
		items = append(items, auto...)
		at := now()
		c.LastMessageAt = at
		if c.WaitingSince == nil {
			c.WaitingSince = &at
		}
		if err := commit(ctx, tx, a.down, c, items...); err != nil {
			return err
		}
		dtos, err := toItemDTOs(ctx, tx, []Item{*msg})
		if err != nil {
			return err
		}
		res = Received{ConversationID: c.ID, Item: dtos[0]}
		return nil
	})
	if database.IsUniqueViolation(err, "timeline_items_external") {
		// 同一条消息同时到达两次，另一次先写入了
		return a.previous(ctx, sub, p)
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// previous 返回之前已经写入的同一条消息（按渠道和外部编号）；没有时返回 nil。sub 是联系人而那条消息不是他发的时
// 返回 ErrForbidden。
func (a *Receive) previous(ctx context.Context, sub identity.Subject, p ReceiveParams) (*Received, error) {
	if p.ExternalID == "" {
		return nil, nil
	}
	var it Item
	err := a.db.NewSelect().Model(&it).
		Where("tenant_id = ? AND channel_id = ? AND external_id = ?", sub.Tenant, p.ChannelID, p.ExternalID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "load received message")
	}
	if sub.Type == contact.Type && (it.ActorType != ContactActor || it.ActorID != sub.ID) {
		return nil, ErrForbidden.New()
	}
	dtos, err := toItemDTOs(ctx, a.db, []Item{it})
	if err != nil {
		return nil, err
	}
	return &Received{ConversationID: it.ConversationID, Item: dtos[0], Duplicate: true}, nil
}

// conversationFor 在事务 tx 中找到消息要归入的会话并锁住它，没有时新建。会话原来是已关闭或稍后处理时，
// opened 是要追加的系统事件名称（conversation.reopened 或 conversation.unsnoozed）。
func (a *Receive) conversationFor(ctx context.Context, tx bun.IDB, ch *channel.Channel, person *contact.Contact, p ReceiveParams, multiple bool) (c *Conversation, opened string, err error) {
	switch {
	case multiple && p.ConversationID != "":
		c, err = find(ctx, tx, ch.TenantID, p.ConversationID, true)
		if err != nil {
			return nil, "", err
		}
		if c.ContactID == nil || *c.ContactID != person.ID || c.ChannelID == nil || *c.ChannelID != ch.ID {
			return nil, "", ErrNotFound.New()
		}
	case !multiple:
		c = new(Conversation)
		err = tx.NewSelect().Model(c).Where("tenant_id = ? AND contact_id = ? AND channel_id = ?", ch.TenantID, person.ID, ch.ID).
			OrderExpr("last_message_at DESC").Limit(1).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			c = nil
		} else if err != nil {
			return nil, "", errors.Wrap(err, "load latest conversation")
		}
	}
	if c == nil {
		c, err = start(ctx, tx, ch, person)
		return c, "", err
	}
	switch c.Status {
	case Closed:
		return c, "conversation.reopened", nil
	case Snoozed:
		return c, "conversation.unsnoozed", nil
	}
	return c, "", nil
}

// start 在事务 tx 中为联系人 person 在渠道 ch 上新建一个打开的服务型会话，进入渠道的默认团队收件箱，联系人是参与者。
func start(ctx context.Context, tx bun.IDB, ch *channel.Channel, person *contact.Contact) (*Conversation, error) {
	c := &Conversation{
		ID: newID(), TenantID: ch.TenantID, Kind: Service, ChannelID: &ch.ID, ContactID: &person.ID, TeamID: ch.TeamID,
		Status: Open, Priority: Normal, Language: person.Language, LastMessageAt: now(), Attributes: map[string]any{},
	}
	if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "insert conversation")
	}
	return c, join(ctx, tx, c.ID, ContactActor, person.ID)
}
