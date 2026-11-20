package conversation_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue/queuetest"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// fixture 是一个测试的工作区、测试渠道和调用者。
type fixture struct {
	env    *apptest.Env
	system identity.Subject // 工作区中的系统主体：连接器、命令
	agent  identity.Subject // 工作区中有坐席权限的成员
	ch     *channel.Channel
	down   conversation.Downstream
}

// setup 创建工作区 slug、其中的测试渠道和调用者。
func setup(t *testing.T, env *apptest.Env, slug string) fixture {
	t.Helper()
	ctx := context.Background()
	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	agent := identity.Subject{Type: member.Type, ID: "01900000-0000-7000-8000-0000000000a1", Tenant: tn.ID, Permissions: auth.Agent.Permissions}
	return fixture{env: env, system: system, agent: agent, ch: ch, down: conversation.Downstream{conversation.DeliverTopic}}
}

// receive 以系统的身份写入客户 from 发来的消息。
func (f fixture) receive(t *testing.T, from, text, externalID string) *conversation.Received {
	t.Helper()
	res, err := conversation.NewReceive(f.env.DB, channel.DefaultRegistry(), f.down).Handle(context.Background(), f.system, conversation.ReceiveParams{
		ChannelID: f.ch.ID, From: from, FromName: from, Text: text, ExternalID: externalID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// get 以坐席的身份读取会话 id。
func (f fixture) get(t *testing.T, id string) *conversation.ConversationDTO {
	t.Helper()
	c, err := conversation.NewGet(f.env.DB).Handle(context.Background(), f.agent, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestReceiveReplyDeliver 检查一次完整的往来：客户发消息新建会话和联系人，再发一条归入同一个会话，重复的消息只记录一次；
// 坐席回复后会话不再等待，回复交给连接器发出并记为已送达。
func TestReceiveReplyDeliver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")

	first := f.receive(t, "rahul", "mera order nahi aaya", "m1")
	c := f.get(t, first.ConversationID)
	if c.Status != conversation.Open || c.WaitingSince == nil || c.Contact == nil || c.Contact.Kind != contact.Lead || c.Contact.Name != "rahul" || first.Item.Seq != 1 {
		t.Fatalf("新会话：%+v，第一项：%+v", c, first.Item)
	}
	second := f.receive(t, "rahul", "kab tak aayega?", "m2")
	again := f.receive(t, "rahul", "kab tak aayega?", "m2")
	if second.ConversationID != first.ConversationID || second.Item.Seq != 2 || !again.Duplicate || again.Item.ID != second.Item.ID {
		t.Fatalf("同一个客户的第二条和重复的消息：%+v %+v", second, again)
	}

	reply, err := conversation.NewReply(env.DB, f.down).Handle(ctx, f.agent, c.ID, "您好，我帮您查一下")
	if err != nil {
		t.Fatal(err)
	}
	c = f.get(t, c.ID)
	if c.WaitingSince != nil || c.FirstReplyAt == nil || reply.Seq != 3 || reply.Actor.Type != conversation.MemberActor ||
		reply.Delivery == nil || *reply.Delivery != conversation.Sending {
		t.Fatalf("回复后：%+v，回复：%+v", c, reply)
	}
	// 每写入一项发布一个任务，重复的消息不发布
	jobs := queuetest.Pending(t, env.DB, conversation.DeliverTopic)
	if len(jobs) != 3 || jobs[2].ItemID != reply.ID {
		t.Fatalf("发布的任务：%+v", jobs)
	}
	// 发出：客户的消息跳过，回复记为已送达；重复的任务不再发出
	deliver := conversation.NewDeliver(env.DB, channel.DefaultRegistry())
	for _, job := range append(jobs, jobs[2]) {
		if err := deliver.Job(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, f.agent, c.ID, 2, 100)
	if err != nil || len(items) != 1 || items[0].Delivery == nil || *items[0].Delivery != conversation.Delivered {
		t.Fatalf("发出后的时间线：%+v %v", items, err)
	}
}

// TestReopen 检查已关闭的会话收到消息后重新打开，时间线中先记下重新打开的系统事件。
func TestReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")

	first := f.receive(t, "priya", "withdrawal pending", "")
	if _, err := env.DB.NewRaw("UPDATE conversations SET status = 'closed' WHERE id = ?", first.ConversationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	next := f.receive(t, "priya", "still pending", "")
	if next.ConversationID != first.ConversationID || next.Item.Seq != 3 {
		t.Fatalf("重新打开：%+v", next)
	}
	if c := f.get(t, first.ConversationID); c.Status != conversation.Open {
		t.Fatalf("状态：%s", c.Status)
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, f.agent, first.ConversationID, 1, 100)
	if err != nil || len(items) != 2 || items[0].Kind != conversation.Event || items[0].Content.Event != "conversation.reopened" ||
		items[0].Visibility != conversation.MembersOnly {
		t.Fatalf("时间线：%+v %v", items, err)
	}
}

// TestNoteAndPermissions 检查内部备注只有成员可见、会话仍在等待回复，以及没有权限的调用被拒绝。
func TestNoteAndPermissions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")

	id := f.receive(t, "anil", "refund kab aayega?", "").ConversationID
	note, err := conversation.NewAddNote(env.DB, f.down).Handle(ctx, f.agent, id, "@张三 这单要找财务")
	if err != nil || note.Kind != conversation.Note || note.Visibility != conversation.MembersOnly || note.Delivery != nil {
		t.Fatalf("内部备注：%+v %v", note, err)
	}
	if c := f.get(t, id); c.WaitingSince == nil {
		t.Fatal("写内部备注后会话仍应等待回复")
	}

	nobody := f.agent
	nobody.Permissions = nil
	if _, err := conversation.NewReply(env.DB, f.down).Handle(ctx, nobody, id, "hi"); !errors.Is(err, conversation.ErrForbidden) {
		t.Errorf("没有权限的回复：%v", err)
	}
	if _, err := conversation.NewGet(env.DB).Handle(ctx, nobody, id); !errors.Is(err, conversation.ErrForbidden) {
		t.Errorf("没有权限的读取：%v", err)
	}
	// 写入收到的消息只有系统主体可以调用
	_, err = conversation.NewReceive(env.DB, channel.DefaultRegistry(), f.down).Handle(ctx, f.agent, conversation.ReceiveParams{ChannelID: f.ch.ID, From: "x", Text: "x"})
	if !errors.Is(err, conversation.ErrForbidden) {
		t.Errorf("成员写入收到的消息：%v", err)
	}
}

// TestTenantIsolation 检查别的工作区读不到、回复不了这个工作区的会话，也用不了它的渠道。
func TestTenantIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha, beta := setup(t, env, "alpha"), setup(t, env, "beta")

	id := alpha.receive(t, "sneha", "can't log in", "").ConversationID
	if _, err := conversation.NewGet(env.DB).Handle(ctx, beta.agent, id); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("别的工作区读取：%v", err)
	}
	if _, err := conversation.NewReply(env.DB, beta.down).Handle(ctx, beta.agent, id, "hi"); !errors.Is(err, conversation.ErrNotFound) {
		t.Errorf("别的工作区回复：%v", err)
	}
	_, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), beta.down).Handle(ctx, beta.system, conversation.ReceiveParams{ChannelID: alpha.ch.ID, From: "x", Text: "x"})
	if !errors.Is(err, channel.ErrNotFound) {
		t.Errorf("用别的工作区的渠道：%v", err)
	}
	page, err := conversation.NewList(env.DB).Handle(ctx, beta.agent, &conversation.ListInput{})
	if err != nil || len(page.Items) != 0 {
		t.Errorf("别的工作区的会话列表：%+v %v", page, err)
	}
}

// TestConcurrentSeq 检查同一个会话并发写入时，时间线的序号连续、不重复。
func TestConcurrentSeq(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")

	id := f.receive(t, "meera", "first", "").ConversationID
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := conversation.NewAddNote(env.DB, f.down).Handle(ctx, f.agent, id, "note"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, f.agent, id, 0, 100)
	if err != nil || len(items) != 11 {
		t.Fatalf("时间线：%d %v", len(items), err)
	}
	for i, it := range items {
		if it.Seq != int64(i+1) {
			t.Fatalf("第 %d 项的序号是 %d", i+1, it.Seq)
		}
	}
	if c := f.get(t, id); c.LastSeq != 11 {
		t.Fatalf("最新序号：%d", c.LastSeq)
	}
}
