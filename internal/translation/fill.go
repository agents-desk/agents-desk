package translation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
)

// FillBody 是补译的请求体：页面上看到的、还缺我的语言的译文的消息。
type FillBody struct {
	ItemIDs []string `json:"item_ids" minItems:"1" maxItems:"100" nullable:"false" doc:"时间线中的项：会话中标为 untranslated 的消息、会话列表中 preview_untranslated 的那一行的 preview_item_id"`
}

// FillInput 是补译的请求。
type FillInput struct {
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

// FillMissing 补译成员看到的消息：收到时只翻译成工作区的主要语言，其他语言的成员看到消息时补上他们的语言；
// 配置翻译之前收到、翻译最终失败了的消息也这样补上。页面（时间线、会话列表）报告看到了哪些消息，这里判断哪些真的要补。
type FillMissing struct {
	db         *bun.DB
	translator *Translator
}

// NewFillMissing 返回补译的动作。
func NewFillMissing(db *bun.DB, translator *Translator) *FillMissing {
	return &FillMissing{db: db, translator: translator}
}

// Handle 把 itemIDs 中 sub 能看到的、要翻译（对方发来、对所有人可见、有文字）、还缺 sub 的语言的译文的消息，
// 各放进一个补译任务（FillTopic），返回放进去的数量；看不到的、不认识的 ID 忽略。翻译不可用时什么也不做。
// 同一条消息重复请求时任务按消息加锁，不会重复调用模型。
func (a *FillMissing) Handle(ctx context.Context, sub identity.Subject, itemIDs []string) (int, error) {
	enabled, err := a.translator.Enabled(ctx)
	if err != nil || !enabled {
		return 0, err
	}
	language, err := conversation.MemberLanguage(ctx, a.db, sub)
	if err != nil || language == "" {
		return 0, err
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
	items, err := conversation.VisibleItems(ctx, a.db, sub, ids)
	if err != nil {
		return 0, err
	}
	var jobs []FillJob
	for _, it := range items {
		if it.ActorType == conversation.MemberActor && it.ActorID == sub.ID {
			continue
		}
		if it.Translatable() && len(conversation.MissingTranslations(it.Derived, []string{language})) > 0 {
			jobs = append(jobs, FillJob{TenantID: sub.Tenant, ItemID: it.ID, Language: language})
		}
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		for _, job := range jobs {
			if err := FillTopic.Publish(ctx, tx, job, queue.DedupKey(job.ItemID+":"+job.Language)); err != nil {
				return errors.Wrap(err, "publish fill jobs")
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(jobs), nil
}

// HTTP 处理 POST /t/{slug}/api/translations/fill。
func (a *FillMissing) HTTP(ctx context.Context, in *FillInput) (*FillOutput, error) {
	sub, _ := identity.FromContext(ctx)
	n, err := a.Handle(ctx, sub, in.Body.ItemIDs)
	if err != nil {
		return nil, err
	}
	return &FillOutput{Body: FillDTO{Queued: n}}, nil
}
