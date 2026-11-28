package agent_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"

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

// fakeChatModel 按顺序执行给定脚本，用于测试 tenon/ai/chat 运行时服务。
type fakeChatModel struct {
	fn func(ctx context.Context, req *model.Request, call int) ([]*model.Response, error)
}

func (m *fakeChatModel) Info() model.Info { return model.Info{Name: "fake-chat"} }

func (m *fakeChatModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	// 用简单计数器交由测试函数控制每次响应
	rsps, err := m.fn(ctx, req, 0)
	if err != nil {
		return nil, err
	}
	ch := make(chan *model.Response, len(rsps))
	for _, r := range rsps {
		ch <- r
	}
	close(ch)
	return ch, nil
}

// TestRunnerGeneratesForMemberMessages 验证内部聊天中成员消息会触发 Bot 生成，客户或服务型会话中的成员回复不会回环触发 AI；
// 片段写进生成后定稿为一条机器人消息，同一触发重复投递只生成一次；模型出错时生成标记为失败、不产生消息。
func TestRunnerGeneratesForMemberMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t, apptest.New(t), "alpha")

	// 创建一个启用机器人参与的内部单聊，验证成员消息触发 AI 回复
	var modelCalls int
	chatConv, err := conversation.NewCreateDirectChat(f.env.DB, f.down).Handle(ctx, f.owner, conversation.CreateDirectChatParams{
		ParticipantType: conversation.BotActor,
		ParticipantID:   f.botID,
	})
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeChatModel{fn: func(_ context.Context, _ *model.Request, _ int) ([]*model.Response, error) {
		modelCalls++
		return []*model.Response{
			{Done: false, IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: "好的，"}}}},
			{Done: false, IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: "马上处理"}}}},
			{Done: true, IsPartial: false, Usage: &model.Usage{PromptTokens: 10, CompletionTokens: 6}, Choices: []model.Choice{{Message: model.NewAssistantMessage("好的，马上处理")}}},
		}, nil
	}}
	svc := agent.NewService(f.env.DB, f.down, f.hub, nil, nil, nil, agent.WithModelOverride(m))
	t.Cleanup(func() { _ = svc.Close() })
	runner := svc.Runner()

	// 服务型会话处于默认 human 模式，客户消息和人工回复都不应触发 AI
	if err := runner.Job(ctx, conversation.Appended{TenantID: f.sys.Tenant, ConversationID: f.convID, ItemID: f.itemID}); err != nil || modelCalls != 0 {
		t.Fatalf("human 模式客户消息不应触发生成：calls=%d %v", modelCalls, err)
	}

	reply, err := conversation.NewSendChatMessage(f.env.DB, f.down).Handle(ctx, f.owner, chatConv.ID, conversation.SendChatMessageBody{Text: "@小助手 帮我查一下"})
	if err != nil {
		t.Fatal(err)
	}
	job := conversation.Appended{TenantID: f.sys.Tenant, ConversationID: chatConv.ID, ItemID: reply.ID, Seq: reply.Seq}
	if err := runner.Job(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := runner.Job(ctx, job); err != nil || modelCalls != 1 {
		t.Fatalf("同一条触发重复投递只生成一次：calls=%d %v", modelCalls, err)
	}
	items, err := conversation.NewTimeline(f.env.DB).Handle(ctx, f.owner, chatConv.ID, reply.Seq, 10)
	if err != nil || len(items) != 1 || items[0].Actor.Type != conversation.BotActor || items[0].Content.Text != "好的，马上处理" {
		t.Fatalf("应定稿为一条机器人消息：%+v %v", items, err)
	}
	// 机器人自己的消息不再触发生成
	if err := runner.Job(ctx, conversation.Appended{TenantID: f.sys.Tenant, ConversationID: chatConv.ID, ItemID: items[0].ID}); err != nil || modelCalls != 1 {
		t.Fatalf("机器人的消息不应触发生成：calls=%d %v", modelCalls, err)
	}

	// 模型出错：生成标记为失败，不产生消息
	failSvc := agent.NewService(f.env.DB, f.down, f.hub, nil, nil, nil, agent.WithModelOverride(&fakeChatModel{
		fn: func(_ context.Context, _ *model.Request, _ int) ([]*model.Response, error) {
			return []*model.Response{
				{Done: false, IsPartial: true, Choices: []model.Choice{{Delta: model.Message{Role: model.RoleAssistant, Content: "半句"}}}},
				{Done: true, Error: &model.ResponseError{Message: "model unavailable"}},
			}, nil
		},
	}))
	t.Cleanup(func() { _ = failSvc.Close() })
	second, err := conversation.NewSendChatMessage(f.env.DB, f.down).Handle(ctx, f.owner, chatConv.ID, conversation.SendChatMessageBody{Text: "再试一次"})
	if err != nil {
		t.Fatal(err)
	}
	if err := failSvc.Runner().Job(ctx, conversation.Appended{TenantID: f.sys.Tenant, ConversationID: chatConv.ID, ItemID: second.ID, Seq: second.Seq}); err != nil {
		t.Fatal(err)
	}
	var state agent.State
	if err := f.env.DB.NewRaw("SELECT state FROM generations WHERE trigger_item_id = ?", second.ID).Scan(ctx, &state); err != nil || state != agent.Failed {
		t.Fatalf("模型出错时生成应为失败：%s %v", state, err)
	}
	after, err := conversation.NewTimeline(f.env.DB).Handle(ctx, f.owner, chatConv.ID, second.Seq, 10)
	if err != nil || len(after) != 0 {
		t.Fatalf("失败的生成不应产生消息：%+v %v", after, err)
	}
}
