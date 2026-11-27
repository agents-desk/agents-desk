package translation

import (
	"context"
	"database/sql"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
	"agents-desk/internal/lang"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
)

// Topic 是翻译收到的消息的下游，在入口加入会话的 Downstream：时间线每有一项新内容，worker 收到一个任务。
var Topic = queue.Topic[conversation.Appended]("conversation.translate")

// FillTopic 是补译：成员看到还缺自己语言译文的消息、工单元数据或联系人备注时（见 FillMissing），补上这种语言的译文。
var FillTopic = queue.Topic[FillJob]("translation.fill")

// FillJob 是一条补译任务：把工作区 TenantID 中的消息（ItemID）、工单标题与描述（TicketID）或联系人备注（ContactID）补译成 Language。
type FillJob struct {
	TenantID  string `json:"tenant_id"`
	ItemID    string `json:"item_id,omitempty"`
	TicketID  string `json:"ticket_id,omitempty"`
	ContactID string `json:"contact_id,omitempty"`
	Language  string `json:"language"` // 要补的目标语言（BCP 47）
}

// Incoming 识别和翻译时间线消息、工单标题/描述以及联系人长期备注，存为对应的派生内容。
// 收到新消息时（Job）识别语言并预译成「当前会话参与者语言 ∪ 工作区主要语言」，补译（Fill）补上阅读者语言；
// 两者走同一条加锁路径：只翻译还缺的语言，还没识别过语言的顺带识别。
type Incoming struct {
	db         *bun.DB
	locker     *lock.Locker
	translator *Translator
	down       conversation.Downstream
}

// NewIncoming 返回翻译任务处理器；locker 保证同一对象同一时刻只翻译一次，down 用来把派生内容的变化推送给能看到它的成员。
func NewIncoming(db *bun.DB, locker *lock.Locker, translator *Translator, down conversation.Downstream) *Incoming {
	return &Incoming{db: db, locker: locker, translator: translator, down: down}
}

// Job 处理 Topic 的任务：
//   - 当新项是工单创建、标题/描述修改或关联受影响客户事件时，异步识别并翻译工单标题与描述；
//   - 当新项是可译消息或内部备注时，按「会话当前参与成员语言 ∪ @提及成员语言 ∪ 工作区主要语言」一次批量预译。
func (a *Incoming) Job(ctx context.Context, p conversation.Appended) error {
	var it conversation.Item
	err := a.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load timeline item for job")
	}
	if it.Kind == conversation.Event {
		switch it.Content.Event {
		case "ticket.created", "ticket.contact_linked":
			return a.EnsureTicketMetaForAudience(ctx, p.TenantID, it.ConversationID)
		case "ticket.updated":
			if changes, ok := it.Content.Data["changes"].(map[string]any); ok {
				if _, hasTitle := changes["title"]; hasTitle {
					return a.EnsureTicketMetaForAudience(ctx, p.TenantID, it.ConversationID)
				}
				if _, hasDesc := changes["description"]; hasDesc {
					return a.EnsureTicketMetaForAudience(ctx, p.TenantID, it.ConversationID)
				}
			}
		}
		return nil
	}
	if !it.Translatable() {
		return nil
	}
	// 个人商务渠道显式关闭自动翻译时跳过入站预译（成员仍可在界面按需补译）
	bp, isBiz, err := a.translator.businessPolicyFor(ctx, p.TenantID, it.ConversationID)
	if err != nil {
		return err
	}
	if isBiz && bp.AutoTranslate != nil && !*bp.AutoTranslate {
		return nil
	}
	targets, err := a.itemPretranslateLanguages(ctx, p.TenantID, &it)
	if err != nil {
		return err
	}
	return a.Ensure(ctx, p.TenantID, p.ItemID, targets)
}

// Fill 处理 FillTopic 的任务：按任务中指定的对象类型补译成目标语言。
func (a *Incoming) Fill(ctx context.Context, p FillJob) error {
	switch {
	case p.TicketID != "":
		return a.EnsureTicketMeta(ctx, p.TenantID, p.TicketID, []string{p.Language})
	case p.ContactID != "":
		return a.EnsureContactNotes(ctx, p.TenantID, p.ContactID, []string{p.Language})
	case p.ItemID != "":
		return a.Ensure(ctx, p.TenantID, p.ItemID, []string{p.Language})
	default:
		return nil
	}
}

// itemPretranslateLanguages 汇总时间线项 it 落库后应立即预译的目标语言集合：
// 当前会话的直接参与成员（含负责人、工单发起人、参与者、@提及成员，排除发送者自己）语言 ∪ 工作区主要语言。
func (a *Incoming) itemPretranslateLanguages(ctx context.Context, tenant string, it *conversation.Item) ([]string, error) {
	primary, err := member.PrimaryLanguages(ctx, a.db, tenant)
	if err != nil {
		return nil, err
	}
	audience, err := conversationMemberLanguages(ctx, a.db, tenant, it.ConversationID, it.ActorType, it.ActorID, it.Content.Mentions)
	if err != nil {
		return nil, err
	}
	return mergeUniqueLanguages(primary, audience), nil
}

// conversationMemberLanguages 查询会话 conversationID 中直接参与成员（参与者、负责人、工单发起人、@提及成员）的沟通语言，
// 排除发送者自己（若为成员）与已停用成员。
func conversationMemberLanguages(ctx context.Context, db bun.IDB, tenant, conversationID string, authorType conversation.ActorType, authorID string, mentions []string) ([]string, error) {
	var memberIDs []string
	seen := map[string]bool{}
	if authorType == conversation.MemberActor && authorID != "" {
		seen[authorID] = true
	}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			memberIDs = append(memberIDs, id)
		}
	}
	for _, id := range mentions {
		add(id)
	}
	var convRow struct {
		Kind         conversation.Kind      `bun:"kind"`
		AssigneeType conversation.ActorType `bun:"assignee_type"`
		AssigneeID   string                 `bun:"assignee_id"`
	}
	err := db.NewSelect().Table("conversations").Column("kind", "assignee_type", "assignee_id").
		Where("id = ? AND tenant_id = ?", conversationID, tenant).Scan(ctx, &convRow)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, errors.Wrap(err, "load conversation for audience languages")
	}
	if convRow.AssigneeType == conversation.MemberActor {
		add(convRow.AssigneeID)
	}
	if convRow.Kind == conversation.Ticket {
		var createdBy *string
		if err := db.NewSelect().Table("tickets").Column("created_by").
			Where("conversation_id = ? AND tenant_id = ?", conversationID, tenant).Scan(ctx, &createdBy); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, errors.Wrap(err, "load ticket creator for audience languages")
		}
		if createdBy != nil {
			add(*createdBy)
		}
	}
	var partIDs []string
	if err := db.NewSelect().Table("conversation_participants").Column("participant_id").
		Where("conversation_id = ? AND participant_type = ? AND left_at IS NULL", conversationID, conversation.MemberActor).
		Scan(ctx, &partIDs); err != nil {
		return nil, errors.Wrap(err, "load participants for audience languages")
	}
	for _, id := range partIDs {
		add(id)
	}
	if len(memberIDs) == 0 {
		return nil, nil
	}
	var langs []string
	if err := db.NewSelect().Table("members").Column("language").
		Where("id IN (?) AND disabled_at IS NULL AND language <> ''", bun.In(memberIDs)).
		Scan(ctx, &langs); err != nil {
		return nil, errors.Wrap(err, "load audience member languages")
	}
	return langs, nil
}

// mergeUniqueLanguages 合并多组语言标签并按 BCP 47 语义去重排序。
func mergeUniqueLanguages(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		for _, raw := range g {
			l := strings.TrimSpace(raw)
			if l == "" {
				continue
			}
			dup := false
			for _, existing := range out {
				if lang.Same(existing, l) {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, l)
			}
		}
	}
	slices.Sort(out)
	return out
}

// lockTTL 是翻译一条消息的锁的有效期，明显长于一次模型调用；lockWait 是等别的任务翻译完同一条消息的最长时间。
const (
	lockTTL  = 3 * time.Minute
	lockWait = 2 * time.Minute
)

// Ensure 确保工作区 tenant 中的消息 itemID 识别过语言、并有 targets 中每一种语言的译文，存为派生内容（conversation.SaveTranslation）。
// 已经有译文、和原文同一种语言、或者判断不出语言的不再翻译；什么都不缺时不调用模型。识别时把发言者常用的语言给模型作参考。
// 按消息加锁：同一条消息的任务（收到时的、补译的、重复的）依次执行，后执行的看到已经翻译好就直接结束。
func (a *Incoming) Ensure(ctx context.Context, tenant, itemID string, targets []string) error {
	lk, err := a.locker.Wait(ctx, "translation:"+itemID, lockTTL, lockWait)
	if err != nil {
		return errors.Wrap(err, "lock item for translation")
	}
	defer func() { _ = lk.Release(context.WithoutCancel(ctx)) }()

	var it conversation.Item
	err = a.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", itemID, tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load timeline item")
	}
	if !it.Translatable() {
		return nil
	}
	missing := conversation.MissingTranslations(it.Derived, targets)
	source, _ := it.Derived["language"].(string)
	if source != "" && len(missing) == 0 {
		return nil
	}
	var translated conversation.Translated
	if source != "" {
		// 已经识别过源语言：只补译缺的语言，不重新识别
		translations, err := a.translator.Translate(ctx, tenant, &it, source, missing)
		if errors.Is(err, llm.ErrNotConfigured) {
			slog.DebugContext(ctx, "translation is not configured, skip", "item", it.ID)
			return nil
		}
		if err != nil {
			return err
		}
		translated.Translations = translations
	} else {
		var usual string
		if it.ActorType == conversation.MemberActor && it.ActorID != "" {
			if err := a.db.NewSelect().Table("members").Column("language").Where("id = ?", it.ActorID).Scan(ctx, &usual); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(err, "load author language")
			}
		} else {
			err := a.db.NewRaw(`SELECT COALESCE(NULLIF(ct.language, ''), NULLIF(eg.language, ''), c.language) FROM conversations c
				LEFT JOIN contacts ct ON ct.id = c.contact_id AND ct.tenant_id = c.tenant_id
				LEFT JOIN external_groups eg ON eg.id = c.external_group_id AND eg.tenant_id = c.tenant_id
				WHERE c.id = ? AND c.tenant_id = ?`, it.ConversationID, tenant).Scan(ctx, &usual)
			if err != nil {
				return errors.Wrap(err, "load conversation language")
			}
		}
		detected, translations, err := a.translator.Incoming(ctx, tenant, &it, usual, missing)
		if errors.Is(err, llm.ErrNotConfigured) {
			slog.DebugContext(ctx, "translation is not configured, skip", "item", it.ID)
			return nil
		}
		if err != nil {
			return err
		}
		translated = conversation.Translated{Language: detected, Translations: translations}
	}
	return conversation.SaveTranslation(ctx, a.db, a.down, tenant, it.ID, translated)
}

// RegisterJobs 在 worker 上登记翻译收到的消息、补译与工单公开进度广播任务。
func RegisterJobs(w *queue.Worker, db *bun.DB, locker *lock.Locker, translator *Translator, down conversation.Downstream) {
	in := NewIncoming(db, locker, translator, down)
	Topic.Handle(w, in.Job)
	FillTopic.Handle(w, in.Fill)
	TicketBroadcastTopic.Handle(w, NewTicketBroadcast(db, locker, translator, down).Job)
}
