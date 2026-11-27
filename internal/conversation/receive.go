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
	"agents-desk/internal/extgroup"
	"agents-desk/internal/member"
	"agents-desk/internal/tag"
)

// ReceiveParams 是渠道收到的一条消息。
// 公共渠道支持 1:1 单聊（IsGroup=false）与群内 @ 机器人（IsGroup=true，ExternalChatID 为群 ID）；
// 个人商务渠道（ch.IsPersonal）归入外部商务聊天（kind=chat, scope=external）。
//
// ExternalID 必须在本渠道内全局唯一，而不只是在会话内唯一：去重按 (channel_id, external_id) 判断，
// 平台的消息编号通常只在会话内唯一（Telegram 的 message_id、Slack 的 ts、邮件的编号在不同会话中会重复），
// 连接器要用会话标识拼出前缀，例如 chatID:messageID；不带前缀会把别的会话的同号消息当成重复丢弃。
type ReceiveParams struct {
	ChannelID           string
	From                string     // 发言人在渠道中的外部编号
	FromName            string     // 发言人在渠道中显示的名字
	FromAvatarURL       string     // 可选：发言人头像地址
	ExternalID          string     // 消息在渠道中的编号，用来去重；渠道没有编号时为空
	ExternalChatID      string     // 群聊或个人号会话在外部渠道中的唯一标识（如 Telegram chat_id、企微 chatid）
	IsGroup             bool       // 是否为群聊消息（公共渠道走外部群路由；个人商务渠道走外部商务群）
	ChatTitle           string     // 可选：外部群的名称，收到消息时同步更新
	FromOwner           bool       // 可选：由个人渠道归属成员在外部客户端发出的同步消息
	CreatedAt           *time.Time // 可选：外部消息原始时间（用于历史消息同步）；为空时取当前时间
	Text                string
	Attachments         []AttachmentInput    // 客户端通过 /files 上传的已就绪媒体附件（在写入事务中确认）
	IncomingAttachments []IncomingAttachment // 外部渠道收到的媒体元数据（不阻塞入站，由队列自动拉取或按需拉取）
	ReplyToExternalID   string               // 可选：外部消息引用的同会话目标外部消息编号
	ReplyContext        map[string]any       // 可选：入站事件携带的短期回复协议上下文（如企微长连接 req_id、钉钉 sessionWebhook）
	TicketID            string               // 可选：客户从工单详情发起补充沟通时附带的工单 ID
}

// Received 是写入收到的消息的结果。
type Received struct {
	ConversationID string
	Item           ItemDTO
	Created        bool // 新建了会话
	Reopened       bool // 重新打开了已关闭的会话
	Duplicate      bool // 这条消息之前已经收到过，这次没有写入
}

// Receive 写入渠道收到的一条消息：根据渠道归属与是否群聊归入对应会话并追加时间线。
// 由连接器以系统主体调用；网页聊天插件与商务链接以联系人主体调用，只能以自己的身份发消息。
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
// 公共渠道按“单聊按 (audience, contact) 最多一条活跃会话 / 群内 @ 按 external_group 最多一条活跃会话”归并：
//   - 已存在未关闭会话（open/snoozed）时直接归入（稍后处理自动恢复为打开）；
//   - 最近一条会话已关闭且处于工作区重开窗口（reopen_window_hours）内时重新打开；
//   - 超出重开窗口或尚无会话时新建下一轮服务型会话。
//
// 同一个 p.ExternalID 重复收到时幂等返回之前写入的项，Duplicate 为 true。
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
	// 并发首条消息触发唯一活跃会话或外部聊天约束冲突时重试一次，第二次事务即可读到先创建的会话。
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
			// 1. 成员个人商务渠道（owner_member_id 非空）：归入外部商务聊天（kind=chat, scope=external）
			if ch.IsPersonal() {
				out, err := a.receiveExternalChat(ctx, tx, sub, ch, p)
				if err != nil {
					return err
				}
				res = *out
				return nil
			}
			// 2. 公共服务渠道群内 @ 机器人：归入外部群路由实体（external_groups），发言人记为 external_user，不污染联系人表
			if p.IsGroup {
				out, err := a.receiveExternalGroup(ctx, tx, sub, ch, p)
				if err != nil {
					return err
				}
				res = *out
				return nil
			}
			// 3. 公共服务渠道 1:1 单聊：按渠道服务对象域归入客户联系人或内部联系人
			out, err := a.receiveDirectService(ctx, tx, sub, ch, p)
			if err != nil {
				return err
			}
			res = *out
			return nil
		})
		if attempt == 0 && (database.IsUniqueViolation(err, "conversations_one_active_service_per_contact") ||
			database.IsUniqueViolation(err, "conversations_one_active_service_per_group") ||
			database.IsUniqueViolation(err, "conversations_external_chat_contact_unique") ||
			database.IsUniqueViolation(err, "chats_tenant_channel_external")) {
			continue
		}
		break
	}
	if database.IsUniqueViolation(err, "timeline_items_external") {
		return a.previous(ctx, sub, p)
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// receiveDirectService 在事务 tx 中写入公共服务渠道的 1:1 单聊消息。
func (a *Receive) receiveDirectService(ctx context.Context, tx database.Tx, sub identity.Subject, ch *channel.Channel, p ReceiveParams) (*Received, error) {
	defaultKind := contact.Lead
	if ch.EffectiveAudience() == channel.AudienceInternal {
		defaultKind = contact.Internal
	}
	person, _, err := contact.ResolveIdentityWithKind(ctx, tx, sub.Tenant, ch.ID, p.From, defaultKind, contact.Profile{Name: p.FromName})
	if err != nil {
		return nil, err
	}
	if sub.Type == contact.Type && person.ID != sub.ID {
		return nil, ErrForbidden.New()
	}
	confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, p.Attachments)
	if err != nil {
		return nil, err
	}
	incomingAtts, needsIngest := buildIncomingAttachments(ch.ID, p.IncomingAttachments)
	allAttachments := append(confirmed, incomingAtts...)

	at := inboundTime(p.CreatedAt)
	c, created, opened, defaultMemberID, err := a.conversationForContact(ctx, tx, ch, person, at)
	if err != nil {
		return nil, err
	}
	replyToID, err := resolveReplyToItemID(ctx, tx, c.TenantID, c.ID, ch.ID, p.ReplyToExternalID)
	if err != nil {
		return nil, err
	}
	msg := &Item{
		Kind:          Message,
		ActorType:     ContactActor,
		ActorID:       person.ID,
		Visibility:    Everyone,
		Content:       Content{Text: p.Text},
		Derived:       inboundDerived(p.ReplyContext),
		ChannelID:     &ch.ID,
		ReplyToItemID: replyToID,
	}
	if p.CreatedAt != nil && !p.CreatedAt.IsZero() {
		msg.CreatedAt = at
	}
	if p.ExternalID != "" {
		ext := p.ExternalID
		msg.ExternalID = &ext
	}
	if err := contact.TouchSeen(ctx, tx, sub.Tenant, person.ID, at); err != nil {
		return nil, err
	}
	return a.finalizeServiceInbound(ctx, tx, c, ch, msg, created, opened, defaultMemberID, allAttachments, needsIngest, p.TicketID, at)
}

// receiveExternalGroup 在事务 tx 中写入公共机器人渠道在第三方群内收到的 @ 机器人或命令消息。
// 群归入 external_groups 路由配置实体，发言人记为 external_users（actor_type=external_user），不创建客户联系人。
func (a *Receive) receiveExternalGroup(ctx context.Context, tx database.Tx, sub identity.Subject, ch *channel.Channel, p ReceiveParams) (*Received, error) {
	if sub.Type == contact.Type {
		return nil, ErrForbidden.New()
	}
	extGroupID := strings.TrimSpace(p.ExternalChatID)
	if extGroupID == "" {
		return nil, errors.New("conversation: external_chat_id is required for external group message")
	}
	at := inboundTime(p.CreatedAt)
	group, _, err := extgroup.EnsureGroup(ctx, tx, ch.TenantID, ch.ID, extGroupID, p.ChatTitle, at)
	if err != nil {
		return nil, err
	}
	senderExtID := strings.TrimSpace(p.From)
	if senderExtID == "" {
		senderExtID = extGroupID + ":unknown"
	}
	sender, err := extgroup.EnsureUser(ctx, tx, ch.TenantID, ch.ID, senderExtID, p.FromName, p.FromAvatarURL)
	if err != nil {
		return nil, err
	}
	confirmed, err := confirmAttachmentInputs(ctx, tx, a.down.Media.Files, sub, p.Attachments)
	if err != nil {
		return nil, err
	}
	incomingAtts, needsIngest := buildIncomingAttachments(ch.ID, p.IncomingAttachments)
	allAttachments := append(confirmed, incomingAtts...)

	c, created, opened, defaultMemberID, err := a.conversationForGroup(ctx, tx, ch, group, at)
	if err != nil {
		return nil, err
	}
	replyToID, err := resolveReplyToItemID(ctx, tx, c.TenantID, c.ID, ch.ID, p.ReplyToExternalID)
	if err != nil {
		return nil, err
	}
	msg := &Item{
		Kind:          Message,
		ActorType:     ExternalUserActor,
		ActorID:       sender.ID,
		Visibility:    Everyone,
		Content:       Content{Text: p.Text},
		Derived:       inboundDerived(p.ReplyContext),
		ChannelID:     &ch.ID,
		ReplyToItemID: replyToID,
	}
	if p.CreatedAt != nil && !p.CreatedAt.IsZero() {
		msg.CreatedAt = at
	}
	if p.ExternalID != "" {
		ext := p.ExternalID
		msg.ExternalID = &ext
	}
	return a.finalizeServiceInbound(ctx, tx, c, ch, msg, created, opened, defaultMemberID, allAttachments, needsIngest, p.TicketID, at)
}

// finalizeServiceInbound 完成服务型会话（单聊或外部群）入站的公共状态流转、自动指派、轮次、附件、提交与等待工单唤醒。
func (a *Receive) finalizeServiceInbound(
	ctx context.Context, tx database.Tx, c *Conversation, ch *channel.Channel, msg *Item,
	created bool, opened, defaultMemberID string, allAttachments []*Attachment, needsIngest bool, ticketID string, at time.Time,
) (*Received, error) {
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
	items = append(items, msg)

	// 自动指派：新会话命中工作区默认成员时直接指派并记录系统事件；否则按离开重分配与团队轮流分配规则处理
	if defaultMemberID != "" {
		c.AssigneeType, c.AssigneeID = MemberActor, defaultMemberID
		items = append(items, assignedEvent(system(c.TenantID), c, c.TeamID, "", "", ReasonDefault))
	} else {
		auto, err := releaseIfAway(ctx, tx, c)
		if err != nil {
			return nil, err
		}
		if len(auto) == 0 {
			next, err := roundRobin(ctx, tx, c)
			if err != nil {
				return nil, err
			}
			if next != nil {
				auto = append(auto, next)
			}
		}
		items = append(items, auto...)
	}
	if created || reopened {
		if err := openRound(ctx, tx, c, at); err != nil {
			return nil, err
		}
	}
	replyChID := ch.ID
	c.ReplyChannelID = &replyChID
	c.LastMessageAt = at
	if c.WaitingSince == nil {
		c.WaitingSince = &at
	}
	if err := commit(ctx, tx, a.down, c, items...); err != nil {
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
	if err := resumeWaitingTickets(ctx, tx, a.down, c.TenantID, c, ticketID, at); err != nil {
		return nil, err
	}
	dtos, err := toItemDTOs(ctx, tx, a.down.Media, []Item{*msg})
	if err != nil {
		return nil, err
	}
	return &Received{ConversationID: c.ID, Item: dtos[0], Created: created, Reopened: opened == "conversation.reopened"}, nil
}

// previous 返回之前已经写入的同一条消息（按渠道和外部编号）；没有时返回 nil。
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

// conversationForContact 在事务 tx 中为联系人 person 找到当前服务对象域下要归入的服务型会话并加行锁。
// 已认证用户（contact.User）与内部联系人（contact.Internal）在同服务域内跨渠道合并；访客线索（contact.Lead）按本渠道归入。
func (a *Receive) conversationForContact(ctx context.Context, tx bun.IDB, ch *channel.Channel, person *contact.Contact, at time.Time) (c *Conversation, created bool, opened string, defaultMemberID string, err error) {
	aud := ch.EffectiveAudience()
	c = new(Conversation)
	q := tx.NewSelect().Model(c).
		Where("tenant_id = ? AND kind = ? AND audience = ? AND contact_id = ?", ch.TenantID, Service, aud, person.ID)
	if person.Kind == contact.Lead {
		q = q.Where("channel_id = ?", ch.ID)
	}
	err = q.OrderExpr("last_message_at DESC, id DESC").Limit(1).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		c = nil
	} else if err != nil {
		return nil, false, "", "", errors.Wrap(err, "load latest contact conversation")
	}
	c, err = applyReopenWindow(ctx, tx, ch.TenantID, c, at)
	if err != nil {
		return nil, false, "", "", err
	}
	if c == nil {
		c, defaultMemberID, err = startContactConversation(ctx, tx, ch, person, at)
		return c, err == nil, "", defaultMemberID, err
	}
	return c, false, reopenEventName(c.Status), "", nil
}

// conversationForGroup 在事务 tx 中为外部群 group 找到当前服务型会话并加行锁，超出重开窗口或尚无会话时新建。
func (a *Receive) conversationForGroup(ctx context.Context, tx bun.IDB, ch *channel.Channel, group *extgroup.Group, at time.Time) (c *Conversation, created bool, opened string, defaultMemberID string, err error) {
	c = new(Conversation)
	err = tx.NewSelect().Model(c).
		Where("tenant_id = ? AND kind = ? AND external_group_id = ?", ch.TenantID, Service, group.ID).
		OrderExpr("last_message_at DESC, id DESC").Limit(1).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		c = nil
	} else if err != nil {
		return nil, false, "", "", errors.Wrap(err, "load latest external group conversation")
	}
	c, err = applyReopenWindow(ctx, tx, ch.TenantID, c, at)
	if err != nil {
		return nil, false, "", "", err
	}
	if c == nil {
		c, defaultMemberID, err = startGroupConversation(ctx, tx, ch, group, at)
		return c, err == nil, "", defaultMemberID, err
	}
	return c, false, reopenEventName(c.Status), "", nil
}

func applyReopenWindow(ctx context.Context, tx bun.IDB, tenantID string, c *Conversation, at time.Time) (*Conversation, error) {
	if c == nil || c.Status != Closed {
		return c, nil
	}
	var hours int
	if err := tx.NewSelect().Table("tenants").Column("reopen_window_hours").Where("id = ?", tenantID).Scan(ctx, &hours); err != nil {
		return nil, errors.Wrap(err, "load tenant reopen window")
	}
	window := time.Duration(hours) * time.Hour
	if window <= 0 || c.ClosedAt == nil || at.Sub(*c.ClosedAt) > window {
		return nil, nil
	}
	return c, nil
}

func reopenEventName(status Status) string {
	switch status {
	case Closed:
		return "conversation.reopened"
	case Snoozed:
		return "conversation.unsnoozed"
	default:
		return ""
	}
}

// resolveDefaultRouting 解析新建服务型会话的默认归属：优先团队（群级 > 渠道级）> 工作区默认指派（团队或成员）> 未指派。
func resolveDefaultRouting(ctx context.Context, tx bun.IDB, tenantID string, preferredTeams ...*string) (teamID *string, defaultMemberID string, err error) {
	for _, cand := range preferredTeams {
		if cand == nil || *cand == "" {
			continue
		}
		active, err := tx.NewSelect().Table("teams").
			Where("id = ? AND tenant_id = ? AND disabled_at IS NULL", *cand, tenantID).Exists(ctx)
		if err != nil {
			return nil, "", errors.Wrap(err, "check preferred default team")
		}
		if active {
			id := *cand
			return &id, "", nil
		}
	}
	cfg, err := NewAssignmentSettingsGroup(tx).Tenant(tenantID).Get(ctx)
	if err != nil {
		return nil, "", errors.Wrap(err, "load default assignment settings")
	}
	switch cfg.DefaultTarget {
	case DefaultTeam:
		if cfg.DefaultTeamID != "" {
			active, err := tx.NewSelect().Table("teams").
				Where("id = ? AND tenant_id = ? AND disabled_at IS NULL", cfg.DefaultTeamID, tenantID).Exists(ctx)
			if err != nil {
				return nil, "", errors.Wrap(err, "check workspace default team")
			}
			if active {
				id := cfg.DefaultTeamID
				return &id, "", nil
			}
		}
	case DefaultMember:
		if cfg.DefaultMemberID != "" {
			ref := identity.Ref{Type: member.Type, ID: cfg.DefaultMemberID}
			if _, err := member.RoleOf(ctx, authz.NewStore(tx, auth.Catalog), ref, tenantID); err == nil {
				active, err := tx.NewSelect().Table("members").
					Where("id = ? AND disabled_at IS NULL", cfg.DefaultMemberID).Exists(ctx)
				if err != nil {
					return nil, "", errors.Wrap(err, "check workspace default member")
				}
				if active {
					return nil, cfg.DefaultMemberID, nil
				}
			} else if !errors.Is(err, member.ErrNotFound) {
				return nil, "", err
			}
		}
	}
	return nil, "", nil
}

func startContactConversation(ctx context.Context, tx bun.IDB, ch *channel.Channel, person *contact.Contact, at time.Time) (*Conversation, string, error) {
	teamID, defaultMemberID, err := resolveDefaultRouting(ctx, tx, ch.TenantID, ch.TeamID)
	if err != nil {
		return nil, "", err
	}
	chID, replyChID := ch.ID, ch.ID
	c := &Conversation{
		ID: newID(), TenantID: ch.TenantID, Kind: Service, Audience: ch.EffectiveAudience(),
		ChannelID: &chID, ReplyChannelID: &replyChID, ContactID: &person.ID, TeamID: teamID,
		Status: Open, Priority: Normal, Language: person.Language, OpenedAt: at, LastMessageAt: at, Attributes: map[string]any{},
	}
	if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
		return nil, "", errors.Wrap(err, "insert contact conversation")
	}
	return c, defaultMemberID, join(ctx, tx, c.ID, ContactActor, person.ID)
}

func startGroupConversation(ctx context.Context, tx bun.IDB, ch *channel.Channel, group *extgroup.Group, at time.Time) (*Conversation, string, error) {
	teamID, defaultMemberID, err := resolveDefaultRouting(ctx, tx, ch.TenantID, group.TeamID, ch.TeamID)
	if err != nil {
		return nil, "", err
	}
	chID, replyChID := ch.ID, ch.ID
	c := &Conversation{
		ID: newID(), TenantID: ch.TenantID, Kind: Service, Audience: ch.EffectiveAudience(),
		ChannelID: &chID, ReplyChannelID: &replyChID, ExternalGroupID: &group.ID, TeamID: teamID,
		Status: Open, Priority: Normal, Language: group.Language, OpenedAt: at, LastMessageAt: at, Attributes: map[string]any{},
	}
	if _, err := tx.NewInsert().Model(c).Exec(ctx); err != nil {
		return nil, "", errors.Wrap(err, "insert external group conversation")
	}
	return c, defaultMemberID, nil
}

// receiveExternalChat 在事务 tx 中将个人商务渠道 ch 收到（或由归属成员在外部客户端同步发出）的消息写入外部商务聊天：
//   - 1:1 外部商务聊天（IsGroup=false）：解析对方联系人，网页商务链接按 (tenant_id, channel_id, contact_id) 复用或创建，
//     带 ExternalChatID 的个人号按 (tenant_id, channel_id, external_chat_id) 复用或创建；
//   - 外部商务群聊（IsGroup=true，如 Telegram 个人号群）：按 (tenant_id, channel_id, external_chat_id) 复用或创建，
//     群内发言人记为 external_users（actor_type=external_user），不再写入客户联系人表。
func (a *Receive) receiveExternalChat(ctx context.Context, tx database.Tx, sub identity.Subject, ch *channel.Channel, p ReceiveParams) (*Received, error) {
	ownerID := *ch.OwnerMemberID
	bp := channel.BusinessPolicyOf(ch.Settings)
	var person *contact.Contact
	var contactCreated bool
	var groupSender *extgroup.User
	var err error
	if p.IsGroup {
		if !p.FromOwner {
			senderExtID := strings.TrimSpace(p.From)
			if senderExtID == "" {
				senderExtID = strings.TrimSpace(p.ExternalChatID) + ":unknown"
			}
			groupSender, err = extgroup.EnsureUser(ctx, tx, ch.TenantID, ch.ID, senderExtID, p.FromName, p.FromAvatarURL)
			if err != nil {
				return nil, err
			}
		}
	} else if !p.FromOwner || strings.TrimSpace(p.From) != "" {
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
	at := inboundTime(p.CreatedAt)
	extChatID := strings.TrimSpace(p.ExternalChatID)

	var c *Conversation
	var cm *ChatMeta
	created := false

	// 查找已存在的外部商务聊天：带真实 external_chat_id 时按 chats 索引查，网页商务单聊按 conversations(channel_id, contact_id) 查
	if extChatID != "" {
		cm = new(ChatMeta)
		err = tx.NewSelect().Model(cm).
			Where("tenant_id = ? AND channel_id = ? AND external_chat_id = ?", ch.TenantID, ch.ID, extChatID).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			cm = nil
			err = nil
		} else if err != nil {
			return nil, errors.Wrap(err, "load external chat meta")
		}
	} else {
		if person == nil {
			return nil, errors.New("conversation: external_chat_id or contact is required for external chat")
		}
		c = new(Conversation)
		err = tx.NewSelect().Model(c).
			Where("tenant_id = ? AND kind = ? AND channel_id = ? AND contact_id = ?", ch.TenantID, Chat, ch.ID, person.ID).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			c = nil
			err = nil
		} else if err != nil {
			return nil, errors.Wrap(err, "load direct external chat conversation")
		}
		if c != nil {
			cm = new(ChatMeta)
			if err := tx.NewSelect().Model(cm).Where("conversation_id = ? AND tenant_id = ?", c.ID, ch.TenantID).For("UPDATE").Scan(ctx); err != nil {
				return nil, errors.Wrap(err, "load direct external chat meta")
			}
		}
	}

	if cm == nil {
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
		chID, replyChID := ch.ID, ch.ID
		c = &Conversation{
			ID:             newID(),
			TenantID:       ch.TenantID,
			Kind:           Chat,
			Audience:       ch.EffectiveAudience(),
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
			return nil, errors.Wrap(err, "insert external chat meta")
		}
		if err := join(ctx, tx, c.ID, MemberActor, ownerID); err != nil {
			return nil, err
		}
		if !p.IsGroup && person != nil {
			if err := join(ctx, tx, c.ID, ContactActor, person.ID); err != nil {
				return nil, err
			}
		}
		// 应用个人商务渠道策略：自动拉入默认协作成员
		if len(bp.DefaultCollaboratorIDs) > 0 {
			roles := authz.NewStore(tx, auth.Catalog)
			for _, mid := range bp.DefaultCollaboratorIDs {
				if mid == "" || mid == ownerID {
					continue
				}
				if _, err := member.RoleOf(ctx, roles, identity.Ref{Type: member.Type, ID: mid}, ch.TenantID); err == nil {
					if err := join(ctx, tx, c.ID, MemberActor, mid); err != nil {
						return nil, err
					}
				}
			}
		}
		created = true
	} else {
		if c == nil {
			c, err = find(ctx, tx, ch.TenantID, cm.ConversationID, true)
			if err != nil {
				return nil, err
			}
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
	switch {
	case p.FromOwner:
		actorType = MemberActor
		actorID = ownerID
	case groupSender != nil:
		actorType = ExternalUserActor
		actorID = groupSender.ID
	case person != nil:
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
		Derived:    inboundDerived(p.ReplyContext),
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

func inboundTime(t *time.Time) time.Time {
	if t != nil && !t.IsZero() {
		return t.UTC().Truncate(time.Microsecond)
	}
	return now()
}

func inboundDerived(replyContext map[string]any) map[string]any {
	if len(replyContext) == 0 {
		return nil
	}
	return map[string]any{"reply_context": replyContext}
}

// resolveReplyToItemID 根据外部引用消息编号，在同会话中查找对应的本地 timeline_items.id（兼容入站与出站消息）。
func resolveReplyToItemID(ctx context.Context, tx bun.IDB, tenantID, conversationID, channelID, extID string) (*string, error) {
	extID = strings.TrimSpace(extID)
	if extID == "" {
		return nil, nil
	}
	var itemID string
	err := tx.NewSelect().Table("timeline_items").Column("id").
		Where("tenant_id = ? AND conversation_id = ? AND channel_id = ? AND external_id = ?", tenantID, conversationID, channelID, extID).
		Limit(1).Scan(ctx, &itemID)
	if err == nil && itemID != "" {
		return &itemID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "resolve inbound reply_to item by timeline external_id")
	}
	err = tx.NewSelect().TableExpr("message_deliveries AS d").
		Join("JOIN timeline_items AS ti ON ti.id = d.item_id").
		ColumnExpr("ti.id").
		Where("ti.tenant_id = ? AND ti.conversation_id = ? AND ti.channel_id = ? AND d.external_id = ?", tenantID, conversationID, channelID, extID).
		Limit(1).Scan(ctx, &itemID)
	if err == nil && itemID != "" {
		return &itemID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "resolve inbound reply_to item by delivery external_id")
	}
	return nil, nil
}
