package translation

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/conversation"
)

// FillBody 是补译的请求体：页面上看到的、还缺我的语言的译文的消息。
type FillBody struct {
	ItemIDs []string `json:"item_ids" minItems:"1" maxItems:"100" nullable:"false" doc:"时间线中的项，页面上可见、标为 untranslated 的消息"`
}

// FillInput 是补译的请求。
type FillInput struct {
	ID   string `path:"id" format:"uuid"`
	Body FillBody
}

// FillDTO 是补译的结果。
type FillDTO struct {
	Queued int `json:"queued" doc:"放进后台补译的消息数；译好后像收到的消息一样推送"`
}

// FillOutput 是补译的响应。
type FillOutput struct {
	Body FillDTO
}

// FillMissing 补译成员看到的消息：收到时还没有这位成员的语言（成员后来改了语言、新成员加入），配置翻译之前收到，
// 或者翻译最终失败了的消息，在成员看到时补上。由页面报告看到了哪些消息，这里判断哪些真的要补。
type FillMissing struct {
	db         *bun.DB
	translator *Translator
}

// NewFillMissing 返回补译的动作。
func NewFillMissing(db *bun.DB, translator *Translator) *FillMissing {
	return &FillMissing{db: db, translator: translator}
}

// Handle 把 sub 能看到的会话 id 中、itemIDs 里要翻译（对方发来、对所有人可见、有文字）、还缺 sub 的语言的译文的消息，
// 各放进一个补译任务（FillTopic），返回放进去的数量。翻译不可用时什么也不做。同一条消息重复请求时任务按消息加锁，
// 不会重复调用模型。会话不存在或 sub 看不到时返回 conversation.ErrNotFound。
func (a *FillMissing) Handle(ctx context.Context, sub identity.Subject, id string, itemIDs []string) (int, error) {
	if _, err := conversation.NewGet(a.db).Handle(ctx, sub, id); err != nil {
		return 0, err
	}
	enabled, err := a.translator.Enabled(ctx)
	if err != nil || !enabled {
		return 0, err
	}
	var language string
	err = a.db.NewSelect().Table("members").Column("language").Where("id = ?", sub.ID).Scan(ctx, &language)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, errors.Wrap(err, "load member language")
	}
	ids := make([]string, 0, len(itemIDs))
	for _, s := range itemIDs {
		if _, err := uuid.Parse(s); err == nil {
			ids = append(ids, s)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var items []conversation.Item
	err = a.db.NewSelect().Model(&items).Where("tenant_id = ? AND conversation_id = ? AND id IN (?)", sub.Tenant, id, bun.In(ids)).Scan(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "load timeline items")
	}
	var jobs []FillJob
	for _, it := range items {
		if it.Translatable() && len(conversation.MissingTranslations(it.Derived, []string{language})) > 0 {
			jobs = append(jobs, FillJob{TenantID: sub.Tenant, ItemID: it.ID, Language: language})
		}
	}
	if err := FillTopic.PublishAll(ctx, a.db, jobs); err != nil {
		return 0, errors.Wrap(err, "publish fill jobs")
	}
	return len(jobs), nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/translations/fill。
func (a *FillMissing) HTTP(ctx context.Context, in *FillInput) (*FillOutput, error) {
	sub, _ := identity.FromContext(ctx)
	n, err := a.Handle(ctx, sub, in.ID, in.Body.ItemIDs)
	if err != nil {
		return nil, err
	}
	return &FillOutput{Body: FillDTO{Queued: n}}, nil
}
