package conversation

import (
	"context"
	"database/sql"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/push"
)

// OnContactMerge 返回供 contact.Merge 使用的事务钩子：
//   - BeforeMigrate 在迁移会话前先归并两个联系人各自可能存在的活跃服务型会话，严格满足唯一活跃约束；
//   - AfterMigrate 在迁移完成后通知受影响会话的可见成员刷新。
func OnContactMerge(down Downstream) contact.MergeHooks {
	return contact.MergeHooks{
		BeforeMigrate: func(ctx context.Context, tx database.Tx, sub identity.Subject, targetContactID, sourceContactID string) error {
			return MergeActiveBetweenContacts(ctx, tx, down, sub, targetContactID, sourceContactID)
		},
		AfterMigrate: func(ctx context.Context, tx database.Tx, sub identity.Subject, _ string, affectedConvIDs []string) error {
			return NotifyUpdated(ctx, tx, down, sub.Tenant, affectedConvIDs)
		},
	}
}

// BeforeContactMerge 返回供 contact.LinkUser 使用的前置归并钩子，在自动将线索合并到已登录用户前先归并双方活跃会话。
func BeforeContactMerge(down Downstream, sub identity.Subject) contact.BeforeMergeHook {
	return func(ctx context.Context, tx database.Tx, targetContactID, sourceContactID string) error {
		return MergeActiveBetweenContacts(ctx, tx, down, sub, targetContactID, sourceContactID)
	}
}

// MergeActiveBetweenContacts 在同一事务中按服务对象类型（audience）检查 targetContactID 与 sourceContactID 是否各自拥有未关闭（open/snoozed）的
// 服务型会话：同一 audience 下双方都存在活跃会话时，将来源联系人的活跃会话归并进目标联系人的活跃会话并删除来源活跃会话，
// 保证后续迁移来源联系人的其余历史会话时不触发“同一联系人同服务对象最多一条活跃服务型会话”唯一约束冲突。
func MergeActiveBetweenContacts(ctx context.Context, tx database.Tx, down Downstream, sub identity.Subject, targetContactID, sourceContactID string) error {
	if targetContactID == "" || sourceContactID == "" || targetContactID == sourceContactID {
		return nil
	}
	var active []Conversation
	err := tx.NewSelect().Model(&active).
		Where("tenant_id = ? AND kind = ? AND contact_id IN (?) AND status IN (?)",
			sub.Tenant, Service, bun.In([]string{targetContactID, sourceContactID}), bun.In([]Status{Open, Snoozed})).
		OrderExpr("last_message_at DESC, id DESC").Scan(ctx)
	if err != nil {
		return errors.Wrap(err, "load active conversations for contact merge")
	}
	type pair struct {
		targetID string
		sourceID string
	}
	byAudience := map[channel.Audience]*pair{}
	var audiences []channel.Audience
	for i := range active {
		if active[i].ContactID == nil {
			continue
		}
		aud := active[i].Audience
		p := byAudience[aud]
		if p == nil {
			p = &pair{}
			byAudience[aud] = p
			audiences = append(audiences, aud)
		}
		switch *active[i].ContactID {
		case targetContactID:
			if p.targetID == "" {
				p.targetID = active[i].ID
			}
		case sourceContactID:
			if p.sourceID == "" {
				p.sourceID = active[i].ID
			}
		}
	}
	slices.Sort(audiences)
	for _, aud := range audiences {
		p := byAudience[aud]
		if p.targetID == "" || p.sourceID == "" {
			continue
		}
		if _, err := mergePair(ctx, tx, down, sub, p.targetID, p.sourceID, targetContactID, sourceContactID); err != nil {
			return err
		}
	}
	return nil
}

// mergePair 在事务 tx 中把 sourceID 合并到 targetID 中：
//   - 按 ID 升序加行锁，防止并发合并死锁；
//   - 按时间顺序归并全部时间线条目并重排连续 seq，保留各消息原有的 channel_id 与关联附件/送达状态；
//   - 合并参与者、标签、工单关联、属性、已读进度与会话状态，在目标时间线末尾追加 conversation.merged 事件后删除来源会话。
func mergePair(ctx context.Context, tx database.Tx, down Downstream, sub identity.Subject, targetID, sourceID, targetContactID, sourceContactID string) (*Conversation, error) {
	if targetID == sourceID {
		return nil, nil
	}
	first, second := targetID, sourceID
	if first > second {
		first, second = second, first
	}
	var locked []Conversation
	err := tx.NewSelect().Model(&locked).
		Where("conversation.tenant_id = ? AND conversation.id IN (?)", sub.Tenant, bun.In([]string{first, second})).
		OrderExpr("conversation.id ASC").For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "lock conversations for merge")
	}
	var target, source *Conversation
	for i := range locked {
		switch locked[i].ID {
		case targetID:
			target = &locked[i]
		case sourceID:
			source = &locked[i]
		}
	}
	if target == nil || source == nil {
		return nil, ErrNotFound.New()
	}
	if target.Kind != Service || source.Kind != Service {
		return nil, ErrNotFound.New()
	}

	// 记录合并前两者的可见成员，以便提交后把合并与删除都推送出去
	audienceSet := map[string]bool{}
	if down.Hub != nil {
		for _, conv := range []*Conversation{target, source} {
			aud, err := audienceOf(ctx, tx, down.Roles, conv, nil)
			if err != nil {
				return nil, err
			}
			for _, m := range aud {
				audienceSet[m] = true
			}
		}
	}

	// 查询来源渠道名用于系统事件留痕
	var sourceChannelName string
	if source.ChannelID != nil {
		ch, err := channel.Find(ctx, tx, sub.Tenant, *source.ChannelID)
		if err != nil && !errors.Is(err, channel.ErrNotFound) {
			return nil, err
		}
		if ch != nil {
			sourceChannelName = ch.Name
		}
	}

	// 1. 读出两个会话的全部时间线项并按时间升序归并排序
	var allItems []Item
	if err := tx.NewSelect().Model(&allItems).
		Where("tenant_id = ? AND conversation_id IN (?)", sub.Tenant, bun.In([]string{target.ID, source.ID})).
		Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load items for conversation merge")
	}
	movedItems := 0
	for i := range allItems {
		if allItems[i].ConversationID == source.ID {
			movedItems++
		}
	}
	sort.SliceStable(allItems, func(i, j int) bool {
		if !allItems[i].CreatedAt.Equal(allItems[j].CreatedAt) {
			return allItems[i].CreatedAt.Before(allItems[j].CreatedAt)
		}
		if allItems[i].Seq != allItems[j].Seq {
			return allItems[i].Seq < allItems[j].Seq
		}
		return allItems[i].ID < allItems[j].ID
	})

	// 2. 先将现有条目的 seq 平移到大偏移段，避开 (conversation_id, seq) 唯一约束冲突，再按归并顺序写回 1..N
	const seqOffset int64 = 1_000_000_000
	const seqBatchSize = 2000
	if _, err := tx.NewRaw(
		`UPDATE timeline_items SET seq = seq + ? WHERE tenant_id = ? AND conversation_id IN (?)`,
		seqOffset, sub.Tenant, bun.In([]string{target.ID, source.ID}),
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "offset timeline seq for merge")
	}
	var syncIDs []string
	var latestContactChannelID *string
	itemIDs := make([]string, len(allItems))
	newSeqs := make([]int64, len(allItems))
	for i := range allItems {
		newSeq := int64(i + 1)
		it := &allItems[i]
		itemIDs[i] = it.ID
		newSeqs[i] = newSeq
		if it.Searchable() {
			syncIDs = append(syncIDs, it.ID)
		}
		if it.Kind == Message && it.ActorType.IsExternal() && it.ChannelID != nil {
			chID := *it.ChannelID
			latestContactChannelID = &chID
		}
	}
	for start := 0; start < len(itemIDs); start += seqBatchSize {
		end := min(start+seqBatchSize, len(itemIDs))
		batchIDs := itemIDs[start:end]
		batchSeqs := newSeqs[start:end]
		if _, err := tx.NewRaw(
			`UPDATE timeline_items AS t
			 SET conversation_id = ?, seq = m.new_seq
			 FROM unnest(?::text[], ?::bigint[]) AS m(id, new_seq)
			 WHERE t.id = m.id::uuid AND t.tenant_id = ?`,
			target.ID, pgdialect.Array(batchIDs), pgdialect.Array(batchSeqs), sub.Tenant,
		).Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "renumber merged timeline items")
		}
		if _, err := tx.NewRaw(
			`UPDATE conversation_mentions AS cm
			 SET conversation_id = ?, seq = m.new_seq
			 FROM unnest(?::text[], ?::bigint[]) AS m(id, new_seq)
			 WHERE cm.item_id = m.id::uuid AND cm.tenant_id = ?`,
			target.ID, pgdialect.Array(batchIDs), pgdialect.Array(batchSeqs), sub.Tenant,
		).Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "update merged mention seqs")
		}
	}
	target.LastSeq = int64(len(allItems))

	// 3. 合并参与者、会话标签、工单关联、修订/撤回/更正记录、服务轮次与 AI/自动化记录
	if _, err := tx.NewRaw(
		`INSERT INTO conversation_participants (id, created_at, updated_at, conversation_id, participant_type, participant_id)
		 SELECT gen_random_uuid(), created_at, updated_at, ?, participant_type, participant_id
		 FROM conversation_participants
		 WHERE conversation_id = ? AND left_at IS NULL
		 ON CONFLICT DO NOTHING`,
		target.ID, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "merge conversation participants")
	}
	if _, err := tx.NewRaw(
		`INSERT INTO conversation_tags (tenant_id, conversation_id, tag_id, created_at)
		 SELECT tenant_id, ?, tag_id, created_at
		 FROM conversation_tags
		 WHERE conversation_id = ?
		 ON CONFLICT DO NOTHING`,
		target.ID, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "merge conversation tags")
	}
	at := database.Now()
	// 更新工单来源会话引用，保留来源会话关联工单。
	if _, err := tx.NewRaw(
		`UPDATE ticket_targets SET source_conversation_id = ?, updated_at = ?
		 WHERE tenant_id = ? AND source_conversation_id = ?`,
		target.ID, at, sub.Tenant, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "migrate merged ticket source conversations")
	}
	if _, err := tx.NewRaw(
		`UPDATE message_corrections SET conversation_id = ? WHERE tenant_id = ? AND conversation_id = ?`,
		target.ID, sub.Tenant, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "migrate merged message corrections")
	}
	if _, err := tx.NewRaw(
		`UPDATE message_revisions SET conversation_id = ? WHERE tenant_id = ? AND conversation_id = ?`,
		target.ID, sub.Tenant, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "migrate merged message revisions")
	}
	if _, err := tx.NewRaw(
		`UPDATE message_recalls SET conversation_id = ? WHERE tenant_id = ? AND conversation_id = ?`,
		target.ID, sub.Tenant, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "migrate merged message recalls")
	}
	if err := mergeRounds(ctx, tx, sub.Tenant, target.ID, source.ID, at); err != nil {
		return nil, err
	}
	if _, err := CancelActiveGenerationsInTx(ctx, tx, down, sub.Tenant, source.ID, "merged"); err != nil {
		return nil, err
	}
	if _, err := tx.NewRaw(
		`UPDATE generations SET conversation_id = ? WHERE tenant_id = ? AND conversation_id = ?`,
		target.ID, sub.Tenant, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "migrate merged generations")
	}
	if _, err := tx.NewRaw(
		`UPDATE agent_turns SET conversation_id = ? WHERE tenant_id = ? AND conversation_id = ?`,
		target.ID, sub.Tenant, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "migrate merged agent turns")
	}
	if _, err := tx.NewRaw(
		`UPDATE automation_runs SET conversation_id = ? WHERE tenant_id = ? AND conversation_id = ?`,
		target.ID, sub.Tenant, source.ID,
	).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "migrate merged automation runs")
	}

	// 4. 合并已读进度：只有成员在合并前已读完源和目标两个会话全部内容时，合并后才视为已读到最新，否则保留未读提醒
	var reads []struct {
		MemberID       string `bun:"member_id"`
		ConversationID string `bun:"conversation_id"`
		LastReadSeq    int64  `bun:"last_read_seq"`
	}
	if err := tx.NewSelect().Table("conversation_reads").
		Column("member_id", "conversation_id", "last_read_seq").
		Where("conversation_id IN (?)", bun.In([]string{target.ID, source.ID})).
		Scan(ctx, &reads); err != nil {
		return nil, errors.Wrap(err, "load conversation reads for merge")
	}
	byMember := map[string]map[string]int64{}
	for _, r := range reads {
		if byMember[r.MemberID] == nil {
			byMember[r.MemberID] = map[string]int64{}
		}
		byMember[r.MemberID][r.ConversationID] = r.LastReadSeq
	}
	for memberID, m := range byMember {
		tRead := m[target.ID]
		sRead := m[source.ID]
		newRead := int64(0)
		if tRead >= target.LastSeq-int64(movedItems) && sRead >= source.LastSeq {
			newRead = target.LastSeq
		}
		if _, err := tx.NewRaw(
			`INSERT INTO conversation_reads (member_id, conversation_id, last_read_seq, updated_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (member_id, conversation_id) DO UPDATE SET last_read_seq = EXCLUDED.last_read_seq, updated_at = EXCLUDED.updated_at`,
			memberID, target.ID, newRead, at,
		).Exec(ctx); err != nil {
			return nil, errors.Wrap(err, "upsert merged conversation read")
		}
	}

	// 5. 合并会话状态、时间、归属与属性
	switch {
	case target.Status == Open || source.Status == Open:
		target.Status = Open
		target.SnoozedUntil = nil
		target.ClosedAt = nil
	case target.Status == Snoozed || source.Status == Snoozed:
		target.Status = Snoozed
		target.ClosedAt = nil
		if target.SnoozedUntil == nil || (source.SnoozedUntil != nil && source.SnoozedUntil.Before(*target.SnoozedUntil)) {
			target.SnoozedUntil = source.SnoozedUntil
		}
	}
	if source.Priority == High {
		target.Priority = High
	}
	if source.OpenedAt.Before(target.OpenedAt) {
		target.OpenedAt = source.OpenedAt
	}
	if source.WaitingSince != nil && (target.WaitingSince == nil || source.WaitingSince.Before(*target.WaitingSince)) {
		t := *source.WaitingSince
		target.WaitingSince = &t
	}
	if source.FirstReplyAt != nil && (target.FirstReplyAt == nil || source.FirstReplyAt.Before(*target.FirstReplyAt)) {
		t := *source.FirstReplyAt
		target.FirstReplyAt = &t
	}
	if source.LastMessageAt.After(target.LastMessageAt) {
		target.LastMessageAt = source.LastMessageAt
	}
	if latestContactChannelID != nil {
		target.ReplyChannelID = latestContactChannelID
	}
	if target.TeamID == nil && source.TeamID != nil {
		tid := *source.TeamID
		target.TeamID = &tid
	}
	if target.AssigneeType == "" && source.AssigneeType != "" {
		target.AssigneeType, target.AssigneeID = source.AssigneeType, source.AssigneeID
	}
	if len(source.Attributes) > 0 {
		mergedAttrs := map[string]any{}
		maps.Copy(mergedAttrs, source.Attributes)
		maps.Copy(mergedAttrs, target.Attributes)
		target.Attributes = mergedAttrs
	}

	// 6. 在目标会话末尾追加合并系统事件，并同步删除来源会话
	mergeEvent := &Item{
		ID:             database.NewID(),
		CreatedAt:      at,
		UpdatedAt:      at,
		TenantID:       target.TenantID,
		ConversationID: target.ID,
		Seq:            target.LastSeq + 1,
		Kind:           Event,
		ActorType:      actorOf(sub),
		ActorID:        sub.ID,
		Visibility:     MembersOnly,
		Content: Content{
			Event: "conversation.merged",
			Data: map[string]any{
				"source_id":           source.ID,
				"source_channel_name": sourceChannelName,
				"moved_items":         movedItems,
			},
		},
		Derived: map[string]any{},
	}
	target.LastSeq = mergeEvent.Seq
	if _, err := tx.NewInsert().Model(mergeEvent).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "insert merge event")
	}
	if _, err := tx.NewUpdate().Model(target).WherePK().Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "update merged target conversation")
	}
	if _, err := tx.NewDelete().Model((*Conversation)(nil)).Where("id = ? AND tenant_id = ?", source.ID, sub.Tenant).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "delete merged source conversation")
	}

	// 7. 重建迁入与重排条目的全文搜索索引
	if len(syncIDs) > 0 {
		if err := SearchIndex.Sync(ctx, tx, syncIDs...); err != nil {
			return nil, err
		}
	}

	// 8. 事务提交后推送更新给受影响的成员和联系人网页插件
	if down.Hub != nil {
		afterAud, err := audienceOf(ctx, tx, down.Roles, target, nil)
		if err != nil {
			return nil, err
		}
		for _, m := range afterAud {
			audienceSet[m] = true
		}
		audience := make([]string, 0, len(audienceSet))
		for m := range audienceSet {
			audience = append(audience, m)
		}
		slices.Sort(audience)
		if len(audience) > 0 {
			hub, tenant := down.Hub, target.TenantID
			targetEv := push.Event{Type: push.Conversation, ID: target.ID, Seq: target.LastSeq}
			sourceEv := push.Event{Type: push.Conversation, ID: source.ID, Seq: source.LastSeq}
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Join(
					errors.Wrap(push.Send(ctx, hub, tenant, audience, targetEv), "push merged target conversation"),
					errors.Wrap(push.Send(ctx, hub, tenant, audience, sourceEv), "push merged source conversation"),
				)
			})
		}
	}
	if down.ContactPush != nil && targetContactID != "" {
		contactIDs := []string{targetContactID}
		if sourceContactID != "" && sourceContactID != targetContactID {
			contactIDs = append(contactIDs, sourceContactID)
		}
		var widgetIDs []string
		err := tx.NewSelect().TableExpr("contact_identities AS ci").
			ColumnExpr("DISTINCT ci.channel_id").
			Join("JOIN channels AS ch ON ch.id = ci.channel_id").
			Where("ci.tenant_id = ? AND ci.contact_id IN (?) AND ch.kind IN (?) AND ch.enabled = true", target.TenantID, bun.In(contactIDs), bun.In(realtimeHubKinds(down))).
			Scan(ctx, &widgetIDs)
		if err != nil {
			return nil, errors.Wrap(err, "load contact widget channels after merge")
		}
		if len(widgetIDs) > 0 {
			notify, contactID := down.ContactPush, targetContactID
			ev := push.Event{Type: push.Conversation, ID: target.ID, Seq: target.LastSeq}
			tx.AfterCommit(func(ctx context.Context) error {
				var errs []error
				for _, chID := range widgetIDs {
					if err := notify(ctx, chID, contactID, ev); err != nil {
						errs = append(errs, err)
					}
				}
				return errors.Join(errs...)
			})
		}
	}

	slog.InfoContext(ctx, "merged conversations",
		"tenant_id", target.TenantID, "target_id", target.ID, "source_id", source.ID, "moved_items", movedItems)
	return target, nil
}

// mergeRounds 将源会话的服务轮次并入目标会话并重编号，避免删除源会话时丢失历史 FRT 与 CSAT 报表数据，同时保证最多一条未关闭轮次。
func mergeRounds(ctx context.Context, tx database.Tx, tenantID, targetID, sourceID string, at time.Time) error {
	var targetOpen, sourceOpen Round
	errT := tx.NewSelect().Model(&targetOpen).
		Where("tenant_id = ? AND conversation_id = ? AND closed_at IS NULL", tenantID, targetID).
		OrderExpr("round DESC").Limit(1).Scan(ctx)
	if errT != nil && !errors.Is(errT, sql.ErrNoRows) {
		return errors.Wrap(errT, "load target open round for merge")
	}
	errS := tx.NewSelect().Model(&sourceOpen).
		Where("tenant_id = ? AND conversation_id = ? AND closed_at IS NULL", tenantID, sourceID).
		OrderExpr("round DESC").Limit(1).Scan(ctx)
	if errS != nil && !errors.Is(errS, sql.ErrNoRows) {
		return errors.Wrap(errS, "load source open round for merge")
	}
	if errT == nil && errS == nil {
		if sourceOpen.OpenedAt.Before(targetOpen.OpenedAt) {
			targetOpen.OpenedAt = sourceOpen.OpenedAt
		}
		if sourceOpen.FirstReplyAt != nil && (targetOpen.FirstReplyAt == nil || sourceOpen.FirstReplyAt.Before(*targetOpen.FirstReplyAt)) {
			targetOpen.FirstReplyAt = sourceOpen.FirstReplyAt
			targetOpen.FirstReplyByType = sourceOpen.FirstReplyByType
			targetOpen.FirstReplyByID = sourceOpen.FirstReplyByID
		}
		targetOpen.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(&targetOpen).
			Column("opened_at", "first_reply_at", "first_reply_by_type", "first_reply_by_id", "updated_at").
			WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "merge open conversation round")
		}
		if _, err := tx.NewDelete().Model((*Round)(nil)).Where("id = ?", sourceOpen.ID).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete duplicate open source round")
		}
	}
	var maxRound int
	if err := tx.NewRaw("SELECT coalesce(max(round), 0) FROM conversation_rounds WHERE conversation_id = ?", targetID).Scan(ctx, &maxRound); err != nil {
		return errors.Wrap(err, "query max target round")
	}
	_, err := tx.NewRaw(
		`UPDATE conversation_rounds SET conversation_id = ?, round = round + ?, updated_at = ? WHERE tenant_id = ? AND conversation_id = ?`,
		targetID, maxRound, at, tenantID, sourceID,
	).Exec(ctx)
	return errors.Wrap(err, "migrate merged conversation rounds")
}
