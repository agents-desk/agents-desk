package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/member"
)

// ReceiveParams 是渠道收到的一条消息。
type ReceiveParams struct {
	ChannelID           string
	From                string // 对方在渠道中的外部编号
	FromName            string // 对方在渠道中显示的名字，新建联系人时使用
	ExternalID          string // 消息在渠道中的编号，用来去重；渠道没有编号时为空
	Text                string
	Attachments         []AttachmentInput    // 客户端通过 /files 上传的已就绪媒体附件（在写入事务中确认）
	IncomingAttachments []IncomingAttachment // 外部渠道收到的媒体元数据（不阻塞 Webhook，由队列自动拉取或点击按需拉取）
	ConversationID      string               // 对方选定继续的会话；只在可以有多个会话的渠道中使用，为空时新建会话
	SingleConversation  bool                 // 为 true 时按单会话模式归入该联系人在本渠道最近的会话，不新建并行会话
}

// Received 是写入收到的消息的结果。
type Received struct {
	ConversationID string
	Item           ItemDTO
	Created        bool // 新建了会话
	Reopened       bool // 重新打开了已关闭的会话
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
	if err := requireMessageContent(p.Text, len(p.Attachments)+len(p.IncomingAttachments)); err != nil {
		return nil, err
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
		confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, p.Attachments)
		if err != nil {
			return err
		}
		incomingAtts, needsIngest := buildIncomingAttachments(ch.ID, p.IncomingAttachments)
		allAttachments := append(confirmed, incomingAtts...)
		at := now()
		multiple := connector.Capabilities().MultipleConversations && !p.SingleConversation
		c, created, opened, defaultMemberID, err := a.conversationFor(ctx, tx, ch, person, p, multiple, at)
		if err != nil {
			return err
		}
		items := make([]*Item, 0, 3)
		reopened := false
		if opened != "" {
			items = append(items, &Item{
				Kind: Event, ActorType: SystemActor, Visibility: MembersOnly,
				Content: Content{Event: opened, Data: map[string]any{"from": string(c.Status)}},
			})
			if c.Status == Closed {
				reopened = true
				c.OpenedAt, c.FirstReplyAt = at, nil
			}
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
		// 自动指派：新会话命中工作区默认成员时直接指派并记录系统事件；否则按离开重分配与团队轮流分配规则处理
		if defaultMemberID != "" {
			c.AssigneeType, c.AssigneeID = MemberActor, defaultMemberID
			items = append(items, assignedEvent(system(c.TenantID), c, c.TeamID, "", "", ReasonDefault))
		} else {
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
		}
		if created || reopened {
			if err := openRound(ctx, tx, c, at); err != nil {
				return err
			}
		}
		if err := contact.TouchSeen(ctx, tx, sub.Tenant, person.ID, at); err != nil {
			return err
		}
		c.LastMessageAt = at
		if c.WaitingSince == nil {
			c.WaitingSince = &at
		}
		if err := commit(ctx, tx, a.down, c, items...); err != nil {
			return err
		}
		if err := insertAttachments(ctx, tx, c.TenantID, msg.ID, allAttachments); err != nil {
			return err
		}
		if needsIngest {
			if err := MediaIngestTopic.Publish(ctx, tx, MediaIngestJob{
				TenantID: c.TenantID, ConversationID: c.ID, ItemID: msg.ID,
			}); err != nil {
				return errors.Wrap(err, "publish media ingest job")
			}
		}
		dtos, err := toItemDTOs(ctx, tx, a.down.Media, []Item{*msg})
		if err != nil {
			return err
		}
		res = Received{ConversationID: c.ID, Item: dtos[0], Created: created, Reopened: opened == "conversation.reopened"}
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
	dtos, err := toItemDTOs(ctx, a.db, a.down.Media, []Item{it})
	if err != nil {
		return nil, err
	}
	return &Received{ConversationID: it.ConversationID, Item: dtos[0], Duplicate: true}, nil
}

// conversationFor 在事务 tx 中找到消息要归入的会话并锁住它，没有时新建。
// 只有一个聊天窗口的渠道（!multiple）：最近的会话已关闭时，若在工作区的重开窗口内则重新打开它，超过重开窗口则新建会话。
// 会话原来是已关闭或稍后处理时，opened 是要追加的系统事件名称（conversation.reopened 或 conversation.unsnoozed）。
// 若新建会话命中了工作区级默认成员指派，defaultMemberID 返回该成员 ID。
func (a *Receive) conversationFor(ctx context.Context, tx bun.IDB, ch *channel.Channel, person *contact.Contact, p ReceiveParams, multiple bool, at time.Time) (c *Conversation, created bool, opened string, defaultMemberID string, err error) {
	switch {
	case multiple && p.ConversationID != "":
		c, err = find(ctx, tx, ch.TenantID, p.ConversationID, true)
		if err != nil {
			return nil, false, "", "", err
		}
		if c.ContactID == nil || *c.ContactID != person.ID || c.ChannelID == nil || *c.ChannelID != ch.ID {
			return nil, false, "", "", ErrNotFound.New()
		}
	case !multiple:
		c = new(Conversation)
		err = tx.NewSelect().Model(c).Where("tenant_id = ? AND contact_id = ? AND channel_id = ?", ch.TenantID, person.ID, ch.ID).
			OrderExpr("last_message_at DESC, id DESC").Limit(1).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			c = nil
		} else if err != nil {
			return nil, false, "", "", errors.Wrap(err, "load latest conversation")
		}
		if c != nil && c.Status == Closed {
			var hours int
			if err := tx.NewSelect().Table("tenants").Column("reopen_window_hours").Where("id = ?", ch.TenantID).Scan(ctx, &hours); err != nil {
				return nil, false, "", "", errors.Wrap(err, "load tenant reopen window")
			}
			window := time.Duration(hours) * time.Hour
			if window <= 0 || c.ClosedAt == nil || at.Sub(*c.ClosedAt) > window {
				c = nil
			}
		}
	}
	if c == nil {
		c, defaultMemberID, err = start(ctx, tx, ch, person, at)
		return c, err == nil, "", defaultMemberID, err
	}
	switch c.Status {
	case Closed:
		return c, false, "conversation.reopened", "", nil
	case Snoozed:
		return c, false, "conversation.unsnoozed", "", nil
	}
	return c, false, "", "", nil
}

// start 在事务 tx 中为联系人 person 在渠道 ch 上新建一个打开的服务型会话，联系人是参与者。
// 默认归属优先级：渠道单独配置的未停用默认团队收件箱 > 工作区默认指派（团队收件箱或指定成员） > 未指派。
func start(ctx context.Context, tx bun.IDB, ch *channel.Channel, person *contact.Contact, at time.Time) (*Conversation, string, error) {
	teamID := ch.TeamID
	if teamID != nil {
		active, err := tx.NewSelect().Table("teams").
			Where("id = ? AND tenant_id = ? AND disabled_at IS NULL", *teamID, ch.TenantID).Exists(ctx)
		if err != nil {
			return nil, "", errors.Wrap(err, "check default team")
		}
		if !active {
			teamID = nil
		}
	}
	var defaultMemberID string
	if teamID == nil {
		cfg, err := NewAssignmentSettingsGroup(tx).Tenant(ch.TenantID).Get(ctx)
		if err != nil {
			return nil, "", errors.Wrap(err, "load default assignment settings")
		}
		switch cfg.DefaultTarget {
		case DefaultTeam:
			if cfg.DefaultTeamID != "" {
				active, err := tx.NewSelect().Table("teams").
					Where("id = ? AND tenant_id = ? AND disabled_at IS NULL", cfg.DefaultTeamID, ch.TenantID).Exists(ctx)
				if err != nil {
					return nil, "", errors.Wrap(err, "check workspace default team")
				}
				if active {
					id := cfg.DefaultTeamID
					teamID = &id
				}
			}
		case DefaultMember:
			if cfg.DefaultMemberID != "" {
				ref := identity.Ref{Type: member.Type, ID: cfg.DefaultMemberID}
				if _, err := member.RoleOf(ctx, authz.NewStore(tx, auth.Catalog), ref, ch.TenantID); err == nil {
					active, err := tx.NewSelect().Table("members").
						Where("id = ? AND disabled_at IS NULL", cfg.DefaultMemberID).Exists(ctx)
					if err != nil {
						return nil, "", errors.Wrap(err, "check workspace default member")
					}
					if active {
						defaultMemberID = cfg.DefaultMemberID
					}
				} else if !errors.Is(err, member.ErrNotFound) {
					return nil, "", err
				}
			}
		}
	}
	c := &Conversation{
		ID: newID(), TenantID: ch.TenantID, Kind: Service, ChannelID: &ch.ID, ContactID: &person.ID, TeamID: teamID,
		Status: Open, Priority: Normal, Language: person.Language, OpenedAt: at, LastMessageAt: at, Attributes: map[string]any{},
	}
	if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
		return nil, "", errors.Wrap(err, "insert conversation")
	}
	return c, defaultMemberID, join(ctx, tx, c.ID, ContactActor, person.ID)
}
