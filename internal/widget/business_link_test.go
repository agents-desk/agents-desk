package widget

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

func TestBusinessLinkEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	f := setup(t, env, "biz")

	// 新建另一位同工作区成员，验证个人商务链接的严格归属隔离
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", f.system.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, f.system, member.AddParams{
		Email: "bob@biz.com", Name: "Bob", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	subBob := identity.Subject{Type: member.Type, ID: bob.ID, Tenant: f.system.Tenant, Permissions: auth.WithBase(nil)}

	list := NewListBusinessLinks(env.DB, "https://desk.example.com")
	create := NewCreateBusinessLink(env.DB, "https://desk.example.com")
	update := NewUpdateBusinessLink(env.DB, "https://desk.example.com")
	del := NewDeleteBusinessLink(env.DB)

	// 1. 成员创建个人网页商务沟通链接，自动生成独立打开链接
	link, err := create.Handle(ctx, f.agent, CreateBusinessLinkBody{
		Name:       "Owner 商务专线",
		Appearance: Appearance{Greeting: "欢迎洽谈合作"},
	})
	if err != nil {
		t.Fatalf("创建个人商务沟通链接失败：%v", err)
	}
	if !link.Enabled || !strings.HasSuffix(link.ChatURL, "/widget/?channel="+link.ID) {
		t.Fatalf("商务沟通链接返回异常：%+v", link)
	}
	// 同工作区其他成员看不到、不能修改也不能删除别人的链接
	bobLinks, err := list.Handle(ctx, subBob)
	if err != nil || len(bobLinks) != 0 {
		t.Fatalf("Bob 不应看到 Owner 的个人商务链接：%v %+v", err, bobLinks)
	}
	if _, err := update.Handle(ctx, subBob, link.ID, UpdateBusinessLinkBody{Name: "篡改", Enabled: false}); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("Bob 不应修改 Owner 的个人商务链接：%v", err)
	}
	if err := del.Handle(ctx, subBob, link.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("Bob 不应删除 Owner 的个人商务链接：%v", err)
	}

	// 2. 外部商户打开个人商务沟通链接：建立访客会话、发送消息、读取时间线、成员回复并标记已读
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	reg := channel.DefaultRegistry()
	reg[channel.BusinessLink] = NewConnector(hub)
	down := conversation.Downstream{Hub: hub, Roles: env.Roles, ContactPush: Push(hub)}

	linkCtx := f.in(t, link.ID)
	sess, err := NewStartSession(env.DB, env.Crypt, f.tokens, down).Handle(linkCtx, SessionBody{
		Name:  "Elena Partner",
		Email: "elena@partner.io",
	}, apitoken.Device{})
	if err != nil {
		t.Fatalf("外部商户建立商务会话失败：%v", err)
	}
	if sess.Contact.Name != "Elena Partner" || sess.Contact.Email != "elena@partner.io" {
		t.Fatalf("外部商户资料未保存：%+v", sess.Contact)
	}
	subPartner := identity.Subject{Type: contact.Type, ID: sess.Contact.ID, Tenant: f.system.Tenant}
	if _, err := Resolve(env.DB)(linkCtx, subPartner); err != nil {
		t.Fatalf("商务链接联系人 Resolve 失败：%v", err)
	}
	if ancestors, err := FrameAncestors(ctx, env.DB, link.ID); err != nil || ancestors != "'none'" {
		t.Fatalf("商务沟通链接不允许第三方 iframe 嵌入，应返回 'none'：%v %q", err, ancestors)
	}

	receive := conversation.NewReceive(env.DB, reg, down)
	sent, err := NewSendMessage(env.DB, receive).Handle(linkCtx, subPartner, MessageBody{
		Text:     "Hi, we want to discuss B2B integration.",
		ClientID: "c-1",
	})
	if err != nil || sent.ConversationID == "" {
		t.Fatalf("外部商户发消息失败：%v %+v", err, sent)
	}

	// 成员回复外部商务聊天并走 Deliver 投递
	reply, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, f.agent, sent.ConversationID, conversation.SendChatMessageBody{
		Text: "Welcome Elena! Happy to discuss.",
	})
	if err != nil {
		t.Fatalf("成员回复商务聊天失败：%v", err)
	}
	if err := conversation.NewDeliver(env.DB, reg, down).Job(ctx, conversation.Appended{
		TenantID: f.system.Tenant, ConversationID: sent.ConversationID, ItemID: reply.ID, Seq: reply.Seq,
	}); err != nil {
		t.Fatalf("投递商务链接消息失败：%v", err)
	}

	// 外部商户通过 ContactStream 读取完整双向时间线，并标记已读
	stream, err := conversation.NewContactStream(env.DB).Handle(ctx, subPartner, link.ID, "", "", 50)
	if err != nil || len(stream.Items) != 2 || stream.Unread != 1 {
		t.Fatalf("商务链接访客时间线异常：%v %+v", err, stream)
	}
	if err := conversation.NewMarkAllReadByContact(env.DB, down).Handle(ctx, subPartner, link.ID, ""); err != nil {
		t.Fatalf("商务链接访客标记已读失败：%v", err)
	}
	stream2, err := conversation.NewContactStream(env.DB).Handle(ctx, subPartner, link.ID, "", "", 50)
	if err != nil || stream2.Unread != 0 {
		t.Fatalf("标记已读后未读应清零：%v %+v", err, stream2)
	}

	// 3. 停用后外部不可再访问；已有沟通记录的链接拒绝删除（请停用），未使用的链接可删除
	updated, err := update.Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:       "Owner 商务专线（暂停）",
		Appearance: link.Appearance,
		Enabled:    false,
	})
	if err != nil || updated.Enabled {
		t.Fatalf("停用个人商务链接失败：%v %+v", err, updated)
	}
	if _, err := findPublic(ctx, env.DB, link.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("停用后的商务链接不应公开可访问：%v", err)
	}
	if err := del.Handle(ctx, f.agent, link.ID); !errors.Is(err, ErrBusinessLinkInUse) {
		t.Fatalf("已有记录的商务链接应返回 ErrBusinessLinkInUse：%v", err)
	}
	unused, err := create.Handle(ctx, f.agent, CreateBusinessLinkBody{Name: "临时链接"})
	if err != nil {
		t.Fatal(err)
	}
	if err := del.Handle(ctx, f.agent, unused.ID); err != nil {
		t.Fatalf("删除未使用的个人商务链接失败：%v", err)
	}
	remaining, err := list.Handle(ctx, f.agent)
	if err != nil || len(remaining) != 1 || remaining[0].ID != link.ID {
		t.Fatalf("删除未使用链接后列表异常：%v %+v", err, remaining)
	}
}
