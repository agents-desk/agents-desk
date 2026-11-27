package conversation

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/contact"
	"agents-desk/internal/push"
)

// ContactConversationMeta 是连续时间线中引用的服务型会话轮次摘要，供客户端渲染轮次结束分隔线与满意度评价卡片。
type ContactConversationMeta struct {
	ID              string     `json:"id"`
	Status          Status     `json:"status" doc:"打开或已关闭；稍后处理的会话对客户显示为打开"`
	ClosedAt        *time.Time `json:"closed_at,omitempty" doc:"最近一次关闭的时间"`
	RatingRequested bool       `json:"rating_requested" doc:"这次关闭是否向客户发出了评价邀请；为真且 rating 为空时可以评价"`
	Rating          *Rating    `json:"rating,omitempty" doc:"客户对最近一次关闭的评价"`
}

// ContactTimelineDTO 是客户侧跨会话连续时间线的一页响应：
// 按时间升序返回对客户可见的消息，并附带涉及会话的轮次元数据、最新会话摘要和未读总数。
type ContactTimelineDTO struct {
	Items              []ContactItemDTO                   `json:"items" nullable:"false" doc:"按时间升序排列的跨会话可见消息"`
	Conversations      map[string]ContactConversationMeta `json:"conversations" doc:"本页消息与最新会话涉及的轮次摘要，键为会话 ID"`
	LatestConversation *ContactConversationMeta           `json:"latest_conversation,omitempty" doc:"该客户最近一条服务型会话摘要；从未发过消息时为空"`
	Unread             int                                `json:"unread" doc:"客户尚未读到的客服消息总数"`
	HasMore            bool                               `json:"has_more" doc:"是否还有更早的历史消息可供向上翻页加载"`
}

// ContactStream 读取联系人在网页聊天插件中的跨会话连续时间线。
type ContactStream struct {
	db    *bun.DB
	media Media
}

// NewContactStream 返回读取客户连续时间线的动作；media 用于填充消息附带的附件地址。
func NewContactStream(db *bun.DB, media ...Media) *ContactStream {
	var m Media
	if len(media) > 0 {
		m = media[0]
	}
	return &ContactStream{db: db, media: m}
}

// Handle 返回联系人 sub 在渠道 channelID 中可见的跨会话连续时间线：
//   - 已登录用户（contact.User）跨所有渠道读取本工作区名下全部服务型会话的公开消息；
//   - 访客线索（contact.Lead）仅读取来源渠道为 channelID 的服务型会话；
//   - beforeID 非空时向前加载更早的历史消息（按时间降序取 limit+1 条再反转为升序）；
//   - afterID 非空时向后增量拉取新消息（按时间升序取 limit 条）；
//   - 两者均为空时读取最新的一页消息。若游标对应的消息已被合并删除，自动降级为读取最新一页。
func (a *ContactStream) Handle(ctx context.Context, sub identity.Subject, channelID, beforeID, afterID string, limit int) (*ContactTimelineDTO, error) {
	if sub.Type != contact.Type {
		return nil, ErrForbidden.New()
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	kind, exists, err := contactKindOf(ctx, a.db, sub)
	if err != nil {
		return nil, err
	}
	empty := &ContactTimelineDTO{
		Items:         []ContactItemDTO{},
		Conversations: map[string]ContactConversationMeta{},
	}
	if !exists {
		return empty, nil
	}
	targetKind, restrictChannel, err := contactChannelScope(ctx, a.db, sub.Tenant, channelID, kind)
	if err != nil {
		return nil, err
	}

	// 读取可选游标项的时间戳与 ID，避免跨会话合并后仅靠 created_at 出现同毫秒漏项
	type cursorPoint struct {
		ID        string    `bun:"id"`
		CreatedAt time.Time `bun:"created_at"`
	}
	loadCursor := func(itemID string) (*cursorPoint, error) {
		var pt cursorPoint
		q := a.db.NewSelect().TableExpr("timeline_items AS ti").
			Join("JOIN conversations AS c ON c.id = ti.conversation_id").
			ColumnExpr("ti.id, ti.created_at").
			Where("ti.id = ? AND ti.tenant_id = ? AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ?",
				itemID, sub.Tenant, sub.Tenant, targetKind, sub.ID)
		if restrictChannel {
			q = q.Where("c.channel_id = ?", channelID)
		}
		err := q.Scan(ctx, &pt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, errors.Wrap(err, "load contact stream cursor")
		}
		return &pt, nil
	}

	var beforePt, afterPt *cursorPoint
	if afterID != "" {
		afterPt, err = loadCursor(afterID)
		if err != nil {
			return nil, err
		}
	} else if beforeID != "" {
		beforePt, err = loadCursor(beforeID)
		if err != nil {
			return nil, err
		}
	}

	var rawItems []Item
	hasMore := false
	q := a.db.NewSelect().Model(&rawItems).
		Join("JOIN conversations AS c ON c.id = item.conversation_id").
		Where("item.tenant_id = ? AND item.visibility = ? AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ?",
			sub.Tenant, Everyone, sub.Tenant, targetKind, sub.ID).
		Where("NOT EXISTS (SELECT 1 FROM message_deliveries d WHERE d.item_id = item.id AND d.status = ?)", Failed).
		Where("NOT (item.actor_type <> ? AND item.derived ? 'pending_translation')", ContactActor)
	if restrictChannel {
		q = q.Where("c.channel_id = ?", channelID)
	}
	if afterPt != nil {
		q = q.Where("(item.created_at, item.id) > (?, ?)", afterPt.CreatedAt, afterPt.ID).
			OrderExpr("item.created_at ASC, item.id ASC").
			Limit(int64(limit + 1))
		if err := q.Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "list contact stream after cursor")
		}
		if len(rawItems) > limit {
			hasMore = true
			rawItems = rawItems[:limit]
		}
	} else {
		if beforePt != nil {
			q = q.Where("(item.created_at, item.id) < (?, ?)", beforePt.CreatedAt, beforePt.ID)
		}
		q = q.OrderExpr("item.created_at DESC, item.id DESC").Limit(int64(limit + 1))
		if err := q.Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "list contact stream items")
		}
		if len(rawItems) > limit {
			hasMore = true
			rawItems = rawItems[:limit]
		}
		slices.Reverse(rawItems)
	}

	items, err := toContactItemDTOs(ctx, a.db, a.media, channelID, rawItems)
	if err != nil {
		return nil, err
	}

	// 读取客户最近一条会话摘要与全量未读数
	var latest Conversation
	latestQ := a.db.NewSelect().Model(&latest).
		Where("tenant_id = ? AND kind = ? AND contact_id = ?", sub.Tenant, targetKind, sub.ID)
	if restrictChannel {
		latestQ = latestQ.Where("channel_id = ?", channelID)
	}
	var latestMeta *ContactConversationMeta
	err = latestQ.OrderExpr("last_message_at DESC, id DESC").Limit(1).Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "load contact latest conversation")
	}
	if err == nil {
		m := toContactConversationMeta(&latest)
		latestMeta = &m
	}

	var unread int
	unreadQ := a.db.NewSelect().TableExpr("timeline_items AS ti").
		Join("JOIN conversations AS c ON c.id = ti.conversation_id").
		Join("JOIN message_deliveries AS d ON d.item_id = ti.id").
		ColumnExpr("count(*)").
		Where("ti.tenant_id = ? AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ?",
			sub.Tenant, sub.Tenant, targetKind, sub.ID).
		Where("ti.actor_type <> ? AND d.status NOT IN (?)", ContactActor, bun.In([]DeliveryStatus{Read, Failed})).
		Where("NOT (ti.derived ? 'pending_translation')")
	if restrictChannel {
		unreadQ = unreadQ.Where("c.channel_id = ?", channelID)
	}
	if err := unreadQ.Scan(ctx, &unread); err != nil {
		return nil, errors.Wrap(err, "count contact stream unread")
	}

	// 批量补齐本页消息引用的所有会话元数据字典
	convIDSet := map[string]bool{}
	convIDs := make([]string, 0, 4)
	if latestMeta != nil {
		convIDSet[latestMeta.ID] = true
		convIDs = append(convIDs, latestMeta.ID)
	}
	for _, it := range rawItems {
		if !convIDSet[it.ConversationID] {
			convIDSet[it.ConversationID] = true
			convIDs = append(convIDs, it.ConversationID)
		}
	}
	convMap := make(map[string]ContactConversationMeta, len(convIDs))
	if len(convIDs) > 0 {
		var convs []Conversation
		if err := a.db.NewSelect().Model(&convs).
			Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In(convIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load contact stream conversations")
		}
		for i := range convs {
			convMap[convs[i].ID] = toContactConversationMeta(&convs[i])
		}
	}

	return &ContactTimelineDTO{
		Items:              items,
		Conversations:      convMap,
		LatestConversation: latestMeta,
		Unread:             unread,
		HasMore:            hasMore,
	}, nil
}

// toContactConversationMeta 把服务型会话转换为客户侧轮次摘要；稍后处理对客户显示为打开。
func toContactConversationMeta(c *Conversation) ContactConversationMeta {
	status := c.Status
	if status == Snoozed {
		status = Open
	}
	m := ContactConversationMeta{
		ID:       c.ID,
		Status:   status,
		ClosedAt: c.ClosedAt,
	}
	if c.Status == Closed {
		m.RatingRequested = c.ratingRequestedSinceClosed()
		if c.ratedSinceClosed() {
			m.Rating = c.rating()
		}
	}
	return m
}

// MarkAllReadByContact 标记联系人在连续时间线中已读到的全部客服消息，并向对应会话的可见成员推送刷新。
type MarkAllReadByContact struct {
	db   *bun.DB
	down Downstream
}

// NewMarkAllReadByContact 返回客户连续时间线标记已读的动作。
func NewMarkAllReadByContact(db *bun.DB, down Downstream) *MarkAllReadByContact {
	return &MarkAllReadByContact{db: db, down: down}
}

// Handle 将联系人 sub 在渠道 channelID 可见的全部客服未读消息标记为已读（若传入 upToItemID 则截至该项）；
// 有消息状态发生变化时，在事务提交后推送给对应会话的可见成员。
func (a *MarkAllReadByContact) Handle(ctx context.Context, sub identity.Subject, channelID, upToItemID string) error {
	if sub.Type != contact.Type {
		return ErrForbidden.New()
	}
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		kind, exists, err := contactKindOf(ctx, tx, sub)
		if err != nil || !exists {
			return err
		}
		targetKind, restrictChannel, err := contactChannelScope(ctx, tx, sub.Tenant, channelID, kind)
		if err != nil {
			return err
		}
		var cutoffTime time.Time
		var cutoffID string
		if upToItemID != "" {
			var pt struct {
				ID        string    `bun:"id"`
				CreatedAt time.Time `bun:"created_at"`
			}
			q := tx.NewSelect().TableExpr("timeline_items AS ti").
				Join("JOIN conversations AS c ON c.id = ti.conversation_id").
				ColumnExpr("ti.id, ti.created_at").
				Where("ti.id = ? AND ti.tenant_id = ? AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ?",
					upToItemID, sub.Tenant, sub.Tenant, targetKind, sub.ID)
			if restrictChannel {
				q = q.Where("c.channel_id = ?", channelID)
			}
			err := q.Scan(ctx, &pt)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return errors.Wrap(err, "load read cutoff item")
			}
			cutoffTime, cutoffID = pt.CreatedAt, pt.ID
		}

		// 批量更新所有可见会话中尚未已读的客服或商务回复消息为已读，并收集受影响的会话 ID
		var affectedConvIDs []string
		at := now()
		if cutoffID != "" && !restrictChannel {
			err = tx.NewRaw(`UPDATE message_deliveries d SET status = ?, updated_at = ?
				FROM timeline_items t
				JOIN conversations c ON c.id = t.conversation_id
				WHERE d.item_id = t.id
				  AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ?
				  AND t.actor_type <> ? AND d.status IN (?)
				  AND (t.created_at, t.id) <= (?, ?)
				RETURNING t.conversation_id`,
				Read, at, sub.Tenant, targetKind, sub.ID, ContactActor, bun.In([]DeliveryStatus{Sending, Delivered}), cutoffTime, cutoffID).
				Scan(ctx, &affectedConvIDs)
		} else if cutoffID != "" {
			err = tx.NewRaw(`UPDATE message_deliveries d SET status = ?, updated_at = ?
				FROM timeline_items t
				JOIN conversations c ON c.id = t.conversation_id
				WHERE d.item_id = t.id
				  AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ? AND c.channel_id = ?
				  AND t.actor_type <> ? AND d.status IN (?)
				  AND (t.created_at, t.id) <= (?, ?)
				RETURNING t.conversation_id`,
				Read, at, sub.Tenant, targetKind, sub.ID, channelID, ContactActor, bun.In([]DeliveryStatus{Sending, Delivered}), cutoffTime, cutoffID).
				Scan(ctx, &affectedConvIDs)
		} else if !restrictChannel {
			err = tx.NewRaw(`UPDATE message_deliveries d SET status = ?, updated_at = ?
				FROM timeline_items t
				JOIN conversations c ON c.id = t.conversation_id
				WHERE d.item_id = t.id
				  AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ?
				  AND t.actor_type <> ? AND d.status IN (?)
				RETURNING t.conversation_id`,
				Read, at, sub.Tenant, targetKind, sub.ID, ContactActor, bun.In([]DeliveryStatus{Sending, Delivered})).
				Scan(ctx, &affectedConvIDs)
		} else {
			err = tx.NewRaw(`UPDATE message_deliveries d SET status = ?, updated_at = ?
				FROM timeline_items t
				JOIN conversations c ON c.id = t.conversation_id
				WHERE d.item_id = t.id
				  AND c.tenant_id = ? AND c.kind = ? AND c.contact_id = ? AND c.channel_id = ?
				  AND t.actor_type <> ? AND d.status IN (?)
				RETURNING t.conversation_id`,
				Read, at, sub.Tenant, targetKind, sub.ID, channelID, ContactActor, bun.In([]DeliveryStatus{Sending, Delivered})).
				Scan(ctx, &affectedConvIDs)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errors.Wrap(err, "mark contact stream read")
		}
		if len(affectedConvIDs) == 0 || a.down.Hub == nil {
			return nil
		}

		// 去重后为每条受影响的会话推送已读更新给可见成员
		seen := map[string]bool{}
		unique := make([]string, 0, len(affectedConvIDs))
		for _, cid := range affectedConvIDs {
			if !seen[cid] {
				seen[cid] = true
				unique = append(unique, cid)
			}
		}
		var convs []Conversation
		if err := tx.NewSelect().Model(&convs).Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In(unique)).Scan(ctx); err != nil {
			return errors.Wrap(err, "load conversations for read push")
		}
		for i := range convs {
			c := &convs[i]
			audience, err := audienceOf(ctx, tx, a.down.Roles, c, nil)
			if err != nil {
				return err
			}
			if len(audience) == 0 {
				continue
			}
			hub, tenant, ev := a.down.Hub, c.TenantID, push.Event{Type: pushType(c.Kind), ID: c.ID, Seq: c.LastSeq}
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Wrap(push.Send(ctx, hub, tenant, audience, ev), "push stream messages read")
			})
		}
		return nil
	})
}

// TypingOnActiveByContact 推送客户正在输入状态到其当前活跃服务型会话的实时频道。
type TypingOnActiveByContact struct {
	db   *bun.DB
	down Downstream
}

// NewTypingOnActiveByContact 返回客户在活跃会话中正在输入的动作。
func NewTypingOnActiveByContact(db *bun.DB, down Downstream) *TypingOnActiveByContact {
	return &TypingOnActiveByContact{db: db, down: down}
}

// Handle 查找联系人 sub 当前处于活跃态（open/snoozed）的服务型会话并广播正在输入事件；
// 尚无活跃会话时静默返回 nil。
func (a *TypingOnActiveByContact) Handle(ctx context.Context, sub identity.Subject, channelID string) error {
	if sub.Type != contact.Type {
		return ErrForbidden.New()
	}
	if a.down.Hub == nil {
		return nil
	}
	kind, exists, err := contactKindOf(ctx, a.db, sub)
	if err != nil || !exists {
		return err
	}
	targetKind, restrictChannel, err := contactChannelScope(ctx, a.db, sub.Tenant, channelID, kind)
	if err != nil {
		return err
	}
	var c Conversation
	q := a.db.NewSelect().Model(&c).
		Where("tenant_id = ? AND kind = ? AND contact_id = ? AND status IN (?)",
			sub.Tenant, targetKind, sub.ID, bun.In([]Status{Open, Snoozed}))
	if restrictChannel {
		q = q.Where("channel_id = ?", channelID)
	}
	err = q.OrderExpr("last_message_at DESC, id DESC").Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load active conversation for typing")
	}
	ev := Typing{Type: "typing", Actor: Actor{Type: ContactActor, ID: sub.ID}}
	return errors.Wrap(a.down.Hub.PublishJSON(ctx, Channel(c.TenantID, c.ID), ev), "push active contact typing")
}
