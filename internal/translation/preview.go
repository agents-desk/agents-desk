package translation

import (
	"context"
	"slices"

	"github.com/cockroachdb/errors"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/lang"
	"agents-desk/internal/member"
)

// ErrForbidden 是调用者没有回复会话的权限。
var ErrForbidden = errorx.Define(errorx.PermissionDenied, "translation.forbidden")

// PreviewBody 是翻译预览的请求体。
type PreviewBody struct {
	Text     string `json:"text" minLength:"1" maxLength:"10000" doc:"成员写的回复"`
	Language string `json:"language,omitempty" maxLength:"35" doc:"发给对方的语言（BCP 47）；不填时取客户的语言"`
}

// PreviewInput 是翻译预览的 HTTP 请求。
type PreviewInput struct {
	ID   string `path:"id" format:"uuid"`
	Body PreviewBody
}

// PreviewDTO 是回复发出前的译文和回译。
type PreviewDTO struct {
	Needed          bool   `json:"needed" doc:"是否需要翻译：对方的语言未知，或者与成员的语言相同时为 false，按原文发出"`
	From            string `json:"from" doc:"成员的语言"`
	Language        string `json:"language" doc:"发给对方的语言；不需要翻译时可能为空"`
	Translation     string `json:"translation" doc:"发给对方的译文；不需要翻译时等于原文"`
	BackTranslation string `json:"back_translation" doc:"译文译回成员语言的结果，成员据此确认；不需要翻译时为空"`
}

// PreviewOutput 是翻译预览的响应。
type PreviewOutput struct {
	Body PreviewDTO
}

// Preview 是成员回复前的翻译预览：把他写的回复翻译成对方的语言，并译回他的语言。
type Preview struct {
	db         *bun.DB
	translator *Translator
	incoming   *Incoming
}

// NewPreview 返回翻译预览的动作；incoming 用来在还不知道对方的语言时先识别对方的消息。
func NewPreview(db *bun.DB, translator *Translator, incoming *Incoming) *Preview {
	return &Preview{db: db, translator: translator, incoming: incoming}
}

// Handle 为成员 sub 在他所在工作区的会话 id 中写的回复 b.Text 给出发给对方的译文和回译。目标语言是 b.Language，
// 不填时取会话中对方的语言；还不知道时（消息刚到、后台还没识别完）先识别对方最近的几条消息。目标语言仍然未知，
// 或者与成员的语言相同时不调用模型，按原文发出（Needed 为 false）。
// 会话不存在或看不到时返回 conversation.ErrNotFound，没有回复的权限时返回 ErrForbidden，翻译没有配置时返回 llm.ErrNotConfigured。
func (a *Preview) Handle(ctx context.Context, sub identity.Subject, id string, b PreviewBody) (*PreviewDTO, error) {
	if sub.Type != member.Type || !sub.Can(string(auth.ConversationsReply)) {
		return nil, ErrForbidden.New()
	}
	c, err := conversation.NewGet(a.db).Handle(ctx, sub, id)
	if err != nil {
		return nil, err
	}
	m, err := member.Find(ctx, a.db, sub.ID)
	if err != nil {
		return nil, err
	}
	from, to := string(m.Language), b.Language
	if to == "" {
		to = c.Language
	}
	if to == "" {
		if to, err = a.detect(ctx, sub, c.ID, from); err != nil {
			return nil, err
		}
	}
	if to == "" || lang.Same(from, to) {
		return &PreviewDTO{From: from, Language: to, Translation: b.Text}, nil
	}
	translation, back, err := a.translator.Reply(ctx, sub.Tenant, id, b.Text, from, to)
	if err != nil {
		return nil, err
	}
	return &PreviewDTO{Needed: true, From: from, Language: to, Translation: translation, BackTranslation: back}, nil
}

// detect 识别 sub 能看到的会话 id 中对方最近还没识别过语言的消息，顺带翻译成工作区主要语言和当前成员的语言
// （避免先只识别、后台任务再调一次模型补译），一旦确定出客户的语言就立刻返回，其余消息交由后台任务处理。
func (a *Preview) detect(ctx context.Context, sub identity.Subject, id, memberLang string) (string, error) {
	var ids []string
	err := a.db.NewSelect().Table("timeline_items").Column("id").
		Where("tenant_id = ? AND conversation_id = ? AND kind = ? AND actor_type = ? AND visibility = ?", sub.Tenant, id, conversation.Message, conversation.ContactActor, conversation.Everyone).
		Where("content->>'text' <> '' AND COALESCE(derived->>'language', '') = ''").
		OrderExpr("seq DESC").Limit(3).Scan(ctx, &ids)
	if err != nil {
		return "", errors.Wrap(err, "load undetected messages")
	}
	if len(ids) == 0 {
		return "", nil
	}
	targets, err := primaryLanguages(ctx, a.db, sub.Tenant)
	if err != nil {
		return "", err
	}
	if memberLang != "" && !slices.Contains(targets, memberLang) {
		targets = append(targets, memberLang)
		slices.Sort(targets)
	}
	for _, itemID := range ids {
		if err := a.incoming.Ensure(ctx, sub.Tenant, itemID, targets); err != nil {
			return "", err
		}
		c, err := conversation.NewGet(a.db).Handle(ctx, sub, id)
		if err != nil {
			return "", err
		}
		if c.Language != "" {
			return c.Language, nil
		}
	}
	return "", nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/translations。
func (a *Preview) HTTP(ctx context.Context, in *PreviewInput) (*PreviewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &PreviewOutput{Body: *dto}, nil
}
