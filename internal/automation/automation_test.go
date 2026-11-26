package automation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/automation"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/hours"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
	"agents-desk/internal/widget"
)

// fixture 是一个有主管理员、坐席和网页聊天插件的工作区，用来测试内置自动化规则。
type fixture struct {
	env    *apptest.Env
	tenant *tenant.Tenant
	owner  identity.Subject
	agent  identity.Subject
	system identity.Subject
	widget *widget.WidgetDTO
	secret string
	hub    *broadcast.Hub
	reg    channel.Registry
	down   conversation.Downstream
	runner *automation.Runner
}

func setup(t *testing.T, env *apptest.Env, slug string) *fixture {
	t.Helper()
	ctx := context.Background()
	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: slug, Slug: slug, OwnerEmail: slug + "-owner@example.com", OwnerName: "主管理员", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, sys, member.AddParams{
		Email: slug + "-agent@example.com", Name: "坐席", Password: "password123", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	w, err := widget.NewCreate(env.DB, env.Crypt).Handle(ctx, sys, widget.Changes{Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := widget.NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, sys, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	reg := channel.DefaultRegistry()
	reg[channel.Widget] = widget.NewConnector(hub)
	down := conversation.Downstream{Hub: hub, Roles: env.Roles, ContactPush: widget.Push(hub)}
	runner := automation.NewRunner(env.DB, lock.New(pglock.New(env.DB)), down)
	return &fixture{
		env:    env,
		tenant: tn,
		owner:  identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)},
		agent:  identity.Subject{Type: member.Type, ID: ag.ID, Tenant: tn.ID, Permissions: auth.WithBase(nil)},
		system: sys,
		widget: w,
		secret: inst.IdentitySecret,
		hub:    hub,
		reg:    reg,
		down:   down,
		runner: runner,
	}
}

// runItem 触发自动化引擎处理会话 convID 中的时间线项 itemID。
func (f *fixture) runItem(t *testing.T, convID, itemID string, seq int64) {
	t.Helper()
	err := f.runner.Handle(context.Background(), conversation.Appended{
		TenantID: f.tenant.ID, ConversationID: convID, ItemID: itemID, Seq: seq,
	})
	if err != nil {
		t.Fatalf("run automation: %v", err)
	}
}

// TestEvaluateCondition 检查条件 AST：空条件、All、Any 和 eq/neq/empty/not_empty/in 操作符。
func TestEvaluateCondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "eval")

	// 发一条消息产生线索联系人与会话
	recv := conversation.NewReceive(env.DB, f.reg, f.down)
	got, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:v1", Text: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	var conv conversation.Conversation
	if err := env.DB.NewSelect().Model(&conv).Where("id = ?", got.ConversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	fact := automation.NewFact(env.DB, f.tenant.ID, &conv, time.Now())

	cases := []struct {
		name string
		cond automation.Condition
		want bool
	}{
		{"空条件恒为真", automation.Condition{}, true},
		{"线索且邮箱为空", automation.Condition{All: []automation.Condition{
			{Field: "contact.kind", Op: "eq", Value: "lead"},
			{Field: "contact.email", Op: "empty"},
		}}, true},
		{"用户不匹配线索", automation.Condition{Field: "contact.kind", Op: "eq", Value: "user"}, false},
		{"渠道种类 in", automation.Condition{Field: "channel.kind", Op: "in", Value: []any{"test", "widget"}}, true},
		{"Any 满足其一", automation.Condition{Any: []automation.Condition{
			{Field: "contact.email", Op: "not_empty"},
			{Field: "channel.kind", Op: "neq", Value: "test"},
		}}, true},
	}
	for _, tc := range cases {
		ok, err := automation.Evaluate(ctx, tc.cond, fact)
		if err != nil || ok != tc.want {
			t.Errorf("%s: got (%v, %v), want %v", tc.name, ok, err, tc.want)
		}
	}
}

// TestFirstMessageReplyTimeAndLeadContact 检查首条消息触发：
//   - 工作时间内告知回复时间，线索同时收到请留邮箱卡片，且严格按 position 顺序；
//   - 不影响 WaitingSince、FirstReplyAt，不污染收件箱预览与坐席未读；
//   - 重复投递与后续消息不会重复触发；
//   - 已登录用户和已有邮箱的线索不会收到请留邮箱卡片；
//   - 非工作时间告知下次上班时间；全年休息时降级为通用非工作时间提示。
func TestFirstMessageReplyTimeAndLeadContact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "first")

	// 设置工作时间：上海时间全天 00:00–24:00（确保当前一定在工作时间内），回复时间“几分钟内”
	allDay := []hours.Interval{{Start: "00:00", End: "24:00"}}
	saveHours := hours.NewSave(env.DB)
	if _, err := saveHours.SaveSchedule(ctx, f.owner, hours.Schedule{
		TimeZone: "Asia/Shanghai",
		Week: hours.Week{
			Monday: allDay, Tuesday: allDay, Wednesday: allDay, Thursday: allDay,
			Friday: allDay, Saturday: allDay, Sunday: allDay,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := saveHours.SaveReplyTime(ctx, f.owner, hours.FewMinutes); err != nil {
		t.Fatal(err)
	}

	recv := conversation.NewReceive(env.DB, f.reg, f.down)
	// 1. 线索发第一条消息
	first, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:lead-1", Text: "请问发货要多久？",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.runItem(t, first.ConversationID, first.Item.ID, first.Item.Seq)
	// 重复投递同一条消息：幂等，不再写入
	f.runItem(t, first.ConversationID, first.Item.ID, first.Item.Seq)

	// 线索发第二条消息：不是首条消息，不触发
	second, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:lead-1", Text: "有人在吗？",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.runItem(t, second.ConversationID, second.Item.ID, second.Item.Seq)

	items, err := conversation.NewTimeline(env.DB).Handle(ctx, f.owner, first.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sysMsgs []conversation.ItemDTO
	for _, it := range items {
		if it.Kind == conversation.Message && it.Actor.Type == conversation.SystemActor {
			sysMsgs = append(sysMsgs, it)
		}
	}
	if len(sysMsgs) != 2 {
		t.Fatalf("系统消息条数 = %d，期望 2：%+v", len(sysMsgs), sysMsgs)
	}
	if sysMsgs[0].Content.Prompt == nil || sysMsgs[0].Content.Prompt.Kind != "reply_time_open" || sysMsgs[0].Content.Prompt.Params["reply_time"] != "minutes" {
		t.Fatalf("第 1 条系统消息应是告知回复时间：%+v", sysMsgs[0].Content)
	}
	if sysMsgs[1].Content.Prompt == nil || sysMsgs[1].Content.Prompt.Kind != "collect_contact" {
		t.Fatalf("第 2 条系统消息应是请留联系方式：%+v", sysMsgs[1].Content)
	}

	// 验证不污染 WaitingSince、FirstReplyAt 和收件箱预览
	convDTO, err := conversation.NewGet(env.DB).Handle(ctx, f.owner, first.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if convDTO.WaitingSince == nil || convDTO.FirstReplyAt != nil {
		t.Fatalf("自动消息不应清空 WaitingSince 或设置 FirstReplyAt：waiting=%v firstReply=%v", convDTO.WaitingSince, convDTO.FirstReplyAt)
	}
	if convDTO.Preview != "有人在吗？" {
		t.Fatalf("坐席预览应显示客户的真实消息，实际为 %q", convDTO.Preview)
	}

	// 坐席读到客户第二条消息后，即使后续再来系统消息也不应把会话标回未读
	if err := conversation.NewMarkRead(env.DB, f.hub).Handle(ctx, f.owner, first.ConversationID, second.Item.Seq); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewSendSystemMessage(env.DB, f.down).Handle(ctx, f.system, first.ConversationID, "系统提示", nil); err != nil {
		t.Fatal(err)
	}
	afterSys, err := conversation.NewGet(env.DB).Handle(ctx, f.owner, first.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if afterSys.Unread {
		t.Fatalf("系统自动消息不应将已读会话重新标记为坐席未读")
	}

	// 2. 已登录用户发第一条消息：只告知回复时间，不请留邮箱
	userContact, err := contact.ResolveUser(ctx, env.DB, f.tenant.ID, f.widget.ID, "user:u-1", "u-1", contact.Profile{Name: "Alice", Email: "alice@example.com"})
	if err != nil || userContact.Kind != contact.User {
		t.Fatalf("创建用户：%+v %v", userContact, err)
	}
	userFirst, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "user:u-1", Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.runItem(t, userFirst.ConversationID, userFirst.Item.ID, userFirst.Item.Seq)
	userItems, err := conversation.NewTimeline(env.DB).Handle(ctx, f.owner, userFirst.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var userSys []conversation.ItemDTO
	for _, it := range userItems {
		if it.Kind == conversation.Message && it.Actor.Type == conversation.SystemActor {
			userSys = append(userSys, it)
		}
	}
	if len(userSys) != 1 || userSys[0].Content.Prompt == nil || userSys[0].Content.Prompt.Kind != "reply_time_open" {
		t.Fatalf("已登录用户应只收到回复时间提示：%+v", userSys)
	}

	// 3. 团队收件箱全年休息：降级为通用非工作时间提示（不带 next_open_at）
	offWeek := hours.Week{
		Monday: []hours.Interval{}, Tuesday: []hours.Interval{}, Wednesday: []hours.Interval{},
		Thursday: []hours.Interval{}, Friday: []hours.Interval{}, Saturday: []hours.Interval{}, Sunday: []hours.Interval{},
	}
	offTeam, err := team.NewCreate(env.DB).Handle(ctx, f.owner, team.Changes{
		Name: "离线组", Assignment: team.Manual,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := team.NewSetBusinessHours(env.DB).Handle(ctx, f.owner, offTeam.ID, &hours.Schedule{TimeZone: "Asia/Shanghai", Week: offWeek}); err != nil {
		t.Fatal(err)
	}
	offCh, err := channel.NewCreate(env.DB).Handle(ctx, f.system, channel.CreateParams{
		Kind: channel.Test, Name: "离线测试渠道", TeamID: &offTeam.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	offMsg, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: offCh.ID, From: "c-off", Text: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.runItem(t, offMsg.ConversationID, offMsg.Item.ID, offMsg.Item.Seq)
	offItems, err := conversation.NewTimeline(env.DB).Handle(ctx, f.owner, offMsg.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var offReply *conversation.Prompt
	for _, it := range offItems {
		if it.Kind == conversation.Message && it.Actor.Type == conversation.SystemActor && it.Content.Prompt != nil && it.Content.Prompt.Kind == "reply_time_closed" {
			offReply = it.Content.Prompt
		}
	}
	if offReply == nil || offReply.Params["next_open_at"] != nil {
		t.Fatalf("全年休息应降级为不带 next_open_at 的 reply_time_closed：%+v", offReply)
	}

	// 4. 非工作时间（特殊日期把今天设为休息，明天正常上班）：返回带 next_open_at 的 reply_time_closed
	loc, _ := time.LoadLocation("Asia/Shanghai")
	today := time.Now().In(loc).Format(time.DateOnly)
	if _, err := saveHours.SaveSpecialDates(ctx, f.owner, []hours.SpecialDate{{Date: today, Name: "放假", Intervals: []hours.Interval{}}}); err != nil {
		t.Fatal(err)
	}
	closedMsg, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:lead-holiday", Text: "节假日有人值班吗？",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.runItem(t, closedMsg.ConversationID, closedMsg.Item.ID, closedMsg.Item.Seq)
	closedItems, err := conversation.NewTimeline(env.DB).Handle(ctx, f.owner, closedMsg.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var nextOpenPrompt *conversation.Prompt
	for _, it := range closedItems {
		if it.Kind == conversation.Message && it.Actor.Type == conversation.SystemActor && it.Content.Prompt != nil && it.Content.Prompt.Kind == "reply_time_closed" {
			nextOpenPrompt = it.Content.Prompt
		}
	}
	if nextOpenPrompt == nil || nextOpenPrompt.Params["next_open_at"] == nil || nextOpenPrompt.Params["time_zone"] != "Asia/Shanghai" {
		t.Fatalf("非工作时间应返回带 next_open_at 和 time_zone 的 reply_time_closed：%+v", nextOpenPrompt)
	}
}

// TestCloseRequestRatingAndToggle 检查关闭后请客户评价：
//   - 默认开启时，关闭事件触发 RequestRating，客户可评价；
//   - 重开后再次关闭会再次触发，允许再次评价；
//   - 管理员关闭该规则后，关闭会话不再邀请评价，客户调用评价返回 ErrRatingNotRequested。
func TestCloseRequestRatingAndToggle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "rating")

	recv := conversation.NewReceive(env.DB, f.reg, f.down)
	msg, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:v-rate", Text: "帮我查一下",
	})
	if err != nil {
		t.Fatal(err)
	}
	var conv conversation.Conversation
	if err := env.DB.NewSelect().Model(&conv).Where("id = ?", msg.ConversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	visitor := identity.Subject{Type: contact.Type, ID: *conv.ContactID, Tenant: f.tenant.ID}

	closeAndRun := func() {
		t.Helper()
		if _, err := conversation.NewClose(env.DB, f.down).Handle(ctx, f.owner, msg.ConversationID); err != nil {
			t.Fatal(err)
		}
		items, err := conversation.NewTimeline(env.DB).Handle(ctx, f.owner, msg.ConversationID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		last := items[len(items)-1]
		if last.Content.Event != "conversation.closed" {
			t.Fatalf("最后一项应是关闭事件：%+v", last)
		}
		f.runItem(t, msg.ConversationID, last.ID, last.Seq)
		// 重复投递同一关闭事件幂等
		f.runItem(t, msg.ConversationID, last.ID, last.Seq)
	}

	// 第 1 次关闭：规则开启 → 发出邀请 → 可以评价
	closeAndRun()
	got, err := conversation.NewGetForContact(env.DB).Handle(ctx, visitor, f.widget.ID, msg.ConversationID)
	if err != nil || !got.RatingRequested {
		t.Fatalf("第 1 次关闭后应请求评价：%+v %v", got, err)
	}
	if _, err := conversation.NewRateByContact(env.DB, f.down).Handle(ctx, visitor, f.widget.ID, msg.ConversationID, 5, "很好"); err != nil {
		t.Fatalf("第 1 次评价失败：%v", err)
	}

	// 客户回复重开 → 再次关闭 → 第 2 次触发评价邀请
	if _, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:v-rate", Text: "还有个问题",
	}); err != nil {
		t.Fatal(err)
	}
	closeAndRun()
	got2, err := conversation.NewGetForContact(env.DB).Handle(ctx, visitor, f.widget.ID, msg.ConversationID)
	if err != nil || !got2.RatingRequested || got2.Rating != nil {
		t.Fatalf("第 2 次关闭后应重新请求评价：%+v %v", got2, err)
	}

	// 管理员停用“关闭后请客户评价”规则
	rules, err := automation.NewList(env.DB).Handle(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	var ratingRuleID string
	for _, r := range rules {
		if r.BuiltinKey == automation.BuiltinRequestRating {
			ratingRuleID = r.ID
		}
	}
	if ratingRuleID == "" {
		t.Fatal("未找到内置的 request_rating 规则")
	}
	if _, err := automation.NewUpdate(env.DB).Handle(ctx, f.owner, ratingRuleID, false); err != nil {
		t.Fatal(err)
	}

	// 再次重开并关闭：规则已停用 → 不请求评价，调用评价报错
	if _, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:v-rate", Text: "第三个问题",
	}); err != nil {
		t.Fatal(err)
	}
	closeAndRun()
	got3, err := conversation.NewGetForContact(env.DB).Handle(ctx, visitor, f.widget.ID, msg.ConversationID)
	if err != nil || got3.RatingRequested {
		t.Fatalf("停用后不应请求评价：%+v %v", got3, err)
	}
	if _, err := conversation.NewRateByContact(env.DB, f.down).Handle(ctx, visitor, f.widget.ID, msg.ConversationID, 4, ""); !errors.Is(err, conversation.ErrRatingNotRequested) {
		t.Fatalf("停用后评价应返回 ErrRatingNotRequested，实际 %v", err)
	}
}

// TestLeadLeaveEmail 检查线索在聊天插件中留下邮箱：
//   - 线索提交邮箱后保存为小写，在会话中写入仅成员可见事件并幂等；
//   - 留下邮箱后再发起新会话不会再触发请留联系方式；
//   - 已登录用户不能通过访客接口改邮箱（返回 contact.ErrNotLead）。
func TestLeadLeaveEmail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "email")

	recv := conversation.NewReceive(env.DB, f.reg, f.down)
	msg, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:lead-mail", Text: "我想咨询报价",
	})
	if err != nil {
		t.Fatal(err)
	}
	var conv conversation.Conversation
	if err := env.DB.NewSelect().Model(&conv).Where("id = ?", msg.ConversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	leadSub := identity.Subject{Type: contact.Type, ID: *conv.ContactID, Tenant: f.tenant.ID}

	leave := widget.NewLeaveEmail(env.DB, f.down)
	summary, err := leave.Handle(ctx, leadSub, f.widget.ID, widget.ContactEmailBody{
		Email: "  Lead@Example.COM ", ConversationID: msg.ConversationID,
	})
	if err != nil || summary.Email != "lead@example.com" {
		t.Fatalf("留邮箱：%+v %v", summary, err)
	}
	// 重复提交相同邮箱：不再追加重复事件
	if _, err := leave.Handle(ctx, leadSub, f.widget.ID, widget.ContactEmailBody{
		Email: "lead@example.com", ConversationID: msg.ConversationID,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, f.owner, msg.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	emailEvents := 0
	for _, it := range items {
		if it.Kind == conversation.Event && it.Content.Event == "contact.email_provided" {
			emailEvents++
		}
	}
	if emailEvents != 1 {
		t.Fatalf("contact.email_provided 事件数 = %d，期望 1", emailEvents)
	}

	// 已留过邮箱的线索发起新会话：不再触发 collect_lead_contact
	secondConv, err := recv.Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.widget.ID, From: "visitor:lead-mail", Text: "再来问一个问题",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.runItem(t, secondConv.ConversationID, secondConv.Item.ID, secondConv.Item.Seq)
	secondItems, err := conversation.NewTimeline(env.DB).Handle(ctx, f.owner, secondConv.ConversationID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range secondItems {
		if it.Content.Prompt != nil && it.Content.Prompt.Kind == "collect_contact" {
			t.Fatalf("已有邮箱的线索不应再次收到 collect_contact：%+v", it)
		}
	}

	// 已登录用户不能调用 LeaveEmail
	u, err := contact.ResolveUser(ctx, env.DB, f.tenant.ID, f.widget.ID, "user:u-signed", "u-signed", contact.Profile{Name: "Bob", Email: "bob@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	userSub := identity.Subject{Type: contact.Type, ID: u.ID, Tenant: f.tenant.ID}
	if _, err := leave.Handle(ctx, userSub, f.widget.ID, widget.ContactEmailBody{Email: "spoof@example.com"}); !errors.Is(err, contact.ErrNotLead) {
		t.Fatalf("已登录用户改邮箱应返回 contact.ErrNotLead，实际 %v", err)
	}
}

// TestPermissionsAndTenantIsolation 检查管理接口的权限控制、多租户隔离与 HTTP 入口。
func TestPermissionsAndTenantIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setup(t, env, "auto-alpha")
	beta := setup(t, env, "auto-beta")

	// 无 tenant-settings:manage 权限的坐席不能读取和修改
	if _, err := automation.NewList(env.DB).Handle(ctx, alpha.agent); !errors.Is(err, automation.ErrForbidden) {
		t.Fatalf("无权限列出：%v", err)
	}
	rules, err := automation.NewList(env.DB).Handle(ctx, alpha.owner)
	if err != nil || len(rules) != 4 {
		t.Fatalf("管理员列出规则：len=%d err=%v", len(rules), err)
	}
	if _, err := automation.NewUpdate(env.DB).Handle(ctx, alpha.agent, rules[0].ID, false); !errors.Is(err, automation.ErrForbidden) {
		t.Fatalf("无权限修改：%v", err)
	}
	// 其他工作区的管理员改不到 alpha 的规则
	if _, err := automation.NewUpdate(env.DB).Handle(ctx, beta.owner, rules[0].ID, false); !errors.Is(err, automation.ErrNotFound) {
		t.Fatalf("跨租户修改：%v", err)
	}

	// HTTP 入口测试：鉴权、权限和正常响应
	r := chi.NewRouter()
	var currentSub identity.Subject
	api := rest.New(r, rest.Options{
		Title: "test", Version: "0.1.0",
		Tenant:   func(huma.Context) string { return alpha.tenant.ID },
		Accounts: identity.Accounts{member.Type: nil},
	})
	validate := func(_ context.Context, tok string) (identity.Subject, error) {
		if tok == "ok" {
			return currentSub, nil
		}
		return identity.Subject{}, identity.ErrUnauthenticated.New()
	}
	require := func(perms ...auth.Permission) func(*huma.Operation) {
		names := make([]string, len(perms))
		for i, p := range perms {
			names[i] = string(p)
		}
		return rest.RequireToken(api, validate, names...)
	}
	automation.RegisterRoutes(api, require, env.DB)

	// 未登录 → 401
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/automations", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录状态码 = %d，期望 401", rec.Code)
	}

	// 无权限坐席 → 403
	currentSub = alpha.agent
	req := httptest.NewRequest(http.MethodGet, "/automations", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("无权限状态码 = %d，期望 403", rec.Code)
	}

	// 有权限主管理员 → 200 & 切换开关
	currentSub = alpha.owner
	body, _ := json.Marshal(automation.UpdateBody{Enabled: false})
	putReq := httptest.NewRequest(http.MethodPut, "/automations/"+rules[0].ID, bytes.NewReader(body))
	putReq.Header.Set("Authorization", "Bearer ok")
	putReq.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, putReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("修改规则状态码 = %d，body=%s", rec.Code, rec.Body.String())
	}
	var updated automation.RuleDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil || updated.Enabled {
		t.Fatalf("修改结果：%+v %v", updated, err)
	}
}
