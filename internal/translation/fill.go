package translation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
)

// FillBody 是补译的请求体：页面上看到的、还缺我的语言的译文的消息、工单元数据或联系人备注。
type FillBody struct {
	ItemIDs    []string `json:"item_ids,omitempty" maxItems:"100" doc:"时间线中标为 untranslated 的消息或列表 preview_item_id"`
	TicketIDs  []string `json:"ticket_ids,omitempty" maxItems:"100" doc:"工单列表或详情中 meta_untranslated 为 true 的工单 ID"`
	ContactIDs []string `json:"contact_ids,omitempty" maxItems:"50" doc:"联系人详情或侧栏中 notes_untranslated 为 true 的联系人 ID"`
}

// FillInput 是补译的请求。
type FillInput struct {
	Body FillBody
}

// FillDTO 是补译的结果。
type FillDTO struct {
	Queued int `json:"queued" doc:"放进后台补译的任务数；译好后推送刷新"`
}

// FillOutput 是补译的响应。
type FillOutput struct {
	Body FillDTO
}

// FillMissing 补译成员看到的内容（时间线消息、工单标题与描述、联系人长期备注）：
// 页面报告看到了哪些还缺当前成员语言的项，这里校验可见性后放进补译队列。
type FillMissing struct {
	db         *bun.DB
	translator *Translator
}

// NewFillMissing 返回补译的动作。
func NewFillMissing(db *bun.DB, translator *Translator) *FillMissing {
	return &FillMissing{db: db, translator: translator}
}

// Handle 校验可见性后，为缺失当前成员语言译文的消息、工单标题/描述与联系人备注发布补译任务。
func (a *FillMissing) Handle(ctx context.Context, sub identity.Subject, b FillBody) (int, error) {
	enabled, err := a.translator.EnabledFor(ctx, sub.Tenant)
	if err != nil || !enabled {
		return 0, err
	}
	language, err := conversation.MemberLanguage(ctx, a.db, sub)
	if err != nil || language == "" {
		return 0, err
	}

	var jobs []FillJob
	if ids := parseUUIDs(b.ItemIDs); len(ids) > 0 {
		items, err := conversation.VisibleItems(ctx, a.db, sub, ids)
		if err != nil {
			return 0, err
		}
		for _, it := range items {
			if it.ActorType == conversation.MemberActor && it.ActorID == sub.ID {
				continue
			}
			if it.Translatable() && len(conversation.MissingTranslations(it.Derived, []string{language})) > 0 {
				jobs = append(jobs, FillJob{TenantID: sub.Tenant, ItemID: it.ID, Language: language})
			}
		}
	}

	if tids := parseUUIDs(b.TicketIDs); len(tids) > 0 {
		visibleIDs, err := conversation.VisibleTicketIDs(ctx, a.db, sub, tids)
		if err != nil {
			return 0, err
		}
		if len(visibleIDs) > 0 {
			var metas []conversation.TicketMeta
			if err := a.db.NewSelect().Model(&metas).
				Where("tenant_id = ? AND conversation_id IN (?)", sub.Tenant, bun.In(visibleIDs)).
				Scan(ctx); err != nil {
				return 0, errors.Wrap(err, "load ticket metas for fill")
			}
			for _, tk := range metas {
				if len(conversation.TicketMetaMissingLanguages(tk.Title, tk.Description, tk.Derived, []string{language})) > 0 {
					jobs = append(jobs, FillJob{TenantID: sub.Tenant, TicketID: tk.ConversationID, Language: language})
				}
			}
		}
	}

	if cids := parseUUIDs(b.ContactIDs); len(cids) > 0 {
		visibleContacts, err := contact.VisibleByIDs(ctx, a.db, sub, cids)
		if err != nil {
			return 0, err
		}
		for _, ct := range visibleContacts {
			if ct.Notes != "" && len(conversation.MissingTranslations(ct.NotesDerived, []string{language})) > 0 {
				jobs = append(jobs, FillJob{TenantID: sub.Tenant, ContactID: ct.ID, Language: language})
			}
		}
	}

	if len(jobs) == 0 {
		return 0, nil
	}
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		for _, job := range jobs {
			key := job.ItemID + ":" + job.Language
			if job.TicketID != "" {
				key = "ticket:" + job.TicketID + ":" + job.Language
			} else if job.ContactID != "" {
				key = "contact:" + job.ContactID + ":" + job.Language
			}
			if err := FillTopic.Publish(ctx, tx, job, queue.DedupKey(key)); err != nil {
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

// parseUUIDs 过滤并保留合法的 UUID 字符串。
func parseUUIDs(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if _, err := uuid.Parse(s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// HTTP 处理 POST /t/{slug}/api/translations/fill。
func (a *FillMissing) HTTP(ctx context.Context, in *FillInput) (*FillOutput, error) {
	sub, _ := identity.FromContext(ctx)
	n, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &FillOutput{Body: FillDTO{Queued: n}}, nil
}
