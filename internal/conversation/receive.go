package conversation

import (
	"context"
	"database/sql"
	"strings"
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
	"agents-desk/internal/tag"
)

// ReceiveParams 是渠道收到的一条消息。
type ReceiveParams struct {
	ChannelID           string
	From                string     // 对方在渠道中的外部编号（外部群聊中为发言人在该渠道中的外部编号）
	FromName            string     // 对方在渠道中显示的名字，新建联系人时使用
	ExternalID          string     // 消息在渠道中的编号，用来去重；渠道没有编号时为空
	ExternalChatID      string     // 可选：个人商务渠道中的外部会话或群聊唯一标识（如 Telegram peer/group ID）
	IsGroup             bool       // 可选：个人商务渠道中该会话是否为外部群聊（如 Telegram 群）
	ChatTitle           string     // 可选：外部群聊的标题，收到消息时同步更新
	FromOwner           bool       // 可选：由个人渠道归属成员在外部客户端（如官方 Telegram App）发出的同步消息
	CreatedAt           *time.Time // 可选：外部消息原始时间（用于历史消息同步）；为空时取当前时间
	Text                string
	Attachments         []AttachmentInput    // 客户端通过 /files 上传的已就绪媒体附件（在写入事务中确认）
	IncomingAttachments []IncomingAttachment // 外部渠道收到的媒体元数据（不阻塞 Webhook，由队列自动拉取或点击按需拉取）
	TicketID            string               // 可选：客户从工单详情发起补充沟通时附带的工单 ID，用于关联当前会话并唤醒等待工单
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

// Handle 在 sub 所在的工作区中写入渠道 p.ChannelID 收到的消息。
// 所有实时聊天渠道统一采用“同一联系人最多一条活跃服务型会话”规则：
//   - 已存在未关闭会话（open/snoozed）时直接归入该会话（稍后处理自动恢复为打开）；
//   - 最近一条会话已关闭且处于工作区重开窗口（reopen_window_hours）内时重新打开；
//   - 超出重开窗口或尚无会话时新建下一轮服务型会话。
//
// 随后执行自动指派、刷新回复渠道、触发等待中工单唤醒与下游分发。同一个 p.ExternalID 重复收到时返回之前写入的项，Duplicate 为 true。
// 系统主体可以以任何人的身份写入；联系人主体只能写入自己的消息（p.From 是他在这个渠道中的身份），否则返回 ErrForbidden。
// 渠道不存在时返回 channel.ErrNotFound，已停用时返回 channel.ErrDisabled。
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
	var err error
	// 同一客户并发首条消息触发唯一活跃会话索引冲突时重试一次，第二次事务即可读到先创建的活跃会话。
	for attempt := range 2 {
		err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
			ch, err := channel.Find(ctx, tx, sub.Tenant, p.ChannelID)
			if err != nil {
				return err
			}
			if !ch.Enabled {
				return channel.ErrDisabled.New()
			}
			if _, err := a.registry.Connector(ch.Kind); err != nil {
				return err
			}
			// 成员个人商务渠道（owner_member_id 非空）：归入外部商务聊天（kind=chat, scope=external），不进入客服收件箱
			if ch.IsPersonal() {
				out, err := a.receiveExternalChat(ctx, tx, sub, ch, p)
				if err != nil {
					return err
				}
				res = *out
				return nil
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
			c, created, opened, defaultMemberID, err := a.conversationFor(ctx, tx, ch, person, at)
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
			replyChID := ch.ID
			c.ReplyChannelID = &replyChID
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
			// 客户回复时，将客户名下关联且处于“等待客户”状态的工单自动切回“处理中”；显式指定工单时同时关联到当前会话
			if err := resumeWaitingTickets(ctx, tx, a.down, c.TenantID, c, p.TicketID, at); err != nil {
				return err
			}
			dtos, err := toItemDTOs(ctx, tx, a.down.Media, []Item{*msg})
			if err != nil {
				return err
			}
			res = Received{ConversationID: c.ID, Item: dtos[0], Created: created, Reopened: opened == "conversation.reopened"}
			return nil
		})
		if attempt == 0 && (database.IsUniqueViolation(err, "conversations_one_active_service_per_contact") ||
			database.IsUniqueViolation(err, "chats_tenant_channel_external")) {
			continue
		}
		break
	}
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

// conversationFor 在事务 tx 中为联系人 person 找到消息要归入的服务型会话并加行锁，没有可用会话时新建：
//   - 已登录用户（contact.User）跨所有渠道查找该联系人在本工作区最近一条服务型会话；
//   - 访客线索（contact.Lead）按本渠道查找最近一条服务型会话；
//   - 若存在进行中/稍后处理的会话直接复用；若最近会话已关闭且在工作区重开窗口（reopen_window_hours）内则重开；
//   - 超出重开窗口或尚无会话时新建一条服务型会话。
//
// 会话原来是已关闭或稍后处理时，opened 返回要追加的系统事件名称（conversation.reopened 或 conversation.unsnoozed）。
// 若新建会话命中工作区级默认成员指派，defaultMemberID 返回该成员 ID。
func (a *Receive) conversationFor(ctx context.Context, tx bun.IDB, ch *channel.Channel, person *contact.Contact, at time.Time) (c *Conversation, created bool, opened string, defaultMemberID string, err error) {
	c = new(Conversation)
	q := tx.NewSelect().Model(c).Where("tenant_id = ? AND kind = ? AND contact_id = ?", ch.TenantID, Service, person.ID)
	if person.Kind != contact.User {
		q = q.Where("channel_id = ?", ch.ID)
	}
	err = q.OrderExpr("last_message_at DESC, id DESC").Limit(1).For("UPDATE").Scan(ctx)
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
	chID := ch.ID
	replyChID := ch.ID
	c := &Conversation{
		ID: newID(), TenantID: ch.TenantID, Kind: Service, ChannelID: &chID, ReplyChannelID: &replyChID, ContactID: &person.ID, TeamID: teamID,
		Status: Open, Priority: Normal, Language: person.Language, OpenedAt: at, LastMessageAt: at, Attributes: map[string]any{},
	}
	if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
		return nil, "", errors.Wrap(err, "insert conversation")
	}
	return c, defaultMemberID, join(ctx, tx, c.ID, ContactActor, person.ID)
}

// receiveExternalChat 在事务 tx 中将个人商务渠道 ch 收到（或由归属成员在外部客户端同步发出）的消息写入外部商务聊天：
//   - 1:1 外部商务聊天（IsGroup=false）：解析对方联系人，按 (tenant_id, channel_id, external_chat_id) 复用或创建外部单聊，
//     归属成员与联系人为参与者；
//   - 外部群聊（IsGroup=true，如 Telegram 群）：按 (tenant_id, channel_id, external_chat_id) 复用或创建外部群聊并同步群标题，
//     归属成员为参与者，每条群消息按发言人的渠道外部编号解析为对应联系人以便准确展示发言人姓名。
//
// 当 FromOwner 为 true 时，消息发起者记为渠道归属成员且不写入 Delivery（已在外部客户端发出），并自动推进归属成员的已读位置。
func (a *Receive) receiveExternalChat(ctx context.Context, tx database.Tx, sub identity.Subject, ch *channel.Channel, p ReceiveParams) (*Received, error) {
	ownerID := *ch.OwnerMemberID
	bp := channel.BusinessPolicyOf(ch.Settings)
	var person *contact.Contact
	var contactCreated bool
	var err error
	if !p.FromOwner || (!p.IsGroup && strings.TrimSpace(p.From) != "") {
		person, contactCreated, err = contact.ResolveIdentity(ctx, tx, sub.Tenant, ch.ID, p.From, contact.Profile{Name: p.FromName})
		if err != nil {
			return nil, err
		}
		if sub.Type == contact.Type && person.ID != sub.ID {
			return nil, ErrForbidden.New()
		}
	}
	confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, p.Attachments)
	if err != nil {
		return nil, err
	}
	incomingAtts, needsIngest := buildIncomingAttachments(ch.ID, p.IncomingAttachments)
	allAttachments := append(confirmed, incomingAtts...)

	at := now()
	if p.CreatedAt != nil && !p.CreatedAt.IsZero() {
		at = p.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	extChatID := strings.TrimSpace(p.ExternalChatID)
	if extChatID == "" {
		if person == nil {
			return nil, errors.New("conversation: external_chat_id or contact is required for external chat")
		}
		extChatID = "contact:" + person.ID
	}

	// 按 (tenant_id, channel_id, external_chat_id) 查找或创建外部商务聊天
	cm := new(ChatMeta)
	err = tx.NewSelect().Model(cm).
		Where("tenant_id = ? AND channel_id = ? AND external_chat_id = ?", ch.TenantID, ch.ID, extChatID).
		For("UPDATE").Scan(ctx)
	var c *Conversation
	created := false
	switch {
	case errors.Is(err, sql.ErrNoRows):
		mode := ChatDirect
		title := ""
		var contactID *string
		var lang string
		if p.IsGroup {
			mode = ChatGroup
			title = strings.TrimSpace(p.ChatTitle)
		} else if person != nil {
			cid := person.ID
			contactID = &cid
			lang = person.Language
		}
		chID := ch.ID
		replyChID := ch.ID
		c = &Conversation{
			ID:             newID(),
			TenantID:       ch.TenantID,
			Kind:           Chat,
			ChannelID:      &chID,
			ReplyChannelID: &replyChID,
			ContactID:      contactID,
			Status:         Open,
			Priority:       Normal,
			Language:       lang,
			OpenedAt:       at,
			LastMessageAt:  at,
			Attributes:     map[string]any{},
		}
		if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "insert external chat conversation")
		}
		cm = &ChatMeta{
			ConversationID: c.ID,
			TenantID:       ch.TenantID,
			Mode:           mode,
			Scope:          ChatExternal,
			Name:           title,
			ChannelID:      &chID,
			ExternalChatID: extChatID,
			CreatedBy:      &ownerID,
		}
		if _, err := tx.NewInsert().Model(cm).Exec(ctx); err != nil {
			return nil, err
		}
		if err := join(ctx, tx, c.ID, MemberActor, ownerID); err != nil {
			return nil, err
		}
		if !p.IsGroup && person != nil {
			if err := join(ctx, tx, c.ID, ContactActor, person.ID); err != nil {
				return nil, err
			}
		}
		// 渠道策略：新建外部商务聊天时自动拉入默认协作成员（已移除工作区的成员自动跳过）
		if len(bp.DefaultCollaboratorIDs) > 0 {
			roles := authz.NewStore(tx, auth.Catalog)
			for _, mid := range bp.DefaultCollaboratorIDs {
				if mid == "" || mid == ownerID {
					continue
				}
				if _, rerr := member.RoleOf(ctx, roles, identity.Ref{Type: member.Type, ID: mid}, ch.TenantID); rerr != nil {
					if errors.Is(rerr, member.ErrNotFound) {
						continue
					}
					return nil, rerr
				}
				if err := join(ctx, tx, c.ID, MemberActor, mid); err != nil {
					return nil, err
				}
			}
		}
		created = true
	case err != nil:
		return nil, errors.Wrap(err, "load external chat meta")
	default:
		c, err = find(ctx, tx, ch.TenantID, cm.ConversationID, true)
		if err != nil {
			return nil, err
		}
		if err := join(ctx, tx, c.ID, MemberActor, ownerID); err != nil {
			return nil, err
		}
		if p.IsGroup {
			if title := strings.TrimSpace(p.ChatTitle); title != "" && cm.Name != title {
				cm.Name = title
				if _, err := tx.NewUpdate().Model(cm).Column("name", "updated_at").WherePK().Exec(ctx); err != nil {
					return nil, errors.Wrap(err, "update external group title")
				}
			}
		}
	}

	actorType := ContactActor
	actorID := ""
	if p.FromOwner {
		actorType = MemberActor
		actorID = ownerID
	} else if person != nil {
		actorID = person.ID
	}
	chID := ch.ID
	msg := &Item{
		Kind:       Message,
		ActorType:  actorType,
		ActorID:    actorID,
		Visibility: Everyone,
		ChannelID:  &chID,
		Content:    Content{Text: p.Text},
	}
	if p.CreatedAt != nil && !p.CreatedAt.IsZero() {
		msg.CreatedAt = at
	}
	if p.ExternalID != "" {
		ext := p.ExternalID
		msg.ExternalID = &ext
	}
	if person != nil && !p.FromOwner {
		if err := contact.TouchSeen(ctx, tx, sub.Tenant, person.ID, at); err != nil {
			return nil, err
		}
		// 渠道策略：首次建立联系人或首次经此商务渠道建聊时自动打上默认标签（已删除标签自动跳过）
		if (contactCreated || created) && len(bp.DefaultTagIDs) > 0 {
			for _, tagID := range bp.DefaultTagIDs {
				if tagID == "" {
					continue
				}
				if _, _, terr := contact.AddTag(ctx, tx, ch.TenantID, person.ID, tagID); terr != nil && !errors.Is(terr, tag.ErrNotFound) {
					return nil, terr
				}
			}
		}
	}
	if at.After(c.LastMessageAt) || created {
		c.LastMessageAt = at
	}
	if err := commit(ctx, tx, a.down, c, msg); err != nil {
		return nil, err
	}
	if err := insertAttachments(ctx, tx, c.TenantID, msg.ID, allAttachments); err != nil {
		return nil, err
	}
	if needsIngest {
		if err := MediaIngestTopic.Publish(ctx, tx, MediaIngestJob{
			TenantID: c.TenantID, ConversationID: c.ID, ItemID: msg.ID,
		}); err != nil {
			return nil, errors.Wrap(err, "publish media ingest job")
		}
	}
	if p.FromOwner {
		if _, err := tx.NewRaw(`INSERT INTO conversation_reads (member_id, conversation_id, last_read_seq, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (member_id, conversation_id) DO UPDATE
			SET last_read_seq = GREATEST(conversation_reads.last_read_seq, EXCLUDED.last_read_seq), updated_at = EXCLUDED.updated_at`,
			ownerID, c.ID, msg.Seq, now()).Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "advance owner external chat read seq")
		}
	}
	dtos, err := toItemDTOs(ctx, tx, a.down.Media, []Item{*msg})
	if err != nil {
		return nil, err
	}
	return &Received{ConversationID: c.ID, Item: dtos[0], Created: created}, nil
}
