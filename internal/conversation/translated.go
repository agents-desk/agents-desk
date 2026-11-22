package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"

	"agents-desk/internal/contact"
	"agents-desk/internal/lang"
	"agents-desk/internal/push"
)

// Translated 是收到的一条消息识别和翻译的结果。
type Translated struct {
	Language     string            // 识别出的语言和书写方式（BCP 47），例如 hi-Latn
	Translations map[string]string // 按成员的语言（zh-CN、en……）给出的译文；与原文同语言的没有
}

// SaveTranslation 把工作区 tenant 中消息 itemID 的识别和翻译结果 t 写进它的派生内容（derived.language 和
// derived.translations，已有的译文保留，同一种语言的换成新的）。这是对方最新的一条消息时，会话和联系人的语言改为识别出的语言
// （对方的语言取最近一条消息）。成员手动设置了联系人的语言时（contact.LanguageManual）以它为准：derived.language 记手动设置的语言，
// 会话和联系人的语言不变。提交后推送给能看到会话的成员，页面重新读取时间线。消息不存在时什么也不做。
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
		// 成员手动设置了对方的语言：以它为准
		var manual string
		if c.ContactID != nil {
			var ct contact.Contact
			if err := tx.NewSelect().Model(&ct).Where("id = ? AND tenant_id = ?", *c.ContactID, tenant).Scan(ctx); err != nil {
				return errors.Wrap(err, "load contact")
			}
			if ct.LanguageManual {
				manual = ct.Language
			}
		}
		source := t.Language
		if manual != "" {
			source = manual
		}

		translations, _ := it.Derived["translations"].(map[string]any)
		if translations == nil {
			translations = map[string]any{}
		}
		for lang, text := range t.Translations {
			translations[lang] = text
		}
		it.Derived["language"], it.Derived["translations"] = source, translations
		if _, err := tx.NewUpdate().Model(&it).Column("derived").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save translation")
		}

		// 对方的语言取最近一条消息：后面没有对方的消息时才更新
		later, err := tx.NewSelect().Model((*Item)(nil)).
			Where("conversation_id = ? AND kind = ? AND actor_type = ? AND seq > ?", c.ID, Message, ContactActor, it.Seq).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check later messages")
		}
		if !later && manual == "" && t.Language != "" && c.Language != t.Language {
			c.Language = t.Language
			if _, err := tx.NewUpdate().Model(c).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update conversation language")
			}
			if c.ContactID != nil {
				_, err := tx.NewUpdate().Table("contacts").Set("language = ?", t.Language).Set("updated_at = ?", now()).
					Where("id = ? AND tenant_id = ?", *c.ContactID, tenant).Exec(ctx)
				if err != nil {
					return errors.Wrap(err, "update contact language")
				}
			}
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

// MissingTranslations 返回 targets 中派生内容 derived 还缺译文的语言：还没有识别过语言时是全部 targets，识别过时去掉已经有译文的、
// 和原文同一种语言的。
func MissingTranslations(derived map[string]any, targets []string) []string {
	source, _ := derived["language"].(string)
	if source == "" {
		return targets
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

// Translatable 报告时间线的一项是不是要翻译的消息：对方发来、对所有人可见、有文字。
func (it *Item) Translatable() bool {
	return it.Kind == Message && it.ActorType == ContactActor && it.Visibility == Everyone && it.Content.Text != ""
}

// markUntranslated 把 items 中要翻译、还缺语言 language 的译文的消息标为 Untranslated；language 为空时什么也不做。
func markUntranslated(items []ItemDTO, language string) {
	if language == "" {
		return
	}
	for i := range items {
		d := &items[i]
		it := Item{Kind: d.Kind, ActorType: d.Actor.Type, Visibility: d.Visibility, Content: d.Content}
		d.Untranslated = it.Translatable() && len(MissingTranslations(d.Derived, []string{language})) > 0
	}
}
