package report_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/report"
	"agents-desk/internal/tenant"
)

// TestReportsExcludeSpamConversations 验证标为垃圾的会话从概览、会话、响应时间、成员与满意度全部报表中彻底排除。
func TestReportsExcludeSpamConversations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "SpamReport", Slug: "spam-rpt", OwnerEmail: "owner@spam-rpt.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	owner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values()))}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "Web"})
	if err != nil {
		t.Fatal(err)
	}

	recv := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{})
	cGood, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "good", FromName: "Good", Text: "help me"})
	if err != nil {
		t.Fatal(err)
	}
	cSpam, err := recv.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch.ID, From: "spam", FromName: "Spam", Text: "buy spam"})
	if err != nil {
		t.Fatal(err)
	}

	// 将三条时间拉在同一自然日并标 cSpam 为垃圾会话（带已关闭轮次和评分也不应被计入）
	today := time.Now().UTC().Format("2006-01-02")
	score := 1
	nowUTC := time.Now().UTC()
	if _, err := env.DB.ExecContext(ctx, "UPDATE conversation_rounds SET first_reply_at = ?, first_reply_by_type = 'member', first_reply_by_id = ?, closed_at = ?, assignee_type = 'member', assignee_id = ?, rating = ?, rated_at = ? WHERE conversation_id = ?",
		nowUTC, tn.OwnerID, nowUTC, tn.OwnerID, score, nowUTC, cSpam.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewMarkSpam(env.DB, conversation.Downstream{}).Handle(ctx, owner, cSpam.ConversationID, conversation.MarkSpamBody{}); err != nil {
		t.Fatal(err)
	}

	p := period.Params{From: today, To: today, TimeZone: "UTC"}
	f := report.FilterParams{Params: p}

	ov, err := report.NewOverview(env.DB, env.Roles).Handle(ctx, owner, p)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Service.Created != 1 || ov.Live.ServiceOpen != 1 || ov.Service.Closed != 0 || ov.Service.RatedCount != 0 {
		t.Fatalf("Overview 不应计入垃圾会话：%+v", ov)
	}

	convRep, err := report.NewConversations(env.DB).Handle(ctx, owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if convRep.Summary.Created != 1 || convRep.Summary.OpenNow != 1 || convRep.Summary.Closed != 0 {
		t.Fatalf("Conversations 报表不应计入垃圾会话：%+v", convRep.Summary)
	}

	rtRep, err := report.NewResponseTime(env.DB).Handle(ctx, owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if rtRep.FirstReply.Samples != 0 || rtRep.Resolution.Samples != 0 {
		t.Fatalf("ResponseTime 报表不应计入垃圾会话：frt=%+v res=%+v", rtRep.FirstReply, rtRep.Resolution)
	}

	memRep, err := report.NewMembers(env.DB, env.Roles, env.Files).Handle(ctx, owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if memRep.Summary.ClosedCount != 0 {
		t.Fatalf("Members 报表不应计入垃圾会话关闭数：%+v", memRep.Summary)
	}

	satRep, err := report.NewSatisfaction(env.DB).Handle(ctx, owner, f)
	if err != nil {
		t.Fatal(err)
	}
	if satRep.Summary.RatedCount != 0 || satRep.Summary.ClosedCount != 0 {
		t.Fatalf("Satisfaction 报表不应计入垃圾会话评分：%+v", satRep.Summary)
	}

	// 顺便确认 cGood 状态未被破坏
	var goodOpen bool
	if err := env.DB.NewRaw("SELECT status = 'open' AND spam_at IS NULL FROM conversations WHERE id = ?", cGood.ConversationID).Scan(ctx, &goodOpen); err != nil {
		t.Fatal(err)
	}
	if !goodOpen {
		t.Fatalf("正常会话 cGood 状态异常")
	}
}
