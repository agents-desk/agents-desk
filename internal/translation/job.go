package translation

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
)

// Topic 是翻译收到的消息的下游，在入口加入会话的 Downstream：时间线每有一项新内容，worker 收到一个任务。
var Topic = queue.Topic[conversation.Appended]("conversation.translate")

// Incoming 翻译收到的消息：识别语言和书写方式，翻译成工作区成员用到的每一种语言，存为消息的派生内容。
type Incoming struct {
	db         *bun.DB
	translator *Translator
	down       conversation.Downstream
}

// NewIncoming 返回翻译收到的消息的任务处理器；down 用来把派生内容的变化推送给能看到会话的成员。
func NewIncoming(db *bun.DB, translator *Translator, down conversation.Downstream) *Incoming {
	return &Incoming{db: db, translator: translator, down: down}
}

// Job 处理 Topic 的任务：新的一项是对方发来、对所有人可见、有文字的消息时，翻译它并保存（conversation.SaveTranslation）。
// 其他的项不处理；已经有工作区成员用到的全部语言的译文时也不再翻译，重复的任务不会重复调用模型。
// 模型没有配置时什么也不做（消息照常显示原文）；调用失败时返回错误，任务按退避重试，期间消息照常显示原文。
func (a *Incoming) Job(ctx context.Context, p conversation.Appended) error {
	var it conversation.Item
	err := a.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load timeline item")
	}
	if it.Kind != conversation.Message || it.ActorType != conversation.ContactActor || it.Visibility != conversation.Everyone || it.Content.Text == "" {
		return nil
	}
	targets, err := memberLanguages(ctx, a.db, p.TenantID)
	if err != nil || len(targets) == 0 {
		return err
	}
	if done(it.Derived, targets) {
		return nil
	}
	lang, translations, err := a.translator.Incoming(ctx, p.TenantID, it.ID, it.Content.Text, targets)
	if errors.Is(err, llm.ErrNotConfigured) {
		slog.DebugContext(ctx, "translation is not configured, skip", "item", it.ID)
		return nil
	}
	if err != nil {
		return err
	}
	return conversation.SaveTranslation(ctx, a.db, a.down, p.TenantID, it.ID, conversation.Translated{Language: lang, Translations: translations})
}

// done 报告派生内容 derived 中是不是已经识别过语言，并且 targets 中和原文不同的语言都有译文。
func done(derived map[string]any, targets []string) bool {
	lang, _ := derived["language"].(string)
	if lang == "" {
		return false
	}
	translations, _ := derived["translations"].(map[string]any)
	for _, t := range targets {
		if _, ok := translations[t]; !ok && !sameLanguage(lang, t) {
			return false
		}
	}
	return true
}

// RegisterJobs 在 worker 上登记翻译收到的消息的任务。
func RegisterJobs(w *queue.Worker, db *bun.DB, translator *Translator, down conversation.Downstream) {
	Topic.Handle(w, NewIncoming(db, translator, down).Job)
}
