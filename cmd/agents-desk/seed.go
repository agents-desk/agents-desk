package main

import (
	"context"
	"fmt"
	"io"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// seedPassword 是 seed 写入的所有账号的密码，只用于开发和演示。
const seedPassword = "password"

// seedCommand 返回 seed 命令：写入开发和演示用的数据，见 seed。
func seedCommand(db *bun.DB, adminLogins, memberLogins *login.Realm, files *filesystem.Service, addr tenant.Addresses, channels channel.Registry, down conversation.Downstream) *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "写入开发和演示用的账号和工作区（密码都是 password），只在开发和演示环境中运行",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return seed(cmd.Context(), cmd.OutOrStdout(), db, adminLogins, memberLogins, files, addr, channels, down)
		},
	}
}

// seed 通过业务动作写入开发和演示用的数据，已经存在的跳过，可以重复运行：
//   - 平台管理员 admin@example.com；
//   - 示例工作区 demo，主管理员 owner@example.com，以及新建工作区时创建的角色“管理员”“坐席”；
//   - demo 中角色为管理员的 manager@example.com、角色为坐席的 agent@example.com；
//   - demo 中的团队“一线客服”，成员 manager@、agent@；
//   - demo 中的测试渠道，新会话进入“一线客服”，以及 10 个示例会话，客户用英语、印地语、Hinglish，部分会话 agent@ 已经回复。
//
// 每一项写入或跳过时向 out 输出一行。
func seed(ctx context.Context, out io.Writer, db *bun.DB, adminLogins, memberLogins *login.Realm, files *filesystem.Service, addr tenant.Addresses, channels channel.Registry, down conversation.Downstream) error {
	system := identity.Subject{Type: identity.System, ID: "seed"}

	// 平台管理员
	_, err := platformadmin.NewCreate(db, adminLogins).Handle(ctx, system, "admin", "admin@example.com", seedPassword)
	switch {
	case errors.Is(err, platformadmin.ErrEmailTaken):
		fmt.Fprintln(out, "平台管理员 admin@example.com 已存在，跳过")
	case err != nil:
		return err
	default:
		fmt.Fprintln(out, "已创建平台管理员 admin@example.com，密码", seedPassword)
	}

	// 示例工作区和它的主管理员
	_, err = tenant.NewCreate(db, memberLogins, addr).Handle(ctx, system, tenant.CreateParams{
		Name: "示例工作区", Slug: "demo",
		OwnerEmail: "owner@example.com", OwnerName: "主管理员", OwnerPassword: seedPassword,
	})
	switch {
	case errors.Is(err, tenant.ErrSlugTaken):
		fmt.Fprintln(out, "示例工作区 demo 已存在，跳过")
	case err != nil:
		return err
	default:
		fmt.Fprintln(out, "已创建示例工作区 demo，主管理员 owner@example.com，密码", seedPassword)
	}

	// 示例工作区的其他成员：管理员和坐席，以系统的身份在 demo 中添加，角色是新建工作区时创建的同名角色
	var demo string
	if err := db.NewRaw("SELECT id FROM tenants WHERE slug = 'demo'").Scan(ctx, &demo); err != nil {
		return errors.Wrap(err, "find demo tenant")
	}
	roles := authz.NewStore(db, auth.Catalog)
	add := member.NewAdd(db, memberLogins, files)
	inDemo := identity.Subject{Type: identity.System, ID: "seed", Tenant: demo}
	for _, m := range []struct{ email, name string }{
		{"manager@example.com", "管理员"},
		{"agent@example.com", "坐席"},
	} {
		r, err := roles.RoleByName(ctx, m.name, demo)
		if err != nil {
			return err
		}
		_, err = add.Handle(ctx, inDemo, member.AddParams{Email: m.email, Name: m.name, Password: seedPassword, RoleID: r.ID})
		switch {
		case errors.Is(err, member.ErrAlreadyMember):
			fmt.Fprintln(out, "成员", m.email, "已在示例工作区中，跳过")
		case err != nil:
			return err
		default:
			fmt.Fprintln(out, "已在示例工作区中添加", m.name, m.email, "，密码", seedPassword)
		}
	}

	// 示例工作区的团队“一线客服”：成员 manager@、agent@
	t, err := team.NewCreate(db).Handle(ctx, inDemo, team.Changes{Name: "一线客服", Description: "处理客户的第一次咨询"})
	switch {
	case errors.Is(err, team.ErrNameTaken):
		fmt.Fprintln(out, "团队 一线客服 已存在，跳过")
	case err != nil:
		return err
	default:
		if err := seedTeamMembers(ctx, out, db, memberLogins, files, inDemo, t.ID); err != nil {
			return err
		}
	}
	return seedConversations(ctx, out, db, memberLogins, inDemo, channels, down)
}

// seedTeamMembers 把管理员 manager@ 和坐席 agent@ 加入示例工作区的团队 teamID。
func seedTeamMembers(ctx context.Context, out io.Writer, db *bun.DB, memberLogins *login.Realm, files *filesystem.Service, inDemo identity.Subject, teamID string) error {
	put := team.NewPutMember(db, memberLogins, files)
	for _, email := range []string{"manager@example.com", "agent@example.com"} {
		ref, err := memberLogins.Lookup(ctx, login.Email(email))
		if err != nil {
			return err
		}
		if _, err := put.Handle(ctx, inDemo, teamID, ref.ID); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "已在示例工作区中创建团队 一线客服：成员 manager@example.com、agent@example.com")
	return nil
}

// seedChat 是一个示例会话：客户在测试渠道中的编号和名字，以及按顺序的消息，agent 为 true 的由坐席回复。
type seedChat struct {
	from, name string
	turns      []seedTurn
}

// seedTurn 是示例会话中的一条消息。
type seedTurn struct {
	agent bool
	text  string
}

// seedChats 是示例会话：客户用英语、印地语、Hinglish 提问，最后一条是客户发的会话在等待回复。
var seedChats = []seedChat{
	{"rahul", "Rahul Sharma", []seedTurn{{false, "mera order abhi tak nahi aaya, 3 din ho gaye"}, {true, "您好，我帮您查一下订单，请稍等"}, {false, "theek hai, kab tak aayega?"}}},
	{"priya", "Priya Nair", []seedTurn{{false, "My withdrawal has been pending for two days"}}},
	{"visitor-7f3a", "", []seedTurn{{false, "आप कौन से भुगतान तरीके स्वीकार करते हैं?"}, {true, "我们支持 UPI、银行卡和钱包"}}},
	{"anil", "Anil Kumar", []seedTurn{{false, "refund kab tak aayega?"}}},
	{"sneha", "Sneha Iyer", []seedTurn{{false, "I can't log in, it says the code is wrong"}, {true, "请您退出后重新获取一次验证码"}, {false, "Still not working"}}},
	{"vikram", "Vikram Singh", []seedTurn{{false, "payment account kaise change kare"}, {true, "在“我的 → 收款账户”中修改"}}},
	{"visitor-2c91", "", []seedTurn{{false, "क्या इस कीमत में टैक्स शामिल है?"}}},
	{"meera", "Meera Joshi", []seedTurn{{false, "I want to cancel my order"}}},
	{"arjun", "Arjun Das", []seedTurn{{false, "seller ship hi nahi kar raha"}, {true, "我们已经联系卖家，今天之内回复您"}}},
	{"kavya", "Kavya Reddy", []seedTurn{{false, "The coupon code doesn't work"}}},
}

// seedConversations 在示例工作区中建一个测试渠道，新会话进入团队“一线客服”，并写入示例会话。
// 示例工作区中已经有渠道时跳过。坐席的回复以 agent@ 的身份发出。
func seedConversations(ctx context.Context, out io.Writer, db *bun.DB, memberLogins *login.Realm, inDemo identity.Subject, channels channel.Registry, down conversation.Downstream) error {
	n, err := db.NewSelect().Model((*channel.Channel)(nil)).Where("tenant_id = ?", inDemo.Tenant).Count(ctx)
	if err != nil {
		return errors.Wrap(err, "count channels")
	}
	if n > 0 {
		fmt.Fprintln(out, "示例工作区已有渠道，跳过示例会话")
		return nil
	}
	var teamID string
	if err := db.NewRaw("SELECT id FROM teams WHERE tenant_id = ? AND name = '一线客服'", inDemo.Tenant).Scan(ctx, &teamID); err != nil {
		return errors.Wrap(err, "find demo team")
	}
	ch, err := channel.NewCreate(db).Handle(ctx, inDemo, channel.CreateParams{Kind: channel.Test, Name: "测试渠道", TeamID: &teamID})
	if err != nil {
		return err
	}
	ref, err := memberLogins.Lookup(ctx, login.Email("agent@example.com"))
	if err != nil {
		return err
	}
	// 坐席的身份和权限与他在请求中的相同
	agent, err := member.Resolve(db, authz.NewStore(db, auth.Catalog))(identity.WithTenant(ctx, inDemo.Tenant), identity.Subject{Type: member.Type, ID: ref.ID})
	if err != nil {
		return err
	}
	receive, reply := conversation.NewReceive(db, channels, down), conversation.NewReply(db, down)
	for _, chat := range seedChats {
		var id string
		for _, turn := range chat.turns {
			if turn.agent {
				if _, err := reply.Handle(ctx, agent, id, turn.text); err != nil {
					return err
				}
				continue
			}
			res, err := receive.Handle(ctx, inDemo, conversation.ReceiveParams{ChannelID: ch.ID, From: chat.from, FromName: chat.name, Text: turn.text})
			if err != nil {
				return err
			}
			id = res.ConversationID
		}
	}
	fmt.Fprintln(out, "已在示例工作区中创建测试渠道和", len(seedChats), "个示例会话")
	return nil
}
