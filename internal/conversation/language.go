package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
	"agents-desk/internal/lang"
)

// ErrInvalidLanguage 是设置的对方语言不是合法的 BCP 47 标签。
var ErrInvalidLanguage = errorx.Define(errorx.InvalidArgument, "conversation.invalid_language")

// LanguageInput 是手动设置对方语言的请求。
type LanguageInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Language string `json:"language" maxLength:"35" doc:"对方的语言（BCP 47），例如 hi-Latn；空字符串恢复为按收到的消息自动识别"`
	}
}

// SetLanguage 手动设置会话对方的语言：模型识别错了时，成员改成正确的语言。
type SetLanguage struct {
	db   *bun.DB
	down Downstream
}

// NewSetLanguage 返回手动设置对方语言的动作；down 是时间线有新内容时通知的下游。
func NewSetLanguage(db *bun.DB, down Downstream) *SetLanguage {
	return &SetLanguage{db: db, down: down}
}

// Handle 由 sub 把他所在工作区中会话 id 的对方语言设为 language（BCP 47，按规范写法保存），时间线中追加 conversation.language_set。
// 会话有联系人时同时设置联系人的语言，并记为手动设置：之后收到的消息照常识别和翻译，但不再改会话和联系人的语言，
// 这个联系人以后的会话也用它；会话中对方的消息的 derived.language 改为它，页面据此补上原来被当成同一种语言而没有翻译的消息。
// language 为空时恢复自动识别（追加 conversation.language_auto），下一条消息到来时按识别结果更新。
// 不是合法的标签时返回 ErrInvalidLanguage；没有变化时什么也不做。
func (a *SetLanguage) Handle(ctx context.Context, sub identity.Subject, id, language string) (*ConversationDTO, error) {
	if language != "" {
		canonical, ok := lang.Canonical(language)
		if !ok {
			return nil, ErrInvalidLanguage.New(errorx.Field("language"))
		}
		language = canonical
	}
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		var ct *contact.Contact
		if c.ContactID != nil {
			ct = new(contact.Contact)
			err := tx.NewSelect().Model(ct).Where("id = ? AND tenant_id = ?", *c.ContactID, c.TenantID).For("UPDATE").Scan(ctx)
			if err != nil {
				return nil, errors.Wrap(err, "load contact")
			}
		}

		// 恢复自动识别：语言先保持不变，下一条消息到来时更新
		if language == "" {
			if ct == nil || !ct.LanguageManual {
				return nil, nil
			}
			ct.LanguageManual = false
			if _, err := tx.NewUpdate().Model(ct).Column("language_manual", "updated_at").WherePK().Exec(ctx); err != nil {
				return nil, errors.Wrap(err, "update contact language")
			}
			return []*Item{event(sub, "conversation.language_auto", map[string]any{})}, nil
		}

		if c.Language == language && (ct == nil || (ct.LanguageManual && ct.Language == language)) {
			return nil, nil
		}
		from := c.Language
		c.Language = language
		if ct != nil {
			ct.Language, ct.LanguageManual = language, true
			if _, err := tx.NewUpdate().Model(ct).Column("language", "language_manual", "updated_at").WherePK().Exec(ctx); err != nil {
				return nil, errors.Wrap(err, "update contact language")
			}
		}
		// 已经识别过的对方消息按新的语言重新判断要不要翻译
		_, err := tx.NewUpdate().Model((*Item)(nil)).
			Set("derived = jsonb_set(derived, '{language}', to_jsonb(?::text))", language).
			Where("conversation_id = ? AND kind = ? AND actor_type = ? AND derived->>'language' IS NOT NULL", c.ID, Message, ContactActor).
			Exec(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "update message languages")
		}
		return []*Item{event(sub, "conversation.language_set", map[string]any{"from": from, "to": language})}, nil
	})
}

// HTTP 处理 PUT /t/{slug}/api/conversations/{id}/language。
func (a *SetLanguage) HTTP(ctx context.Context, in *LanguageInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Language)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
