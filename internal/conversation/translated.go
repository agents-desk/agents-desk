package conversation

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
	"agents-desk/internal/extgroup"
	"agents-desk/internal/lang"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
)

// Undetermined 是判断不出语言的消息的语言（BCP 47 的 und）：太短、或者不属于哪一种语言，例如 ok、表情、订单号。
// 这样的消息不翻译，也不参与判断客户的语言。
const Undetermined = "und"

// customerLanguageWindow 是判断客户的语言时看的消息数：客户最近这么多条判断得出语言的消息中，出现最多的语言。
const customerLanguageWindow = 3

// Translated 是收到的一条消息识别和翻译的结果。
type Translated struct {
	Language     string            // 识别出的语言和书写方式（BCP 47），例如 hi-Latn；判断不出时是 Undetermined
	Translations map[string]string // 按成员的语言（zh-CN、en……）给出的译文；与原文同语言的没有
}

var errLockOrderRetry = errors.New("conversation: lock order target changed")

// sameOptString 比较两个可选字符串指针的值是否一致。
func sameOptString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// lockPeerInOrder 在事务内先于会话锁定预读到的对端实体（联系人或外部群），用于遵守 contacts -> external_groups -> conversations 全局锁序。
// 若预读实体因并发合并已被删除，返回 errLockOrderRetry 通知调用方刷新重读。
func lockPeerInOrder(ctx context.Context, tx bun.IDB, tenant string, contactID, groupID *string) error {
	if contactID != nil && *contactID != "" {
		var dummy string
		err := tx.NewSelect().Table("contacts").Column("id").
			Where("id = ? AND tenant_id = ?", *contactID, tenant).For("UPDATE").Scan(ctx, &dummy)
		if errors.Is(err, sql.ErrNoRows) {
			return errLockOrderRetry
		}
		if err != nil {
			return errors.Wrap(err, "lock contact before conversation")
		}
	}
	if groupID != nil && *groupID != "" {
		var dummy string
		err := tx.NewSelect().Table("external_groups").Column("id").
			Where("id = ? AND tenant_id = ?", *groupID, tenant).For("UPDATE").Scan(ctx, &dummy)
		if errors.Is(err, sql.ErrNoRows) {
			return errLockOrderRetry
		}
		if err != nil {
			return errors.Wrap(err, "lock external group before conversation")
		}
	}
	return nil
}

// SaveTranslation 把工作区 tenant 中消息 itemID 的识别和翻译结果 t 写进它的派生内容：derived.language 是这条消息的语言，
// 已经识别过的保持不变；derived.translations 中已有的译文保留，同一种语言的换成新的。
// 然后按 CustomerLanguage 更新会话和联系人的语言（成员手动设置了联系人的语言时不改）。
// 提交后推送给受影响的成员：消息首次识别出语言或客户的语言变化时推送给能看到会话的全部成员；
// 只是补译某些语言时只推送给使用这些语言的成员，避免影响其他语言的成员。事件带上消息的序号（ItemSeq），
// 方便页面把历史消息的补译刷新出来。消息不存在时什么也不做。
func SaveTranslation(ctx context.Context, db *bun.DB, down Downstream, tenant, itemID string, t Translated) error {
	for attempt := range 3 {
		var meta struct {
			ConversationID string    `bun:"conversation_id"`
			ActorType      ActorType `bun:"actor_type"`
		}
		err := db.NewSelect().Table("timeline_items").Column("conversation_id", "actor_type").
			Where("id = ? AND tenant_id = ?", itemID, tenant).Scan(ctx, &meta)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "load conversation id for translation")
		}

		var preContactID, preGroupID *string
		if meta.ActorType.IsExternal() {
			var peer struct {
				ContactID       *string `bun:"contact_id"`
				ExternalGroupID *string `bun:"external_group_id"`
			}
			err := db.NewSelect().Table("conversations").Column("contact_id", "external_group_id").
				Where("id = ? AND tenant_id = ?", meta.ConversationID, tenant).Scan(ctx, &peer)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return errors.Wrap(err, "load conversation peers for translation")
			}
			preContactID, preGroupID = peer.ContactID, peer.ExternalGroupID
		}

		err = database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
			// 按 contacts/external_groups -> conversations -> timeline_items 固定锁序加锁，避免 AB-BA 死锁与竞态覆盖。
			if meta.ActorType.IsExternal() {
				if err := lockPeerInOrder(ctx, tx, tenant, preContactID, preGroupID); err != nil {
					return err
				}
			}
			c, err := find(ctx, tx, tenant, meta.ConversationID, true)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					if attempt < 2 {
						return errLockOrderRetry
					}
					return nil
				}
				return err
			}
			if meta.ActorType.IsExternal() && (!sameOptString(c.ContactID, preContactID) || !sameOptString(c.ExternalGroupID, preGroupID)) {
				return errLockOrderRetry
			}
			var it Item
			err = tx.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", itemID, tenant).For("UPDATE").Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return errors.Wrap(err, "load timeline item")
			}
			if it.Derived == nil {
				it.Derived = map[string]any{}
			}

			// 消息的语言和译文
			detectedNow := false
			if source, _ := it.Derived["language"].(string); source == "" && t.Language != "" {
				it.Derived["language"] = t.Language
				detectedNow = true
			}
			translations, _ := it.Derived["translations"].(map[string]any)
			if translations == nil {
				translations = map[string]any{}
			}
			verified, _ := it.Derived["verified_language"].(string)
			added := make([]string, 0, len(t.Translations))
			for target, text := range t.Translations {
				// 保留发送者发送前已人工校对过的目标语言译文，避免后台自动翻译覆盖
				if verified != "" && lang.Same(verified, target) && hasTranslationFor(translations, target) {
					continue
				}
				if prev, _ := translations[target].(string); prev != text {
					added = append(added, target)
				}
				translations[target] = text
			}
			it.Derived["translations"] = translations
			if _, err := tx.NewUpdate().Model(&it).Column("derived").WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "save translation")
			}
			if len(added) > 0 && it.Searchable() {
				if err := SearchIndex.Sync(ctx, tx, it.ID); err != nil {
					return err
				}
			}

			// 对端语言：仅外部入站消息（联系人或外部群发言人）参与更新，成员回复与内部备注不改变对端语言
			langChanged := false
			if it.ActorType.IsExternal() {
				langChanged, err = updateCustomerLanguage(ctx, tx, c, false)
				if err != nil {
					return err
				}
			}

			if down.Hub == nil || (!detectedNow && !langChanged && len(added) == 0) {
				return nil
			}
			audience, err := audienceOf(ctx, tx, down.Roles, c, nil)
			if err != nil {
				return err
			}
			if !detectedNow && !langChanged {
				audience, err = membersWithLanguages(ctx, tx, audience, added)
				if err != nil {
					return err
				}
			}
			if len(audience) == 0 {
				return nil
			}
			hub, ev := down.Hub, push.Event{Type: pushType(c.Kind), ID: c.ID, Seq: c.LastSeq, ItemSeq: it.Seq}
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Wrap(push.Send(ctx, hub, tenant, audience, ev), "push translation")
			})
			return nil
		})
		if errors.Is(err, errLockOrderRetry) && attempt < 2 {
			continue
		}
		return err
	}
	return nil
}

// membersWithLanguages 从 memberIDs 中筛出语言与 targets 中任意一种相同的成员，用于补译时只推送给看得到这些译文的成员。
func membersWithLanguages(ctx context.Context, db bun.IDB, memberIDs, targets []string) ([]string, error) {
	if len(memberIDs) == 0 || len(targets) == 0 {
		return nil, nil
	}
	var rows []struct {
		ID       string `bun:"id"`
		Language string `bun:"language"`
	}
	if err := db.NewSelect().Table("members").Column("id", "language").Where("id IN (?)", bun.In(memberIDs)).Scan(ctx, &rows); err != nil {
		return nil, errors.Wrap(err, "load member languages for push")
	}
	var out []string
	for _, r := range rows {
		for _, target := range targets {
			if lang.Same(r.Language, target) {
				out = append(out, r.ID)
				break
			}
		}
	}
	return out, nil
}

// CustomerLanguage 返回会话 c 的对端语言：对端（联系人查该联系人全部会话，外部群查该群全部会话，否则查当前会话）
// 最近 customerLanguageWindow 条判断得出语言的消息中出现最多的语言，一样多时取最近的。
func CustomerLanguage(ctx context.Context, db bun.IDB, c *Conversation) (string, error) {
	q := db.NewSelect().TableExpr("timeline_items AS t").ColumnExpr("t.derived->>'language'").
		Where("t.tenant_id = ? AND t.kind = ? AND t.actor_type IN (?)", c.TenantID, Message, bun.In([]ActorType{ContactActor, ExternalUserActor})).
		Where("COALESCE(t.derived->>'language', '') NOT IN ('', ?)", Undetermined).
		OrderExpr("t.created_at DESC, t.seq DESC").Limit(customerLanguageWindow)
	switch {
	case c.ContactID != nil:
		q = q.Where("t.conversation_id IN (SELECT id FROM conversations WHERE tenant_id = ? AND contact_id = ?)", c.TenantID, *c.ContactID)
	case c.ExternalGroupID != nil:
		q = q.Where("t.conversation_id IN (SELECT id FROM conversations WHERE tenant_id = ? AND external_group_id = ?)", c.TenantID, *c.ExternalGroupID)
	default:
		q = q.Where("t.conversation_id = ?", c.ID)
	}
	var recent []string
	if err := q.Scan(ctx, &recent); err != nil {
		return "", errors.Wrap(err, "load recent message languages")
	}
	best, count := "", map[string]int{}
	for _, l := range recent {
		count[l]++
		if best == "" || count[l] > count[best] {
			best = l
		}
	}
	return best, nil
}

// updateCustomerLanguage 在事务 tx 中按 CustomerLanguage 更新已锁住的会话 c 及其对端实体（联系人或外部群）的语言。
func updateCustomerLanguage(ctx context.Context, tx bun.IDB, c *Conversation, clearIfUnknown bool) (bool, error) {
	var ct *contact.Contact
	if c.ContactID != nil {
		ct = new(contact.Contact)
		if err := tx.NewSelect().Model(ct).Where("id = ? AND tenant_id = ?", *c.ContactID, c.TenantID).For("UPDATE").Scan(ctx); err != nil {
			return false, errors.Wrap(err, "load contact")
		}
		if ct.LanguageManual {
			if c.Language != ct.Language {
				c.Language = ct.Language
				if _, err := tx.NewUpdate().Model(c).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
					return false, errors.Wrap(err, "sync conversation language")
				}
			}
			return false, nil
		}
	}
	var grp *extgroup.Group
	if c.ExternalGroupID != nil {
		grp = new(extgroup.Group)
		if err := tx.NewSelect().Model(grp).Where("id = ? AND tenant_id = ?", *c.ExternalGroupID, c.TenantID).For("UPDATE").Scan(ctx); err != nil {
			return false, errors.Wrap(err, "load external group")
		}
		if grp.LanguageManual {
			if c.Language != grp.Language {
				c.Language = grp.Language
				if _, err := tx.NewUpdate().Model(c).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
					return false, errors.Wrap(err, "sync group conversation language")
				}
			}
			return false, nil
		}
	}
	language, err := CustomerLanguage(ctx, tx, c)
	if err != nil {
		return false, err
	}
	if language == "" && !clearIfUnknown {
		return false, nil
	}
	changed := false
	if c.Language != language {
		c.Language = language
		if _, err := tx.NewUpdate().Model(c).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
			return false, errors.Wrap(err, "update conversation language")
		}
		changed = true
	}
	if ct != nil && ct.Language != language {
		ct.Language = language
		if _, err := tx.NewUpdate().Model(ct).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
			return false, errors.Wrap(err, "update contact language")
		}
		changed = true
	}
	if grp != nil && grp.Language != language {
		grp.Language = language
		if _, err := tx.NewUpdate().Model(grp).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
			return false, errors.Wrap(err, "update external group language")
		}
		changed = true
	}
	return changed, nil
}

// MissingTranslations 返回 targets 中派生内容 derived 还缺译文的语言：还没有识别过语言时是全部 targets；判断不出语言的消息不翻译；
// 识别过时去掉已经有译文的、和原文同一种语言的。
func MissingTranslations(derived map[string]any, targets []string) []string {
	source, _ := derived["language"].(string)
	if source == "" {
		return targets
	}
	if source == Undetermined {
		return nil
	}
	translations, _ := derived["translations"].(map[string]any)
	var missing []string
	for _, t := range targets {
		if !hasTranslationFor(translations, t) && !lang.Same(source, t) {
			missing = append(missing, t)
		}
	}
	return missing
}

// hasTranslationFor 报告派生译文表 translations 中是否已有与 target 相同语言（含 BCP 47 等价写法）的非空译文。
func hasTranslationFor(translations map[string]any, target string) bool {
	if len(translations) == 0 {
		return false
	}
	if v, ok := translations[target].(string); ok && strings.TrimSpace(v) != "" {
		return true
	}
	for k, raw := range translations {
		if v, ok := raw.(string); ok && strings.TrimSpace(v) != "" && lang.Same(k, target) {
			return true
		}
	}
	return false
}

// applyVerifiedTranslation 把成员在发送前校对或微调过的译文 tr 写入时间线项 it 的派生内容：
// 消息正文仍保持原文，译文写入 derived.translations[language] 并记下 derived.verified_language 与作者语言 derived.language，
// 使该语言读者立即可见精准译文且不被后台重译，其余语言的读者仍基于原文自动翻译。
func applyVerifiedTranslation(ctx context.Context, db bun.IDB, sub identity.Subject, it *Item, tr *Translation) error {
	if tr == nil || strings.TrimSpace(it.Content.Text) == "" || strings.TrimSpace(tr.Text) == "" {
		return nil
	}
	target, ok := lang.Canonical(tr.Language)
	if !ok {
		return ErrInvalidLanguage.New()
	}
	from, err := MemberLanguage(ctx, db, sub)
	if err != nil {
		return err
	}
	if from != "" && lang.Same(from, target) {
		return nil
	}
	if it.Derived == nil {
		it.Derived = map[string]any{}
	}
	if from != "" {
		it.Derived["language"] = from
	}
	translations, _ := it.Derived["translations"].(map[string]any)
	if translations == nil {
		translations = map[string]any{}
	}
	translations[target] = strings.TrimSpace(tr.Text)
	it.Derived["translations"] = translations
	it.Derived["verified_language"] = target
	return nil
}

// SourceText 返回用于自动翻译与跨语言阅读的源文本：翻译后发出的回复取成员写的原文，其余取消息正文。
func (it *Item) SourceText() string {
	if it.Content.Original != "" {
		return it.Content.Original
	}
	return it.Content.Text
}

// Translatable 报告时间线的一项是不是需要识别与跨语言翻译的人类文本：
// 联系人或成员发出的有文字消息与内部备注均可按阅读者语言互译，系统事件、系统提示卡片与纯附件不翻译。
func (it *Item) Translatable() bool {
	if it.SourceText() == "" || it.Content.Prompt != nil {
		return false
	}
	if it.Kind != Message && it.Kind != Note {
		return false
	}
	return it.ActorType.IsExternal() || it.ActorType == MemberActor
}

// untranslated 报告时间线的一项对成员 viewerID（语言为 language）来说是不是还缺译文：
// 成员自己发出的内容不需要译文；其余可译项还没有这种语言的译文且不是这种语言时返回 true。
func untranslated(it *Item, viewerID, language string) bool {
	if language == "" || !it.Translatable() {
		return false
	}
	if it.ActorType == MemberActor && viewerID != "" && it.ActorID == viewerID {
		return false
	}
	return len(MissingTranslations(it.Derived, []string{language})) > 0
}

// markUntranslated 把 items 中对成员 viewerID（语言为 language）来说还缺译文的项标为 Untranslated。
func markUntranslated(items []ItemDTO, viewerID, language string) {
	for i := range items {
		d := &items[i]
		d.Untranslated = untranslated(&Item{Kind: d.Kind, ActorType: d.Actor.Type, ActorID: d.Actor.ID, Visibility: d.Visibility, Content: d.Content, Derived: d.Derived}, viewerID, language)
	}
}

// MemberLanguage 返回成员 sub 的语言；不是成员（例如系统主体）时返回空字符串。
func MemberLanguage(ctx context.Context, db bun.IDB, sub identity.Subject) (string, error) {
	if sub.Type != member.Type {
		return "", nil
	}
	var language string
	err := db.NewSelect().Table("members").Column("language").Where("id = ?", sub.ID).Scan(ctx, &language)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return language, errors.Wrap(err, "load member language")
}

// VisibleItems 返回 ids 中属于 sub 所在工作区、sub 能看到的会话的时间线项：补译时页面报告看到了哪些消息，这里只认 sub 能看到的。
// 需要 conversations:read，没有时返回 ErrForbidden。
func VisibleItems(ctx context.Context, db bun.IDB, sub identity.Subject, ids []string) ([]Item, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	var items []Item
	if len(ids) == 0 {
		return items, nil
	}
	convs := visible(db.NewSelect().Model((*Conversation)(nil)).Column("conversation.id").Where("conversation.tenant_id = ?", sub.Tenant), sub)
	err := db.NewSelect().Model(&items).Where("tenant_id = ? AND id IN (?) AND conversation_id IN (?)", sub.Tenant, bun.In(ids), convs).Scan(ctx)
	return items, errors.Wrap(err, "load visible items")
}
