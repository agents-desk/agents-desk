package conversation_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// multiConnector 是支持多会话的测试连接器，用来对照验证重开窗口只作用于单窗口渠道。
type multiConnector struct{}

func (multiConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text"}, MultipleConversations: true}
}

func (multiConnector) Send(_ context.Context, m channel.Outgoing) (string, bool, error) {
	return "multi-" + m.ItemID, true, nil
}

// TestReopenWindow 检查单窗口渠道按工作区的重开窗口决定重新打开还是新建会话，以及多会话渠道不受它限制。
func TestReopenWindow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		env := apptest.New(t)
		ctx := context.Background()
		tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
		agent := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}
		reg := channel.Registry{channel.Test: channel.TestConnector{}, channel.Widget: multiConnector{}}
		singleCh, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
		if err != nil {
			t.Fatal(err)
		}
		multiCh, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Widget, Name: "多会话渠道"})
		if err != nil {
			t.Fatal(err)
		}
		receive := conversation.NewReceive(env.DB, reg, conversation.Downstream{})
		closeConv := conversation.NewClose(env.DB, conversation.Downstream{})

		// 1. 单窗口渠道：默认 24 小时重开窗口内，客户再发消息重新打开原会话
		first, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: singleCh.ID, From: "rahul", Text: "first"})
		if err != nil || !first.Created || first.Reopened {
			t.Fatalf("新建会话：%v %+v", err, first)
		}
		if _, err := closeConv.Handle(ctx, agent, first.ConversationID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(23*time.Hour + 59*time.Minute)
		within, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: singleCh.ID, From: "rahul", Text: "within"})
		if err != nil || within.ConversationID != first.ConversationID || within.Created || !within.Reopened {
			t.Fatalf("窗口内应重开原会话：%v %+v", err, within)
		}

		// 2. 再次关闭，超过 24 小时后来消息：新建一个会话
		if _, err := closeConv.Handle(ctx, agent, first.ConversationID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(24*time.Hour + time.Minute)
		after, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: singleCh.ID, From: "rahul", Text: "after"})
		if err != nil || after.ConversationID == first.ConversationID || !after.Created || after.Reopened {
			t.Fatalf("超过重开窗口应新建会话：%v %+v", err, after)
		}

		// 3. 把重开窗口改为 0 小时：关闭后立刻来消息也新建会话
		zero := 0
		if _, err := tenant.NewRename(env.DB).Handle(ctx, agent, tenant.GeneralSettings{Name: tn.Name, ReopenWindowHours: &zero}); err != nil {
			t.Fatal(err)
		}
		if _, err := closeConv.Handle(ctx, agent, after.ConversationID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		immediate, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: singleCh.ID, From: "rahul", Text: "zero"})
		if err != nil || immediate.ConversationID == after.ConversationID || !immediate.Created {
			t.Fatalf("重开窗口为 0 应立刻新建会话：%v %+v", err, immediate)
		}

		// 4. 多会话渠道不受重开窗口限制：即使窗口为 0，客户指定已关闭会话 ID 依然重新打开
		mc, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: multiCh.ID, From: "u1", Text: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := closeConv.Handle(ctx, agent, mc.ConversationID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(48 * time.Hour)
		mcReopen, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: multiCh.ID, From: "u1", ConversationID: mc.ConversationID, Text: "again"})
		if err != nil || mcReopen.ConversationID != mc.ConversationID || !mcReopen.Reopened {
			t.Fatalf("多会话渠道指定会话应重开：%v %+v", err, mcReopen)
		}
	})
}

// TestClosuresAndRounds 检查每次关闭的轮次记录：首次回复时长、解决时长、客户评价、跨轮重置、Reply 自动重开、
// 稍后处理不重置轮次，以及跨工作区隔离。
func TestClosuresAndRounds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		env := apptest.New(t)
		ctx := context.Background()
		alpha, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "owner", OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		beta, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "beta", OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		system := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
		agent := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}
		betaAgent := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}

		tm, err := team.NewCreate(env.DB).Handle(ctx, system, team.Changes{Name: "一线客服", Assignment: team.Manual})
		if err != nil {
			t.Fatal(err)
		}
		ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "测试渠道", TeamID: &tm.ID})
		if err != nil {
			t.Fatal(err)
		}
		down := conversation.Downstream{}
		receive := conversation.NewReceive(env.DB, channel.DefaultRegistry(), down)
		reply := conversation.NewReply(env.DB, down)
		snooze := conversation.NewSnooze(env.DB, down)
		reopen := conversation.NewReopen(env.DB, down)
		closeConv := conversation.NewClose(env.DB, down)
		reqRating := conversation.NewRequestRating(env.DB, down)
		rate := conversation.NewRateByContact(env.DB, down)
		listRounds := conversation.NewListRounds(env.DB)

		// Round 1：打开（即产生进行中的第 1 轮）→ 10 分钟后回复（记录首次回复人与时间）→ 稍后处理再恢复（不重置轮次）→ 30 分钟关闭 → 请求评价 → 客户评 5 星
		res, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: ch.ID, From: "rahul", Text: "help"})
		if err != nil {
			t.Fatal(err)
		}
		convID := res.ConversationID
		var contactID string
		if err := env.DB.NewRaw("SELECT contact_id FROM conversations WHERE id = ?", convID).Scan(ctx, &contactID); err != nil {
			t.Fatal(err)
		}
		customer := identity.Subject{Type: contact.Type, ID: contactID, Tenant: alpha.ID}

		openRounds, err := listRounds.Handle(ctx, agent, convID)
		if err != nil || len(openRounds) != 1 || openRounds[0].ClosedAt != nil || openRounds[0].FirstReplyAt != nil {
			t.Fatalf("新建即应存在第 1 轮进行中记录：err=%v rounds=%+v", err, openRounds)
		}

		time.Sleep(10 * time.Minute)
		if _, err := reply.Handle(ctx, agent, convID, conversation.ReplyBody{Text: "on it"}); err != nil {
			t.Fatal(err)
		}
		afterReply, err := listRounds.Handle(ctx, agent, convID)
		if err != nil || len(afterReply) != 1 || afterReply[0].FirstReplyBy == nil || afterReply[0].FirstReplyBy.ID != alpha.OwnerID || afterReply[0].FirstReplySeconds == nil || *afterReply[0].FirstReplySeconds != 600 || afterReply[0].ClosedAt != nil {
			t.Fatalf("首响后应立刻记录首次回复人与时长：err=%v rounds=%+v", err, afterReply)
		}
		if _, err := snooze.Handle(ctx, agent, convID, time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Minute)
		if _, err := reopen.Handle(ctx, agent, convID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Minute)
		if _, err := closeConv.Handle(ctx, agent, convID); err != nil {
			t.Fatal(err)
		}
		if ok, err := reqRating.Handle(ctx, system, convID); err != nil || !ok {
			t.Fatalf("发出评价邀请：%v %v", ok, err)
		}
		if _, err := rate.Handle(ctx, customer, ch.ID, convID, 5, "great"); err != nil {
			t.Fatal(err)
		}

		// Round 2：手动重新打开 → 不回复，5 分钟后直接关闭（本轮 first_reply_seconds 为空）
		time.Sleep(time.Minute)
		if _, err := reopen.Handle(ctx, agent, convID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Minute)
		if _, err := closeConv.Handle(ctx, agent, convID); err != nil {
			t.Fatal(err)
		}

		// Round 3：成员对已关闭会话直接回复（自动重开并开启新一轮，首次回复时长为 0 秒）→ 2 分钟后关闭 → 评 4 星
		time.Sleep(time.Minute)
		if _, err := reply.Handle(ctx, agent, convID, conversation.ReplyBody{Text: "follow up"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Minute)
		if _, err := closeConv.Handle(ctx, agent, convID); err != nil {
			t.Fatal(err)
		}
		if ok, err := reqRating.Handle(ctx, system, convID); err != nil || !ok {
			t.Fatalf("第三轮评价邀请：%v %v", ok, err)
		}
		if _, err := rate.Handle(ctx, customer, ch.ID, convID, 4, "good"); err != nil {
			t.Fatal(err)
		}

		rounds, err := listRounds.Handle(ctx, agent, convID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rounds) != 3 {
			t.Fatalf("服务轮次数：%d，期望 3", len(rounds))
		}
		// 校验第 1 轮
		c1 := rounds[0]
		if c1.Round != 1 || c1.TeamID == nil || *c1.TeamID != tm.ID || c1.Assignee == nil || c1.Assignee.ID != alpha.OwnerID || c1.FirstReplyBy == nil || c1.FirstReplyBy.ID != alpha.OwnerID {
			t.Errorf("第 1 轮归属：%+v", c1)
		}
		if c1.FirstReplySeconds == nil || *c1.FirstReplySeconds != 600 || c1.ResolutionSeconds == nil || *c1.ResolutionSeconds != 1800 {
			t.Errorf("第 1 轮时长：first=%v res=%v", c1.FirstReplySeconds, c1.ResolutionSeconds)
		}
		if c1.Rating == nil || c1.Rating.Score != 5 || c1.Rating.Comment != "great" {
			t.Errorf("第 1 轮评价：%+v", c1.Rating)
		}
		// 校验第 2 轮：未回复，未评价
		c2 := rounds[1]
		if c2.Round != 2 || c2.FirstReplyAt != nil || c2.FirstReplySeconds != nil || c2.FirstReplyBy != nil || c2.ResolutionSeconds == nil || *c2.ResolutionSeconds != 300 || c2.Rating != nil {
			t.Errorf("第 2 轮：%+v", c2)
		}
		// 校验第 3 轮：Reply 自动重开并即时回复
		c3 := rounds[2]
		if c3.Round != 3 || c3.FirstReplySeconds == nil || *c3.FirstReplySeconds != 0 || c3.ResolutionSeconds == nil || *c3.ResolutionSeconds != 120 {
			t.Errorf("第 3 轮时长：%+v", c3)
		}
		if c3.Rating == nil || c3.Rating.Score != 4 || c3.Rating.Comment != "good" {
			t.Errorf("第 3 轮评价：%+v", c3.Rating)
		}

		// 其他工作区访问不到服务轮次记录
		if _, err := listRounds.Handle(ctx, betaAgent, convID); !errors.Is(err, conversation.ErrNotFound) {
			t.Errorf("跨工作区读服务轮次记录：%v", err)
		}

		// 停用团队的轮流分配跳过测试：停用后即使有在线成员也不自动分派
		rrTeam, err := team.NewCreate(env.DB).Handle(ctx, system, team.Changes{Name: "轮流组", Assignment: team.RoundRobin})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := team.NewPutMember(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, system, rrTeam.ID, alpha.OwnerID); err != nil {
			t.Fatal(err)
		}
		if _, err := member.NewSetMyStatus(env.DB).Handle(ctx, agent, member.StatusSetting{Status: member.Online}); err != nil {
			t.Fatal(err)
		}
		rrCh, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "轮流渠道", TeamID: &rrTeam.ID})
		if err != nil {
			t.Fatal(err)
		}
		// 建一个会话并放进轮流组，然后清空负责人并停用团队，对方再来消息时不会自动轮流分配
		rrConv, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: rrCh.ID, From: "c1", Text: "hi"})
		if err != nil {
			t.Fatal(err)
		}
		empty := ""
		if _, err := conversation.NewAssign(env.DB, down).Handle(ctx, system, rrConv.ConversationID, conversation.Assignment{AssigneeID: &empty}); err != nil {
			t.Fatal(err)
		}
		if _, err := team.NewDisable(env.DB).Handle(ctx, system, rrTeam.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: rrCh.ID, From: "c1", Text: "hi again"}); err != nil {
			t.Fatal(err)
		}
		got, err := conversation.NewGet(env.DB).Handle(ctx, agent, rrConv.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Assignee != nil {
			t.Errorf("停用团队不应触发轮流分配：%+v", got.Assignee)
		}
	})
}
