package agent_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue/queuetest"

	"agents-desk/internal/agent"
	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// fixture 是一个测试的工作区、测试渠道中的一条服务型会话和一个启用的机器人。
type fixture struct {
	env    *apptest.Env
	hub    *broadcast.Hub
	down   conversation.Downstream
	sys    identity.Subject
	owner  identity.Subject
	convID string
	itemID string // 客户发来的第一条消息
	botID  string
}

// setup 创建工作区 slug、测试渠道、一条客户发来的服务型会话和一个机器人参与者。
func setup(t *testing.T, env *apptest.Env, slug string) fixture {
	t.Helper()
	ctx := context.Background()
	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	owner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{
		Sinks: []conversation.Sink{{Topic: conversation.DeliverTopic, Accept: conversation.Delivers}},
		Hub:   hub, Roles: env.Roles,
	}
	rec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "rahul", FromName: "Rahul", Text: "Where is my order?",
	})
	if err != nil {
		t.Fatal(err)
	}
	botID := uuid.Must(uuid.NewV7()).String()
	at := time.Now().UTC()
	if _, err := env.DB.NewRaw(`INSERT INTO bots (id, created_at, updated_at, tenant_id, name, enabled, settings)
		VALUES (?, ?, ?, ?, ?, true, '{}')`, botID, at, at, tn.ID, "小助手").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO conversation_participants (id, created_at, updated_at, conversation_id, participant_type, participant_id)
		VALUES (gen_random_uuid(), ?, ?, ?, ?, ?)`, at, at, rec.ConversationID, conversation.BotActor, botID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return fixture{env: env, hub: hub, down: down, sys: sys, owner: owner, convID: rec.ConversationID, itemID: rec.Item.ID, botID: botID}
}

// readFrame 从订阅 sub 读取一条生成频道的事件。
func readFrame(t *testing.T, sub *broadcast.Subscription) agent.Frame {
	t.Helper()
	select {
	case msg := <-sub.C():
		var f agent.Frame
		if err := json.Unmarshal(msg.Data, &f); err != nil {
			t.Fatal(err)
		}
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("等待生成频道事件超时")
		return agent.Frame{}
	}
}

// TestGenerationLifecycle 验证生成的完整生命周期：同一条触发只创建一次；逐字产出只写 generations、不写时间线；
// 定稿在一个事务里追加一条机器人消息并交给投递；重复定稿幂等；结束后不能再写入或取消。
func TestGenerationLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, apptest.New(t), "alpha")

	open := agent.NewOpen(f.env.DB)
	g, err := open.Handle(ctx, f.sys, agent.OpenParams{ConversationID: f.convID, BotID: f.botID, TriggerItemID: f.itemID})
	if err != nil || g.State != agent.Pending {
		t.Fatalf("创建生成：%+v %v", g, err)
	}
	again, err := open.Handle(ctx, f.sys, agent.OpenParams{ConversationID: f.convID, BotID: f.botID, TriggerItemID: f.itemID})
	if err != nil || again.ID != g.ID {
		t.Fatalf("同一条触发应返回同一次生成：%+v %v", again, err)
	}
	if _, err := open.Handle(ctx, f.owner, agent.OpenParams{ConversationID: f.convID, BotID: f.botID, TriggerItemID: f.itemID}); !errors.Is(err, agent.ErrForbidden) {
		t.Fatalf("成员不能直接创建生成：%v", err)
	}

	sub := f.hub.Subscribe(agent.Channel(f.sys.Tenant, g.ID))
	defer sub.Close()
	var before int64
	if err := f.env.DB.NewRaw("SELECT last_seq FROM conversations WHERE id = ?", f.convID).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}

	appendChunk := agent.NewAppend(f.env.DB, f.hub)
	for _, d := range []string{"您的订单", "已经发货"} {
		if _, err := appendChunk.Handle(ctx, f.sys, agent.AppendParams{GenerationID: g.ID, Delta: d}); err != nil {
			t.Fatal(err)
		}
	}
	if fr := readFrame(t, sub); fr.Type != "delta" || fr.Delta != "您的订单" || fr.Checkpoint != 1 {
		t.Fatalf("第一段：%+v", fr)
	}
	if fr := readFrame(t, sub); fr.Type != "delta" || fr.Delta != "已经发货" || fr.Checkpoint != 2 {
		t.Fatalf("第二段：%+v", fr)
	}
	var mid int64
	if err := f.env.DB.NewRaw("SELECT last_seq FROM conversations WHERE id = ?", f.convID).Scan(ctx, &mid); err != nil {
		t.Fatal(err)
	}
	if mid != before {
		t.Fatalf("逐字产出不应写时间线：before=%d after=%d", before, mid)
	}

	// 页面重连时能读回进行中的生成和已产出的文字
	active, err := agent.NewList(f.env.DB).Handle(ctx, f.owner, f.convID)
	if err != nil || len(active) != 1 || active[0].Text != "您的订单已经发货" || active[0].State != agent.Streaming || active[0].Actor.Name != "小助手" {
		t.Fatalf("进行中的生成：%+v %v", active, err)
	}

	fin := agent.NewFinalize(f.env.DB, f.down, f.hub)
	item, err := fin.Handle(ctx, f.sys, agent.FinalizeParams{GenerationID: g.ID, Text: "您的订单已经发货了"})
	if err != nil {
		t.Fatal(err)
	}
	if item.ActorType != conversation.BotActor || item.ActorID != f.botID || item.Seq != before+1 || item.Content.Text != "您的订单已经发货了" || item.ChannelID == nil {
		t.Fatalf("定稿消息：%+v", item)
	}
	if fr := readFrame(t, sub); fr.Type != "done" || fr.ItemID != item.ID || fr.Seq != item.Seq {
		t.Fatalf("定稿事件：%+v", fr)
	}
	jobs := queuetest.Pending(t, f.env.DB, conversation.DeliverTopic)
	if len(jobs) != 1 || jobs[0].ItemID != item.ID {
		t.Fatalf("机器人的回复应交给投递：%+v", jobs)
	}
	c, err := conversation.NewGet(f.env.DB).Handle(ctx, f.owner, f.convID)
	if err != nil || c.WaitingSince != nil || c.FirstReplyAt == nil {
		t.Fatalf("机器人回复后会话不再等待：%+v %v", c, err)
	}

	// 重复定稿（任务重试）返回同一条消息，不再追加
	dup, err := fin.Handle(ctx, f.sys, agent.FinalizeParams{GenerationID: g.ID, Text: "另一段"})
	if err != nil || dup.ID != item.ID {
		t.Fatalf("重复定稿应幂等：%+v %v", dup, err)
	}
	if _, err := appendChunk.Handle(ctx, f.sys, agent.AppendParams{GenerationID: g.ID, Delta: "x"}); !errors.Is(err, agent.ErrFinished) {
		t.Fatalf("定稿后不能再写入：%v", err)
	}
	if err := agent.NewCancel(f.env.DB, f.hub).Handle(ctx, f.owner, g.ID); !errors.Is(err, agent.ErrFinished) {
		t.Fatalf("定稿后不能取消：%v", err)
	}
	if active, err := agent.NewList(f.env.DB).Handle(ctx, f.owner, f.convID); err != nil || len(active) != 0 {
		t.Fatalf("定稿后不再列为进行中：%+v %v", active, err)
	}
}

// TestGenerationCancelAndIsolation 验证成员取消生成后不写时间线、不能再定稿；其他工作区的成员看不到也取消不了；
// 超过有效期的生成由定时任务标记为失败。
func TestGenerationCancelAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")
	other := setup(t, env, "beta")

	g, err := agent.NewOpen(env.DB).Handle(ctx, f.sys, agent.OpenParams{ConversationID: f.convID, BotID: f.botID, TriggerItemID: f.itemID})
	if err != nil {
		t.Fatal(err)
	}
	cancel := agent.NewCancel(env.DB, f.hub)
	if err := cancel.Handle(ctx, other.owner, g.ID); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("其他工作区的成员不能取消：%v", err)
	}
	if _, err := agent.NewList(env.DB).Handle(ctx, other.owner, f.convID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("其他工作区的成员看不到生成：%v", err)
	}

	sub := f.hub.Subscribe(agent.Channel(f.sys.Tenant, g.ID))
	defer sub.Close()
	if err := cancel.Handle(ctx, f.owner, g.ID); err != nil {
		t.Fatal(err)
	}
	if fr := readFrame(t, sub); fr.Type != "canceled" || fr.State != agent.Canceled {
		t.Fatalf("取消事件：%+v", fr)
	}
	if err := cancel.Handle(ctx, f.owner, g.ID); err != nil {
		t.Fatalf("重复取消应幂等：%v", err)
	}
	if _, err := agent.NewFinalize(env.DB, f.down, f.hub).Handle(ctx, f.sys, agent.FinalizeParams{GenerationID: g.ID, Text: "迟到的回复"}); !errors.Is(err, agent.ErrFinished) {
		t.Fatalf("取消后不能定稿：%v", err)
	}
	if jobs := queuetest.Pending(t, env.DB, conversation.DeliverTopic); len(jobs) != 0 {
		t.Fatalf("取消的生成不应产生消息：%+v", jobs)
	}

	// 有效期已过的生成由定时任务标记为失败
	expired, err := agent.NewOpen(env.DB).Handle(ctx, other.sys, agent.OpenParams{
		ConversationID: other.convID, BotID: other.botID, TriggerItemID: other.itemID, TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := agent.ExpireGenerations(ctx, env.DB, time.Now().UTC().Add(time.Minute), 100)
	if err != nil || n != 1 {
		t.Fatalf("应标记一条超时生成：%d %v", n, err)
	}
	var state agent.State
	if err := env.DB.NewRaw("SELECT state FROM generations WHERE id = ?", expired.ID).Scan(ctx, &state); err != nil || state != agent.Failed {
		t.Fatalf("超时生成应为失败：%s %v", state, err)
	}
}

// fakeGenerator 按顺序产出给定的片段。
type fakeGenerator struct {
	chunks []agent.Chunk
	calls  int
}

// Generate 返回一个依次送出 chunks 的频道。
func (g *fakeGenerator) Generate(_ context.Context, _ agent.GenerateRequest) (<-chan agent.Chunk, error) {
	g.calls++
	out := make(chan agent.Chunk, len(g.chunks))
	for _, c := range g.chunks {
		out <- c
	}
	close(out)
	return out, nil
}

// TestRunnerGeneratesForMemberMessages 验证执行器只为成员的可见消息生成：客户的消息和机器人自己的消息不触发；
// 片段写进生成后定稿为一条机器人消息，同一条触发重复投递只生成一次；模型出错时生成标记为失败、不产生消息。
func TestRunnerGeneratesForMemberMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, apptest.New(t), "alpha")
	gen := &fakeGenerator{chunks: []agent.Chunk{{Text: "好的，"}, {Text: "马上处理"}, {Usage: map[string]any{"output_tokens": 6}}}}
	runner := agent.NewRunner(f.env.DB, f.down, f.hub, gen)

	// 客户的消息不触发
	if err := runner.Job(ctx, conversation.Appended{TenantID: f.sys.Tenant, ConversationID: f.convID, ItemID: f.itemID}); err != nil || gen.calls != 0 {
		t.Fatalf("客户的消息不应触发生成：calls=%d %v", gen.calls, err)
	}

	reply, err := conversation.NewReply(f.env.DB, f.down).Handle(ctx, f.owner, f.convID, conversation.ReplyBody{Text: "@小助手 帮我回复一下"})
	if err != nil {
		t.Fatal(err)
	}
	job := conversation.Appended{TenantID: f.sys.Tenant, ConversationID: f.convID, ItemID: reply.ID, Seq: reply.Seq}
	if err := runner.Job(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := runner.Job(ctx, job); err != nil || gen.calls != 1 {
		t.Fatalf("同一条触发重复投递只生成一次：calls=%d %v", gen.calls, err)
	}
	items, err := conversation.NewTimeline(f.env.DB).Handle(ctx, f.owner, f.convID, reply.Seq, 10)
	if err != nil || len(items) != 1 || items[0].Actor.Type != conversation.BotActor || items[0].Content.Text != "好的，马上处理" {
		t.Fatalf("应定稿为一条机器人消息：%+v %v", items, err)
	}
	// 机器人自己的消息不再触发生成
	if err := runner.Job(ctx, conversation.Appended{TenantID: f.sys.Tenant, ConversationID: f.convID, ItemID: items[0].ID}); err != nil || gen.calls != 1 {
		t.Fatalf("机器人的消息不应触发生成：calls=%d %v", gen.calls, err)
	}

	// 模型出错：生成标记为失败，不产生消息
	failing := agent.NewRunner(f.env.DB, f.down, f.hub, &fakeGenerator{chunks: []agent.Chunk{{Text: "半句"}, {Err: errors.New("model unavailable")}}})
	second, err := conversation.NewReply(f.env.DB, f.down).Handle(ctx, f.owner, f.convID, conversation.ReplyBody{Text: "再试一次"})
	if err != nil {
		t.Fatal(err)
	}
	if err := failing.Job(ctx, conversation.Appended{TenantID: f.sys.Tenant, ConversationID: f.convID, ItemID: second.ID, Seq: second.Seq}); err != nil {
		t.Fatal(err)
	}
	var state agent.State
	if err := f.env.DB.NewRaw("SELECT state FROM generations WHERE trigger_item_id = ?", second.ID).Scan(ctx, &state); err != nil || state != agent.Failed {
		t.Fatalf("模型出错时生成应为失败：%s %v", state, err)
	}
	after, err := conversation.NewTimeline(f.env.DB).Handle(ctx, f.owner, f.convID, second.Seq, 10)
	if err != nil || len(after) != 0 {
		t.Fatalf("失败的生成不应产生消息：%+v %v", after, err)
	}
}
