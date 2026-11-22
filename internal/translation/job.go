package translation

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
)

// Topic 是翻译收到的消息的下游，在入口加入会话的 Downstream：时间线每有一项新内容，worker 收到一个任务。
var Topic = queue.Topic[conversation.Appended]("conversation.translate")

// FillTopic 是补译：成员看到一条还没有自己语言的译文的消息时（见 FillMissing），补上这种语言的译文。
var FillTopic = queue.Topic[FillJob]("translation.fill")

// FillJob 是一条补译任务：把工作区 TenantID 中的消息 ItemID 补译成 Language。
type FillJob struct {
	TenantID string `json:"tenant_id"`
	ItemID   string `json:"item_id"`
	Language string `json:"language"` // 要补的语言，成员的语言
}

// Incoming 识别和翻译收到的消息，存为消息的派生内容。收到时（Job）识别语言并翻译成工作区的主要语言，补译（Fill）补上看到它的成员的语言；
// 两者走同一条路径：按消息加锁，只翻译还缺的语言，还没识别过语言的顺带识别。
type Incoming struct {
	db         *bun.DB
	locker     *lock.Locker
	translator *Translator
	down       conversation.Downstream
}

// NewIncoming 返回翻译收到的消息的任务处理器；locker 让同一条消息同一时刻只翻译一次，down 用来把派生内容的变化推送给能看到会话的成员。
func NewIncoming(db *bun.DB, locker *lock.Locker, translator *Translator, down conversation.Downstream) *Incoming {
	return &Incoming{db: db, locker: locker, translator: translator, down: down}
}

// Job 处理 Topic 的任务：新的一项是对方发来、对所有人可见、有文字的消息时，识别语言（自动回复、分配规则等不等成员看就要用到），
// 并翻译成工作区的主要语言。
func (a *Incoming) Job(ctx context.Context, p conversation.Appended) error {
	targets, err := primaryLanguages(ctx, a.db, p.TenantID)
	if err != nil {
		return err
	}
	return a.Ensure(ctx, p.TenantID, p.ItemID, targets)
}

// Fill 处理 FillTopic 的任务：把消息补译成任务中的语言。
func (a *Incoming) Fill(ctx context.Context, p FillJob) error {
	return a.Ensure(ctx, p.TenantID, p.ItemID, []string{p.Language})
}

// lockTTL 是翻译一条消息的锁的有效期，明显长于一次模型调用；lockWait 是等别的任务翻译完同一条消息的最长时间。
const (
	lockTTL  = 3 * time.Minute
	lockWait = 2 * time.Minute
)

// Ensure 确保工作区 tenant 中的消息 itemID 识别过语言、并有 targets 中每一种语言的译文，存为派生内容（conversation.SaveTranslation）。
// 已经有译文、和原文同一种语言、或者判断不出语言的不再翻译；什么都不缺时不调用模型。识别时把客户目前的语言给模型作参考。
// 按消息加锁：同一条消息的任务（收到时的、补译的、重复的）依次执行，后执行的看到已经翻译好就直接结束。
// 不是对方发来、对所有人可见、有文字的消息时什么也不做；模型没有配置时什么也不做（消息照常显示原文）；
// 调用失败时返回错误，任务按退避重试，期间消息照常显示原文。
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
	if source, _ := it.Derived["language"].(string); source != "" && len(missing) == 0 {
		return nil
	}
	var usual string
	if err := a.db.NewSelect().Table("conversations").Column("language").Where("id = ?", it.ConversationID).Scan(ctx, &usual); err != nil {
		return errors.Wrap(err, "load conversation language")
	}
	detected, translations, err := a.translator.Incoming(ctx, tenant, it.ID, it.Content.Text, usual, missing)
	if errors.Is(err, llm.ErrNotConfigured) {
		slog.DebugContext(ctx, "translation is not configured, skip", "item", it.ID)
		return nil
	}
	if err != nil {
		return err
	}
	return conversation.SaveTranslation(ctx, a.db, a.down, tenant, it.ID, conversation.Translated{Language: detected, Translations: translations})
}

// RegisterJobs 在 worker 上登记翻译收到的消息和补译的任务。
func RegisterJobs(w *queue.Worker, db *bun.DB, locker *lock.Locker, translator *Translator, down conversation.Downstream) {
	in := NewIncoming(db, locker, translator, down)
	Topic.Handle(w, in.Job)
	FillTopic.Handle(w, in.Fill)
}
