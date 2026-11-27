package agent

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/conversation"
)

// Topic 是时间线有新内容时交给 AI 的下游，在入口中加入会话的 Downstream：时间线每有一项新内容，worker 收到一个任务。
var Topic = queue.Topic[conversation.Appended]("agent.generate")

// Triggers 报告时间线项是否可能触发 AI 回复：成员发出的、所有人可见的消息。
// 机器人的消息不触发，避免 AI 之间互相回复；备注、事件和内部聊天之外的项不触发。
func Triggers(it *conversation.Item) bool {
	return it.Kind == conversation.Message && it.ActorType == conversation.MemberActor && it.Visibility == conversation.Everyone
}

// GenerateRequest 是一次生成要交给模型的信息：哪个工作区、哪个会话、由哪个机器人回复哪一条消息。
// 提示词的上下文（最近的对话、知识库）由实现从会话中读取，不放在任务里，避免任务随消息增长。
type GenerateRequest struct {
	TenantID       string
	ConversationID string
	BotID          string
	TriggerItemID  string
}

// Chunk 是模型产出的一段内容：Text 是一段新文字，Usage 是结束时的用量，Err 表示这次生成失败。
type Chunk struct {
	Text  string
	Usage map[string]any
	Err   error
}

// Generator 为一个生成产出回复片段，由具体的模型调用实现。
// 实现返回的频道由调用方读到关闭为止；生成被取消时 ctx 会被取消。
type Generator interface {
	Generate(ctx context.Context, req GenerateRequest) (<-chan Chunk, error)
}

// Runner 消费时间线事件：会话中有启用的机器人参与者时，为成员的新消息开始一次生成，
// 把片段写进生成并推送，最后定稿为一条时间线消息。没有接入模型实现时不生成任何内容。
type Runner struct {
	db        *bun.DB
	generator Generator
	open      *Open
	append    *Append
	finalize  *Finalize
	fail      *Fail
}

// NewRunner 返回 AI 的执行器；down 决定定稿后通知哪些下游，hub 用于推送生成片段，generator 为 nil 时不做任何事。
func NewRunner(db *bun.DB, down conversation.Downstream, hub *broadcast.Hub, generator Generator) *Runner {
	return &Runner{
		db:        db,
		generator: generator,
		open:      NewOpen(db),
		append:    NewAppend(db, hub),
		finalize:  NewFinalize(db, down, hub),
		fail:      NewFail(db, hub),
	}
}

// RegisterJobs 在 worker 上登记 AI 的生成任务。
func RegisterJobs(w *queue.Worker, r *Runner) {
	Topic.Handle(w, r.Job)
}

// Job 处理时间线的一条新内容：不是成员消息、会话里没有启用的机器人、或者已经处理过这条触发时什么也不做。
// 模型失败时把生成标记为失败；成员中途取消时安静地停下。
func (r *Runner) Job(ctx context.Context, p conversation.Appended) error {
	if r.generator == nil {
		return nil
	}
	var it conversation.Item
	err := r.db.NewSelect().Model(&it).Where("id = ? AND tenant_id = ?", p.ItemID, p.TenantID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load trigger item")
	}
	if !Triggers(&it) {
		return nil
	}
	botID, err := r.botFor(ctx, p.TenantID, p.ConversationID)
	if err != nil || botID == "" {
		return err
	}

	sys := identity.Subject{Type: identity.System, ID: "agent", Tenant: p.TenantID}
	g, err := r.open.Handle(ctx, sys, OpenParams{
		ConversationID: p.ConversationID, BotID: botID, TriggerItemID: p.ItemID,
	})
	if err != nil {
		return err
	}
	// 同一条触发已经处理过（完成、取消、失败），或者别的 worker 已经接手
	if g.State != Pending {
		return nil
	}

	chunks, err := r.generator.Generate(ctx, GenerateRequest{
		TenantID: p.TenantID, ConversationID: p.ConversationID, BotID: botID, TriggerItemID: p.ItemID,
	})
	if err != nil {
		return r.fail.Handle(ctx, sys, FailParams{GenerationID: g.ID, Reason: err.Error()})
	}
	var text strings.Builder
	var usage map[string]any
	for chunk := range chunks {
		if chunk.Err != nil {
			if errors.Is(chunk.Err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			return r.fail.Handle(ctx, sys, FailParams{GenerationID: g.ID, Reason: chunk.Err.Error()})
		}
		if chunk.Text != "" {
			if _, err := r.append.Handle(ctx, sys, AppendParams{GenerationID: g.ID, Delta: chunk.Text}); err != nil {
				if errors.Is(err, ErrFinished) || ctx.Err() != nil {
					return nil
				}
				return err
			}
			text.WriteString(chunk.Text)
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return r.fail.Handle(ctx, sys, FailParams{GenerationID: g.ID, Reason: "empty reply"})
	}
	_, err = r.finalize.Handle(ctx, sys, FinalizeParams{GenerationID: g.ID, Text: text.String(), Usage: usage})
	if errors.Is(err, ErrFinished) || errors.Is(err, ErrEmptyReply) {
		return nil
	}
	return err
}

// botFor 返回会话中第一个启用的机器人参与者；没有时返回空字符串。
func (r *Runner) botFor(ctx context.Context, tenantID, conversationID string) (string, error) {
	var id string
	err := r.db.NewRaw(`SELECT b.id::text
		FROM conversation_participants cp
		JOIN conversations c ON c.id = cp.conversation_id AND c.tenant_id = ?
		JOIN bots b ON b.id::text = cp.participant_id AND b.tenant_id = c.tenant_id
		WHERE cp.conversation_id = ? AND cp.participant_type = ? AND b.enabled = true
		ORDER BY b.created_at, b.id LIMIT 1`, tenantID, conversationID, conversation.BotActor).Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, errors.Wrap(err, "find conversation bot")
}
