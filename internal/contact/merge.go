package contact

import (
	"context"
	"database/sql"
	"log/slog"
	"maps"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// BeforeMergeHook 在两个联系人合并迁移会话之前于同一事务中执行，供会话模块先将来源的活跃服务型会话归并到目标活跃会话，
// 保证后续迁移其余历史会话时严格满足“同一联系人最多一条活跃服务型会话”的唯一约束。
type BeforeMergeHook func(ctx context.Context, tx database.Tx, targetContactID, sourceContactID string) error

// MergeHooks 是手动合并联系人时接入会话模块的事务钩子：
//   - BeforeMigrate 在迁移 conversations.contact_id 之前归并双活服务型会话；
//   - AfterMigrate 在完成联系人合并后向受影响的成员和客户端推送刷新。
type MergeHooks struct {
	BeforeMigrate  func(ctx context.Context, tx database.Tx, sub identity.Subject, targetContactID, sourceContactID string) error
	AfterMigrate   func(ctx context.Context, tx database.Tx, sub identity.Subject, targetContactID string, affectedConvIDs []string) error
	OnNotesUpdated NotesUpdatedHook
}

// MergeBody 是手动合并联系人的请求体：把 source_id 指定的联系人合并进路径指定的目标联系人。
type MergeBody struct {
	SourceID string `json:"source_id" format:"uuid" doc:"要被合并进来的来源联系人 ID（合并后删除）"`
}

// MergeInput 是手动合并联系人的 HTTP 请求。
type MergeInput struct {
	ID   string `path:"id" format:"uuid"`
	Body MergeBody
}

// Merge 由成员手动把同工作区的来源联系人合并到目标联系人名下，合并资料、标签、渠道身份和全部会话。
type Merge struct {
	db    *bun.DB
	hooks MergeHooks
}

// NewMerge 返回手动合并联系人的动作；hooks 可选，用于在合并同一事务中归并双活会话并通知成员刷新。
func NewMerge(db *bun.DB, hooks ...MergeHooks) *Merge {
	var h MergeHooks
	if len(hooks) > 0 {
		h = hooks[0]
	}
	return &Merge{db: db, hooks: h}
}

// Handle 由 sub 把工作区中的来源联系人 body.SourceID 合并到目标联系人 targetID：
//   - 需要 contacts:update 权限（系统主体始终允许）；
//   - 若两个联系人绑定了不同的非空 external_user_id，返回 ErrMergeConflict 防止误合并不同正式账号；
//   - 目标保留，来源的空白补全资料、备注、标签、渠道身份与全部会话全部迁到目标名下后删除来源；
//   - 若目标原本是线索而来源是已登录用户，目标升级为已登录用户并继承其 external_user_id。
func (a *Merge) Handle(ctx context.Context, sub identity.Subject, targetID string, body MergeBody) (*ContactDetailDTO, error) {
	if sub.Type != identity.System && !sub.Can(string(auth.ContactsUpdate)) {
		return nil, ErrForbidden.New()
	}
	if targetID == "" || body.SourceID == "" || targetID == body.SourceID {
		return nil, ErrMergeSelf.New()
	}
	var merged *Contact
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		// 按确定顺序锁住两行，防止并发合并死锁
		first, second := targetID, body.SourceID
		if first > second {
			first, second = second, first
		}
		var locked []Contact
		if err := tx.NewSelect().Model(&locked).
			Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In([]string{first, second})).
			OrderExpr("id ASC").For("UPDATE").Scan(ctx); err != nil {
			return errors.Wrap(err, "lock contacts for manual merge")
		}
		var target, source *Contact
		for i := range locked {
			switch locked[i].ID {
			case targetID:
				target = &locked[i]
			case body.SourceID:
				source = &locked[i]
			}
		}
		if target == nil || source == nil {
			return ErrNotFound.New()
		}
		if target.ExternalUserID != "" && source.ExternalUserID != "" && target.ExternalUserID != source.ExternalUserID {
			return ErrMergeConflict.New()
		}

		// 如果来源已绑定 external_user_id 而目标未绑定，先清空来源上的 external_user_id 再赋给目标，避开唯一索引冲突
		inheritedExternalID := ""
		if target.ExternalUserID == "" && source.ExternalUserID != "" {
			inheritedExternalID = source.ExternalUserID
			if _, err := tx.NewUpdate().Model((*Contact)(nil)).
				Set("external_user_id = ''").
				Where("id = ? AND tenant_id = ?", source.ID, sub.Tenant).Exec(ctx); err != nil {
				return errors.Wrap(err, "release source external user id")
			}
		}

		at := now()
		var cols []string
		if inheritedExternalID != "" {
			target.ExternalUserID = inheritedExternalID
			cols = append(cols, "external_user_id")
		}
		if target.Kind != User && (source.Kind == User || inheritedExternalID != "") {
			target.Kind = User
			cols = append(cols, "kind")
		}
		if target.Name == "" && source.Name != "" {
			target.Name, cols = source.Name, append(cols, "name")
		}
		if target.Company == "" && source.Company != "" {
			target.Company, cols = source.Company, append(cols, "company")
		}
		if target.Email == "" && source.Email != "" {
			target.Email, cols = source.Email, append(cols, "email")
		}
		if target.Location == "" && source.Location != "" {
			target.Location, cols = source.Location, append(cols, "location")
		}
		if target.TimeZone == "" && source.TimeZone != "" {
			target.TimeZone, cols = source.TimeZone, append(cols, "time_zone")
		}
		if target.Referrer == "" && source.Referrer != "" {
			target.Referrer, cols = source.Referrer, append(cols, "referrer")
		}
		if source.Notes != "" {
			if target.Notes == "" {
				target.Notes = source.Notes
			} else if target.Notes != source.Notes {
				target.Notes = target.Notes + "\n\n" + source.Notes
			}
			cols = append(cols, "notes")
		}
		if !target.LanguageManual {
			if source.LanguageManual && source.Language != "" {
				target.Language, target.LanguageManual = source.Language, true
				cols = append(cols, "language", "language_manual")
			} else if target.Language == "" && source.Language != "" {
				target.Language = source.Language
				cols = append(cols, "language")
			}
		}
		if source.LastSeenAt != nil && (target.LastSeenAt == nil || source.LastSeenAt.After(*target.LastSeenAt)) {
			t := *source.LastSeenAt
			target.LastSeenAt, cols = &t, append(cols, "last_seen_at")
		}
		if len(source.Attributes) > 0 {
			mergedAttrs := map[string]any{}
			maps.Copy(mergedAttrs, source.Attributes)
			maps.Copy(mergedAttrs, target.Attributes)
			target.Attributes, cols = mergedAttrs, append(cols, "attributes")
		}
		if len(cols) > 0 {
			target.UpdatedAt = at
			if _, err := tx.NewUpdate().Model(target).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update merged contact")
			}
		}

		// 先归并双方可能同时存在的活跃服务型会话，保证后续迁移全部会话时不违反唯一活跃约束
		if a.hooks.BeforeMigrate != nil {
			if err := a.hooks.BeforeMigrate(ctx, tx, sub, target.ID, source.ID); err != nil {
				return err
			}
		}

		// 迁移来源联系人的全部会话、参与者、时间线发起者、标签与渠道身份
		var convIDs []string
		if err := tx.NewRaw(
			`UPDATE conversations SET contact_id = ?, updated_at = ? WHERE tenant_id = ? AND contact_id = ? RETURNING id`,
			target.ID, at, sub.Tenant, source.ID,
		).Scan(ctx, &convIDs); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return errors.Wrap(err, "migrate merged contact conversations")
		}
		if target.Language != "" && len(convIDs) > 0 {
			if _, err := tx.NewUpdate().Table("conversations").
				Set("language = ?", target.Language).
				Where("tenant_id = ? AND id IN (?) AND language = ''", sub.Tenant, bun.In(convIDs)).Exec(ctx); err != nil {
				return errors.Wrap(err, "sync merged conversation language")
			}
		}
		if _, err := tx.NewRaw(
			`INSERT INTO conversation_participants (id, created_at, updated_at, conversation_id, participant_type, participant_id)
			 SELECT gen_random_uuid(), cp.created_at, cp.updated_at, cp.conversation_id, cp.participant_type, ?
			 FROM conversation_participants cp
			 JOIN conversations c ON c.id = cp.conversation_id
			 WHERE c.tenant_id = ? AND cp.participant_type = ? AND cp.participant_id = ?
			 ON CONFLICT DO NOTHING`,
			target.ID, sub.Tenant, string(Type), source.ID,
		).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert merged contact participants")
		}
		if _, err := tx.NewRaw(
			`DELETE FROM conversation_participants cp
			 USING conversations c
			 WHERE cp.conversation_id = c.id AND c.tenant_id = ? AND cp.participant_type = ? AND cp.participant_id = ?`,
			sub.Tenant, string(Type), source.ID,
		).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete source contact participants")
		}
		if _, err := tx.NewUpdate().Table("timeline_items").
			Set("actor_id = ?", target.ID).
			Where("tenant_id = ? AND actor_type = ? AND actor_id = ?", sub.Tenant, string(Type), source.ID).Exec(ctx); err != nil {
			return errors.Wrap(err, "migrate merged contact timeline actors")
		}
		if _, err := tx.NewRaw(
			`INSERT INTO contact_tags (tenant_id, contact_id, tag_id, created_at)
			 SELECT tenant_id, ?, tag_id, created_at
			 FROM contact_tags
			 WHERE tenant_id = ? AND contact_id = ?
			 ON CONFLICT DO NOTHING`,
			target.ID, sub.Tenant, source.ID,
		).Exec(ctx); err != nil {
			return errors.Wrap(err, "migrate merged contact tags")
		}
		if _, err := tx.NewRaw(
			`DELETE FROM contact_tags WHERE tenant_id = ? AND contact_id = ?`,
			sub.Tenant, source.ID,
		).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete source contact tags")
		}
		if _, err := tx.NewRaw(
			`INSERT INTO ticket_targets (id, tenant_id, ticket_id, contact_id, source_conversation_id, created_by, created_at, updated_at)
			 SELECT id, tenant_id, ticket_id, ?, source_conversation_id, created_by, created_at, ?
			 FROM ticket_targets
			 WHERE tenant_id = ? AND contact_id = ?
			 ON CONFLICT (tenant_id, ticket_id, contact_id) WHERE contact_id IS NOT NULL DO NOTHING`,
			target.ID, at, sub.Tenant, source.ID,
		).Exec(ctx); err != nil {
			return errors.Wrap(err, "migrate merged ticket targets")
		}
		if _, err := tx.NewRaw(
			`DELETE FROM ticket_targets WHERE tenant_id = ? AND contact_id = ?`,
			sub.Tenant, source.ID,
		).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete source ticket targets")
		}
		if _, err := tx.NewUpdate().Model((*Identity)(nil)).
			Set("contact_id = ?", target.ID).
			Where("tenant_id = ? AND contact_id = ?", sub.Tenant, source.ID).Exec(ctx); err != nil {
			return errors.Wrap(err, "migrate merged contact identities")
		}
		if _, err := apitoken.NewStore(tx, apitoken.Config{Lifetime: time.Minute, IdleTimeout: time.Minute}).
			RevokeSubject(ctx, identity.Ref{Type: Type, ID: source.ID}); err != nil {
			return errors.Wrap(err, "revoke source contact tokens")
		}
		if _, err := tx.NewDelete().Model((*Contact)(nil)).Where("id = ? AND tenant_id = ?", source.ID, sub.Tenant).Exec(ctx); err != nil {
			return errors.Wrap(err, "delete source contact")
		}
		if a.hooks.AfterMigrate != nil {
			if err := a.hooks.AfterMigrate(ctx, tx, sub, target.ID, convIDs); err != nil {
				return err
			}
		}
		slog.InfoContext(ctx, "merged contacts",
			"tenant_id", sub.Tenant, "target_id", target.ID, "source_id", source.ID,
			"moved_conversations", len(convIDs))
		merged = target
		return nil
	})
	if err != nil {
		return nil, err
	}
	return loadDetail(ctx, a.db, sub, merged)
}

// HTTP 处理 POST /t/{slug}/api/contacts/{id}/merge。
func (a *Merge) HTTP(ctx context.Context, in *MergeInput) (*ContactDetailOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ContactDetailOutput{Body: *dto}, nil
}

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
		`INSERT INTO conversation_participants (id, created_at, updated_at, conversation_id, participant_type, participant_id)
		 SELECT gen_random_uuid(), cp.created_at, cp.updated_at, cp.conversation_id, cp.participant_type, ?
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

	// 4. 合并联系人标签与工单关联（保留线索已打的标签与关联的工单，冲突时去重）
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
	if _, err := tx.NewRaw(
		`INSERT INTO ticket_targets (id, tenant_id, ticket_id, contact_id, source_conversation_id, created_by, created_at, updated_at)
		 SELECT id, tenant_id, ticket_id, ?, source_conversation_id, created_by, created_at, ?
		 FROM ticket_targets
		 WHERE tenant_id = ? AND contact_id = ?
		 ON CONFLICT (tenant_id, ticket_id, contact_id) WHERE contact_id IS NOT NULL DO NOTHING`,
		user.ID, at, tenant, lead.ID,
	).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "migrate lead ticket targets")
	}
	if _, err := tx.NewRaw(
		`DELETE FROM ticket_targets WHERE tenant_id = ? AND contact_id = ?`,
		tenant, lead.ID,
	).Exec(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "delete lead ticket targets")
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
