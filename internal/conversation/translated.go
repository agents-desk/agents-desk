package conversation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"

	"agents-desk/internal/push"
)

// Translated 是收到的一条消息识别和翻译的结果。
type Translated struct {
	Language     string            // 识别出的语言和书写方式（BCP 47），例如 hi-Latn
	Translations map[string]string // 按成员的语言（zh-CN、en……）给出的译文；与原文同语言的没有
}

// SaveTranslation 把工作区 tenant 中消息 itemID 的识别和翻译结果 t 写进它的派生内容（derived.language 和
// derived.translations，已有的译文保留，同一种语言的换成新的）。这是对方最新的一条消息时，会话和联系人的语言改为识别出的语言
// （对方的语言取最近一条消息）。提交后推送给能看到会话的成员，页面重新读取时间线。消息不存在时什么也不做。
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
		translations, _ := it.Derived["translations"].(map[string]any)
		if translations == nil {
			translations = map[string]any{}
		}
		for lang, text := range t.Translations {
			translations[lang] = text
		}
		it.Derived["language"], it.Derived["translations"] = t.Language, translations
		if _, err := tx.NewUpdate().Model(&it).Column("derived").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "save translation")
		}

		// 对方的语言取最近一条消息：后面没有对方的消息时才更新
		later, err := tx.NewSelect().Model((*Item)(nil)).
			Where("conversation_id = ? AND kind = ? AND actor_type = ? AND seq > ?", c.ID, Message, ContactActor, it.Seq).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check later messages")
		}
		if !later && t.Language != "" && c.Language != t.Language {
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
