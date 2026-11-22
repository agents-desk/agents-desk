package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
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

// SaveTranslation 把工作区 tenant 中消息 itemID 的识别和翻译结果 t 写进它的派生内容：derived.language 是这条消息的语言，
// 已经识别过的保持不变；derived.translations 中已有的译文保留，同一种语言的换成新的。
// 然后按 CustomerLanguage 更新会话和联系人的语言（成员手动设置了联系人的语言时不改）。提交后推送给能看到会话的成员，
// 页面重新读取。消息不存在时什么也不做。
func SaveTranslation(ctx context.Context, db *bun.DB, down Downstream, tenant, itemID string, t Translated) error {
	return database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		var it Item
		err := tx.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", itemID, tenant).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "load timeline item")
		}
		c, err := find(ctx, tx, tenant, it.ConversationID, true)
		if err != nil {
			return err
		}

		// 消息的语言和译文
		if source, _ := it.Derived["language"].(string); source == "" && t.Language != "" {
			it.Derived["language"] = t.Language
		}
		translations, _ := it.Derived["translations"].(map[string]any)
		if translations == nil {
			translations = map[string]any{}
		}
		for target, text := range t.Translations {
			translations[target] = text
		}
		it.Derived["translations"] = translations
		if _, err := tx.NewUpdate().Model(&it).Column("derived").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save translation")
		}

		// 客户的语言：手动设置的不改，否则按最近几条消息
		if err := updateCustomerLanguage(ctx, tx, c); err != nil {
			return err
		}

		if down.Hub == nil {
			return nil
		}
		audience, err := audienceOf(ctx, tx, down.Roles, c, nil)
		if err != nil {
			return err
		}
		hub, ev := down.Hub, push.Event{Type: push.Conversation, ID: c.ID, Seq: c.LastSeq}
		tx.AfterCommit(func(ctx context.Context) error {
			return errors.Wrap(push.Send(ctx, hub, tenant, audience, ev), "push translation")
		})
		return nil
	})
}

// CustomerLanguage 返回会话 c 的客户的语言：客户（有联系人时是这个联系人的全部会话，否则是这个会话）最近 customerLanguageWindow 条
// 判断得出语言的消息中出现最多的语言，一样多时取最近的。一句 ok、一段粘贴的英文报错不会改变客户的语言，连续换了语言才会。
// 还没有判断得出语言的消息时返回空字符串。
func CustomerLanguage(ctx context.Context, db bun.IDB, c *Conversation) (string, error) {
	q := db.NewSelect().TableExpr("timeline_items AS t").ColumnExpr("t.derived->>'language'").
		Where("t.tenant_id = ? AND t.kind = ? AND t.actor_type = ?", c.TenantID, Message, ContactActor).
		Where("t.derived->>'language' NOT IN ('', ?)", Undetermined).
		OrderExpr("t.created_at DESC, t.seq DESC").Limit(customerLanguageWindow)
	if c.ContactID != nil {
		q = q.Where("t.conversation_id IN (SELECT id FROM conversations WHERE tenant_id = ? AND contact_id = ?)", c.TenantID, *c.ContactID)
	} else {
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

// updateCustomerLanguage 在事务 tx 中按 CustomerLanguage 更新已锁住的会话 c 和它的联系人的语言；联系人的语言是成员手动设置的时不改。
func updateCustomerLanguage(ctx context.Context, tx bun.IDB, c *Conversation) error {
	if c.ContactID != nil {
		var ct contact.Contact
		if err := tx.NewSelect().Model(&ct).Where("id = ? AND tenant_id = ?", *c.ContactID, c.TenantID).Scan(ctx); err != nil {
			return errors.Wrap(err, "load contact")
		}
		if ct.LanguageManual {
			return nil
		}
	}
	language, err := CustomerLanguage(ctx, tx, c)
	if err != nil || language == "" || language == c.Language {
		return err
	}
	c.Language = language
	if _, err := tx.NewUpdate().Model(c).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
		return errors.Wrap(err, "update conversation language")
	}
	if c.ContactID != nil {
		_, err := tx.NewUpdate().Table("contacts").Set("language = ?", language).Set("updated_at = ?", now()).
			Where("id = ? AND tenant_id = ?", *c.ContactID, c.TenantID).Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "update contact language")
		}
	}
	return nil
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
		if _, ok := translations[t]; !ok && !lang.Same(source, t) {
			missing = append(missing, t)
		}
	}
	return missing
}

// Translatable 报告时间线的一项是不是要识别和翻译的消息：对方发来、对所有人可见、有文字。
func (it *Item) Translatable() bool {
	return it.Kind == Message && it.ActorType == ContactActor && it.Visibility == Everyone && it.Content.Text != ""
}

// untranslated 报告时间线的一项对语言是 language 的成员来说是不是还缺译文：要翻译的消息、还没有这种语言的译文、也不是这种语言。
func untranslated(it *Item, language string) bool {
	return language != "" && it.Translatable() && len(MissingTranslations(it.Derived, []string{language})) > 0
}

// markUntranslated 把 items 中对语言是 language 的成员来说还缺译文的消息标为 Untranslated。
func markUntranslated(items []ItemDTO, language string) {
	for i := range items {
		d := &items[i]
		d.Untranslated = untranslated(&Item{Kind: d.Kind, ActorType: d.Actor.Type, Visibility: d.Visibility, Content: d.Content, Derived: d.Derived}, language)
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
