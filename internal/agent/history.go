package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
	"trpc.group/trpc-go/trpc-agent-go/model"

	"github.com/go-tenon/tenon/ai/chat"

	"agents-desk/internal/conversation"
)

// historyLimit 限制单次拼装给 AI 模型的历史时间线消息条数，控制上下文窗口与成本。
const historyLimit = 40

// historyStore 实现 chat.HistoryStore，将 timeline_items 与 agent_turns 映射为统一对话上下文。
type historyStore struct {
	db *bun.DB
}

func newHistoryStore(db *bun.DB) *historyStore {
	return &historyStore{db: db}
}

type orderedMsg struct {
	logicalSeq int64
	subOrder   int
	minorSeq   int64
	msg        model.Message
}

// Load 读取会话 c 的可见历史时间线与活跃生成的中间工具轮次，并给出当前最大序号作为 Cursor。
func (h *historyStore) Load(ctx context.Context, c chat.Conversation) (chat.History, error) {
	var convMeta struct {
		Kind         conversation.Kind         `bun:"kind"`
		HandlingMode conversation.HandlingMode `bun:"handling_mode"`
	}
	if err := h.db.NewSelect().Table("conversations").
		Column("kind", "handling_mode").
		Where("id = ? AND tenant_id = ?", c.ID, c.UserID).
		Scan(ctx, &convMeta); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return chat.History{}, conversation.ErrNotFound.New()
		}
		return chat.History{}, errors.Wrap(err, "load conversation metadata for history")
	}

	var activeGen struct {
		ID         string `bun:"id"`
		Mode       Mode   `bun:"mode"`
		TriggerSeq int64  `bun:"trigger_seq"`
	}
	err := h.db.NewSelect().Table("generations").
		Column("id", "mode", "trigger_seq").
		Where("tenant_id = ? AND conversation_id = ? AND state IN (?)",
			c.UserID, c.ID, bun.In([]State{Pending, Streaming, WaitingApproval})).
		OrderExpr("created_at DESC").Limit(1).
		Scan(ctx, &activeGen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return chat.History{}, errors.Wrap(err, "load active generation for history")
	}

	allowInternalNotes := convMeta.Kind == conversation.Chat || activeGen.Mode == ModeDraft

	q := h.db.NewSelect().TableExpr("timeline_items AS t").
		Column("t.seq", "t.after_seq", "t.kind", "t.actor_type", "t.visibility").
		ColumnExpr("COALESCE(rev.text, t.content->>'text', '') AS text").
		Join(`LEFT JOIN LATERAL (
			SELECT mr.text FROM message_revisions mr
			WHERE mr.item_id = t.id ORDER BY mr.revision DESC LIMIT 1
		) rev ON true`).
		Where("t.tenant_id = ? AND t.conversation_id = ? AND t.actor_type <> ?", c.UserID, c.ID, conversation.SystemActor).
		Where("NOT EXISTS (SELECT 1 FROM message_recalls rc WHERE rc.item_id = t.id)").
		Where("COALESCE(rev.text, t.content->>'text', '') <> ''").
		OrderExpr("t.seq DESC").Limit(historyLimit)

	if allowInternalNotes {
		q = q.Where("t.kind IN (?) AND t.visibility IN (?)",
			bun.In([]conversation.ItemKind{conversation.Message, conversation.Note}),
			bun.In([]conversation.Visibility{conversation.Everyone, conversation.MembersOnly}))
	} else {
		// 对外自动接待严格隔离：仅读取对所有人可见的普通消息，内部备注与仅成员可见项绝不进入模型上下文。
		q = q.Where("t.kind = ? AND t.visibility = ?", conversation.Message, conversation.Everyone)
	}

	var rows []struct {
		Seq        int64                   `bun:"seq"`
		AfterSeq   *int64                  `bun:"after_seq"`
		Kind       conversation.ItemKind   `bun:"kind"`
		ActorType  conversation.ActorType  `bun:"actor_type"`
		Visibility conversation.Visibility `bun:"visibility"`
		Text       string                  `bun:"text"`
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return chat.History{}, errors.Wrap(err, "load timeline history")
	}

	var items []orderedMsg
	var maxSeq int64
	for _, r := range rows {
		if r.Seq > maxSeq {
			maxSeq = r.Seq
		}
		text := strings.TrimSpace(r.Text)
		if text == "" {
			continue
		}
		logical := r.Seq
		if r.AfterSeq != nil && *r.AfterSeq > 0 {
			logical = *r.AfterSeq
		}
		var msg model.Message
		switch {
		case r.Kind == conversation.Note || r.Visibility == conversation.MembersOnly:
			msg = model.Message{Role: model.RoleAssistant, Content: "[Internal Note]: " + text}
		case r.ActorType == conversation.BotActor:
			msg = model.NewAssistantMessage(text)
		case r.ActorType == conversation.MemberActor:
			if convMeta.Kind == conversation.Chat {
				msg = model.NewUserMessage(text)
			} else {
				msg = model.NewAssistantMessage("[Human Agent]: " + text)
			}
		default:
			msg = model.NewUserMessage(text)
		}
		items = append(items, orderedMsg{logicalSeq: logical, subOrder: 0, minorSeq: r.Seq, msg: msg})
	}

	// 读取尚未归档或属于当前活跃生成的中间工具调用与审批结果轮次。
	var turns []AgentTurn
	if err := h.db.NewSelect().Model(&turns).
		Join("LEFT JOIN generations AS g ON g.id = agent_turn.generation_id").
		Where("agent_turn.tenant_id = ? AND agent_turn.conversation_id = ?", c.UserID, c.ID).
		Where("agent_turn.generation_id IS NULL OR g.state IN (?)", bun.In([]State{Pending, Streaming, WaitingApproval})).
		OrderExpr("agent_turn.after_seq ASC, agent_turn.turn_order ASC, agent_turn.created_at ASC").
		Scan(ctx); err != nil {
		return chat.History{}, errors.Wrap(err, "load agent turns")
	}
	for _, turn := range turns {
		var msg model.Message
		if err := json.Unmarshal(turn.Message, &msg); err != nil {
			return chat.History{}, errors.Wrap(err, "unmarshal agent turn message")
		}
		items = append(items, orderedMsg{
			logicalSeq: turn.AfterSeq,
			subOrder:   1,
			minorSeq:   int64(turn.TurnOrder),
			msg:        msg,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].logicalSeq != items[j].logicalSeq {
			return items[i].logicalSeq < items[j].logicalSeq
		}
		if items[i].subOrder != items[j].subOrder {
			return items[i].subOrder < items[j].subOrder
		}
		return items[i].minorSeq < items[j].minorSeq
	})

	msgs := make([]model.Message, 0, len(items))
	for _, it := range items {
		msgs = append(msgs, it.msg)
	}
	return chat.History{
		Messages: msgs,
		Cursor:   strconv.FormatInt(maxSeq, 10),
	}, nil
}

// Save 保存 tenon/ai/chat 在一轮运行中产生的中间 assistant（含 tool_calls）与 tool 结果消息。
// 最终定稿消息由 Finalize 写入 timeline_items（reply 模式）或保留在 generations（draft 模式）。
func (h *historyStore) Save(ctx context.Context, c chat.Conversation, e chat.Entry) error {
	if e.Message.Role != model.RoleTool && e.Message.Role != model.RoleAssistant {
		return nil
	}
	var activeGen struct {
		ID         string `bun:"id"`
		TriggerSeq int64  `bun:"trigger_seq"`
	}
	err := h.db.NewSelect().Table("generations").
		Column("id", "trigger_seq").
		Where("tenant_id = ? AND conversation_id = ? AND state IN (?)",
			c.UserID, c.ID, bun.In([]State{Pending, Streaming, WaitingApproval})).
		OrderExpr("created_at DESC").Limit(1).
		Scan(ctx, &activeGen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return errors.Wrap(err, "load active generation for turn save")
	}
	var genPtr *string
	if activeGen.ID != "" {
		genPtr = &activeGen.ID
	}

	afterSeq := activeGen.TriggerSeq
	if strings.TrimSpace(e.After) != "" {
		if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(e.After), 10, 64); parseErr == nil && parsed > 0 {
			afterSeq = parsed
		}
	}

	var maxOrder int
	if err := h.db.NewRaw(`SELECT COALESCE(MAX(turn_order), 0) FROM agent_turns WHERE tenant_id = ? AND conversation_id = ? AND after_seq = ?`,
		c.UserID, c.ID, afterSeq).Scan(ctx, &maxOrder); err != nil {
		return errors.Wrap(err, "load max turn order")
	}

	payload, err := json.Marshal(e.Message)
	if err != nil {
		return errors.Wrap(err, "marshal agent turn message")
	}
	at := now()
	turn := &AgentTurn{
		ID:             newID(),
		TenantID:       c.UserID,
		ConversationID: c.ID,
		GenerationID:   genPtr,
		RunID:          e.RunID,
		AfterSeq:       afterSeq,
		TurnOrder:      maxOrder + 1,
		Role:           string(e.Message.Role),
		Message:        payload,
	}
	turn.CreatedAt, turn.UpdatedAt = at, at
	_, err = h.db.NewInsert().Model(turn).Exec(ctx)
	return errors.Wrap(err, "insert agent turn")
}
