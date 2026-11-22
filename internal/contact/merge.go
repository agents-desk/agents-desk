package contact

import (
	"context"
	"database/sql"
	"maps"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/identity"
)

// MergeLead 在事务 tx 中把工作区 tenant 里的线索 leadID 合并到用户 userID：
//   - 按 ID 升序加行锁（FOR UPDATE），避免并发登录死锁；leadID 已不存在、不是线索或等于 userID 时直接返回当前用户；
//   - 用线索的资料补齐用户上为空的字段（名字、邮箱、所在地、时区、来源页面、语言、自定义属性），备注两端非空时拼接保留；
//   - 把线索的全部会话、会话参与者和时间线发起者迁到用户名下；
//   - 消费掉指定的访客渠道身份（consumeChannelID + consumeExternalID，防止共用浏览器退出登录后仍以旧访客编号冒用已登录用户），
//     其余渠道身份迁到用户名下；
//   - 吊销线索的联系人令牌并删除原线索记录。
//
// 返回合并后的用户，以及受影响（contact_id 发生变化）的会话编号列表，供调用方在事务提交后推送刷新。
func MergeLead(ctx context.Context, tx bun.IDB, tenant, leadID, userID, consumeChannelID, consumeExternalID string) (*Contact, []string, error) {
	if leadID == "" || userID == "" || leadID == userID {
		u, err := Find(ctx, tx, tenant, userID)
		return u, nil, err
	}
	// 按确定顺序锁住两行，防止两个并发请求互相等待
	first, second := leadID, userID
	if first > second {
		first, second = second, first
	}
	var locked []Contact
	if err := tx.NewSelect().Model(&locked).
		Where("tenant_id = ? AND id IN (?)", tenant, bun.In([]string{first, second})).
		OrderExpr("id ASC").For("UPDATE").Scan(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "lock contacts for merge")
	}
	var lead, user *Contact
	for i := range locked {
		switch locked[i].ID {
		case leadID:
			lead = &locked[i]
		case userID:
			user = &locked[i]
		}
	}
	if user == nil {
		return nil, nil, ErrNotFound.New()
	}
	// 线索已被并发请求合并掉，或者目标/来源类型不符：幂等跳过
	if lead == nil || lead.Kind != Lead || user.Kind != User {
		return user, nil, nil
	}

	at := now()
	// 资料回填：产品传来的用户资料优先，用户为空的项从线索补齐
	var cols []string
	if user.Name == "" && lead.Name != "" {
		user.Name, cols = lead.Name, append(cols, "name")
	}
	if user.Email == "" && lead.Email != "" {
		user.Email, cols = lead.Email, append(cols, "email")
	}
	if user.Location == "" && lead.Location != "" {
		user.Location, cols = lead.Location, append(cols, "location")
	}
	if user.TimeZone == "" && lead.TimeZone != "" {
		user.TimeZone, cols = lead.TimeZone, append(cols, "time_zone")
	}
	if user.Referrer == "" && lead.Referrer != "" {
		user.Referrer, cols = lead.Referrer, append(cols, "referrer")
	}
	if lead.Notes != "" {
		if user.Notes == "" {
			user.Notes = lead.Notes
		} else if user.Notes != lead.Notes {
			user.Notes = user.Notes + "\n\n" + lead.Notes
		}
		cols = append(cols, "notes")
	}
	if !user.LanguageManual {
		if lead.LanguageManual && lead.Language != "" {
			user.Language, user.LanguageManual = lead.Language, true
			cols = append(cols, "language", "language_manual")
		} else if user.Language == "" && lead.Language != "" {
			user.Language = lead.Language
			cols = append(cols, "language")
		}
	}
	if lead.LastSeenAt != nil && (user.LastSeenAt == nil || lead.LastSeenAt.After(*user.LastSeenAt)) {
		t := *lead.LastSeenAt
		user.LastSeenAt, cols = &t, append(cols, "last_seen_at")
	}
	if len(lead.Attributes) > 0 {
		merged := map[string]any{}
		maps.Copy(merged, lead.Attributes)
		maps.Copy(merged, user.Attributes)
		user.Attributes, cols = merged, append(cols, "attributes")
	}
	if len(cols) > 0 {
		user.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(user).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "update merged user")
		}
	}

	// 1. 迁移线索的全部会话到用户名下，记录受影响的会话 ID
	var convIDs []string
	if err := tx.NewRaw(
		`UPDATE conversations SET contact_id = ?, updated_at = ? WHERE tenant_id = ? AND contact_id = ? RETURNING id`,
		user.ID, at, tenant, lead.ID,
	).Scan(ctx, &convIDs); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, errors.Wrap(err, "migrate conversations")
	}
	if user.Language != "" && len(convIDs) > 0 {
		if _, err := tx.NewUpdate().Table("conversations").
			Set("language = ?", user.Language).
			Where("tenant_id = ? AND id IN (?) AND language = ''", tenant, bun.In(convIDs)).Exec(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "sync migrated conversation language")
		}
	}

	// 2. 迁移会话参与者：避免主键冲突，先插入再删除旧记录
	if _, err := tx.NewRaw(
		`INSERT INTO conversation_participants (conversation_id, participant_type, participant_id, created_at)
		 SELECT cp.conversation_id, cp.participant_type, ?, cp.created_at
		 FROM conversation_participants cp
		 JOIN conversations c ON c.id = cp.conversation_id
		 WHERE c.tenant_id = ? AND cp.participant_type = ? AND cp.participant_id = ?
		 ON CONFLICT DO NOTHING`,
		user.ID, tenant, string(Type), lead.ID,
	).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "insert merged participants")
	}
	if _, err := tx.NewRaw(
		`DELETE FROM conversation_participants cp
		 USING conversations c
		 WHERE cp.conversation_id = c.id AND c.tenant_id = ? AND cp.participant_type = ? AND cp.participant_id = ?`,
		tenant, string(Type), lead.ID,
	).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "delete lead participants")
	}

	// 3. 迁移时间线中联系人的发起者编号
	if _, err := tx.NewUpdate().Table("timeline_items").
		Set("actor_id = ?", user.ID).
		Where("tenant_id = ? AND actor_type = ? AND actor_id = ?", tenant, string(Type), lead.ID).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "migrate timeline actors")
	}

	// 4. 合并联系人标签（保留线索已打的标签，冲突时去重）
	if _, err := tx.NewRaw(
		`INSERT INTO contact_tags (tenant_id, contact_id, tag_id, created_at)
		 SELECT tenant_id, ?, tag_id, created_at
		 FROM contact_tags
		 WHERE tenant_id = ? AND contact_id = ?
		 ON CONFLICT DO NOTHING`,
		user.ID, tenant, lead.ID,
	).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "migrate contact tags")
	}
	if _, err := tx.NewRaw(
		`DELETE FROM contact_tags WHERE tenant_id = ? AND contact_id = ?`,
		tenant, lead.ID,
	).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "delete lead contact tags")
	}

	// 5. 渠道身份：删除已消费的访客编号，其余渠道身份迁到用户名下
	if consumeChannelID != "" && consumeExternalID != "" {
		if _, err := tx.NewDelete().Model((*Identity)(nil)).
			Where("tenant_id = ? AND channel_id = ? AND external_id = ? AND contact_id = ?",
				tenant, consumeChannelID, consumeExternalID, lead.ID).Exec(ctx); err != nil {
			return nil, nil, errors.Wrap(err, "delete consumed visitor identity")
		}
	}
	if _, err := tx.NewUpdate().Model((*Identity)(nil)).
		Set("contact_id = ?", user.ID).
		Where("tenant_id = ? AND contact_id = ?", tenant, lead.ID).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "migrate contact identities")
	}

	// 6. 吊销原线索的联系人令牌，并删除原线索记录（Config 只在 Login 签发时使用，这里仅吊销）
	if _, err := apitoken.NewStore(tx, apitoken.Config{Lifetime: time.Minute, IdleTimeout: time.Minute}).
		RevokeSubject(ctx, identity.Ref{Type: Type, ID: lead.ID}); err != nil {
		return nil, nil, errors.Wrap(err, "revoke lead tokens")
	}
	if _, err := tx.NewDelete().Model((*Contact)(nil)).Where("id = ? AND tenant_id = ?", lead.ID, tenant).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "delete merged lead")
	}
	return user, convIDs, nil
}
