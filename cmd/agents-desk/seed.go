package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/savedview"

	"agents-desk/internal/attribute"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/macro"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tag"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
	"agents-desk/internal/widget"
)

// seedPassword 是 seed 写入的所有账号的密码，只用于开发和演示。
const seedPassword = "password"

// seedCommand 返回 seed 命令：写入开发和演示用的数据，见 seed。
func seedCommand(db *bun.DB, roles *authz.Store, adminLogins, memberLogins *login.Realm, files *filesystem.Service, keys *crypt.Crypt, addr tenant.Addresses, channels channel.Registry, down conversation.Downstream) *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "写入开发和演示用的账号和工作区（密码都是 password），只在开发和演示环境中运行",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return seed(cmd.Context(), cmd.OutOrStdout(), db, roles, adminLogins, memberLogins, files, keys, addr, channels, down)
		},
	}
}

// seed 通过业务动作写入开发和演示用的数据，已经存在的跳过，可以重复运行：
//   - 平台管理员 admin@example.com；
//   - 示例工作区 demo，主管理员 owner@example.com，以及新建工作区时创建的角色“管理员”“坐席”；
//   - demo 中角色为管理员的 manager@example.com、角色为坐席的 agent@example.com，两人都在线（主管理员是默认的离开）；
//   - demo 中的团队收件箱“一线客服”，轮流分配，成员 manager@、agent@；
//   - demo 中的测试渠道，新会话进入“一线客服”，以及 10 个示例会话，客户用英语、印地语、Hinglish，部分会话 agent@ 已经回复；
//   - demo 中的聊天插件“官网”，新会话进入“一线客服”。
//
// 每一项写入或跳过时向 out 输出一行。
func seed(ctx context.Context, out io.Writer, db *bun.DB, roles *authz.Store, adminLogins, memberLogins *login.Realm, files *filesystem.Service, keys *crypt.Crypt, addr tenant.Addresses, channels channel.Registry, down conversation.Downstream) error {
	system := identity.Subject{Type: identity.System, ID: "seed"}

	// 平台管理员
	_, err := platformadmin.NewCreate(db, roles, adminLogins).Handle(ctx, system, "admin", "admin@example.com", seedPassword)
	switch {
	case errors.Is(err, platformadmin.ErrEmailTaken):
		fmt.Fprintln(out, "平台管理员 admin@example.com 已存在，跳过")
	case err != nil:
		return err
	default:
		fmt.Fprintln(out, "已创建平台管理员 admin@example.com，密码", seedPassword)
	}

	// 示例工作区和它的主管理员
	_, err = tenant.NewCreate(db, roles, memberLogins, addr).Handle(ctx, system, tenant.CreateParams{
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
	add := member.NewAdd(db, roles, memberLogins, files)
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

	// manager@ 和 agent@ 在线，轮流分配时会分给他们；重复运行时保持在线
	for _, email := range []string{"manager@example.com", "agent@example.com"} {
		ref, err := memberLogins.Lookup(ctx, login.Email(email))
		if err != nil {
			return err
		}
		inMember := identity.Subject{Type: member.Type, ID: ref.ID, Tenant: demo}
		if _, err := member.NewSetMyStatus(db).Handle(ctx, inMember, member.StatusSetting{Status: member.Online}); err != nil {
			return err
		}
	}

	// 示例工作区的团队收件箱“一线客服”：轮流分配，成员 manager@、agent@
	t, err := team.NewCreate(db).Handle(ctx, inDemo, team.Changes{Name: "一线客服", Description: "处理客户的第一次咨询", Assignment: team.RoundRobin})
	switch {
	case errors.Is(err, team.ErrNameTaken):
		fmt.Fprintln(out, "团队收件箱 一线客服 已存在，跳过")
	case err != nil:
		return err
	default:
		if err := seedTeamMembers(ctx, out, db, roles, memberLogins, files, inDemo, t.ID); err != nil {
			return err
		}
	}
	if err := seedTagsAndAttributes(ctx, out, db, inDemo); err != nil {
		return err
	}
	if err := seedMacros(ctx, out, db, roles, memberLogins, inDemo); err != nil {
		return err
	}
	if err := seedConversations(ctx, out, db, roles, memberLogins, inDemo, channels, down); err != nil {
		return err
	}
	if err := seedViews(ctx, out, db, memberLogins, inDemo, down); err != nil {
		return err
	}
	if err := seedWidget(ctx, out, db, keys, inDemo); err != nil {
		return err
	}
	return seedReportHistory(ctx, out, db, memberLogins, inDemo)
}

// seedTagsAndAttributes 在示例工作区中写入默认标签和自定义属性定义，已存在的跳过。
func seedTagsAndAttributes(ctx context.Context, out io.Writer, db *bun.DB, inDemo identity.Subject) error {
	createTag := tag.NewCreate(db)
	createdTags := 0
	for _, item := range []tag.Changes{
		{Name: "VIP", Color: tag.Yellow, Description: "高价值客户"},
		{Name: "退款", Color: tag.DeepOrange, Description: "退款与取消订单相关"},
		{Name: "Bug", Color: tag.Red, Description: "产品故障或登录异常"},
		{Name: "待跟进", Color: tag.SkyBlue, Description: "需要后续跟进"},
	} {
		_, err := createTag.Handle(ctx, inDemo, item)
		switch {
		case errors.Is(err, tag.ErrNameTaken):
		case err != nil:
			return err
		default:
			createdTags++
		}
	}
	if createdTags > 0 {
		fmt.Fprintln(out, "已在示例工作区中创建", createdTags, "个示例标签")
	} else {
		fmt.Fprintln(out, "示例工作区已有示例标签，跳过")
	}

	createAttr := attribute.NewCreate(db)
	createdAttrs := 0
	for _, item := range []attribute.CreateParams{
		{
			Target: attribute.TargetConversation, Key: "issue_type", Name: "问题类型",
			Description: "用于分类咨询和后续统计", Type: attribute.TypeSelect,
			Options: []string{"物流", "退款", "账号", "支付", "优惠券"},
		},
		{
			Target: attribute.TargetConversation, Key: "order_no", Name: "订单号",
			Description: "关联的业务订单编号", Type: attribute.TypeText,
		},
		{
			Target: attribute.TargetContact, Key: "plan", Name: "套餐",
			Description: "客户当前的会员等级", Type: attribute.TypeSelect,
			Options: []string{"免费版", "专业版", "企业版"},
		},
		{
			Target: attribute.TargetContact, Key: "is_vip", Name: "VIP 客户",
			Description: "优先接待标记", Type: attribute.TypeBoolean,
		},
	} {
		_, err := createAttr.Handle(ctx, inDemo, item)
		switch {
		case errors.Is(err, attribute.ErrKeyTaken):
		case err != nil:
			return err
		default:
			createdAttrs++
		}
	}
	if createdAttrs > 0 {
		fmt.Fprintln(out, "已在示例工作区中创建", createdAttrs, "个示例属性")
	} else {
		fmt.Fprintln(out, "示例工作区已有示例属性，跳过")
	}
	return nil
}

// seedWidget 在示例工作区中建聊天插件“官网”，新会话进入团队收件箱“一线客服”。示例工作区中已经有聊天插件时跳过。
func seedWidget(ctx context.Context, out io.Writer, db *bun.DB, keys *crypt.Crypt, inDemo identity.Subject) error {
	n, err := db.NewSelect().Model((*channel.Channel)(nil)).Where("tenant_id = ? AND kind = ?", inDemo.Tenant, channel.Widget).Count(ctx)
	if err != nil {
		return errors.Wrap(err, "count widgets")
	}
	if n > 0 {
		fmt.Fprintln(out, "示例工作区已有聊天插件，跳过")
		return nil
	}
	var teamID string
	if err := db.NewRaw("SELECT id FROM teams WHERE tenant_id = ? AND name = '一线客服'", inDemo.Tenant).Scan(ctx, &teamID); err != nil {
		return errors.Wrap(err, "find demo team")
	}
	w, err := widget.NewCreate(db, keys).Handle(ctx, inDemo, widget.Changes{
		Name:       "官网",
		TeamID:     &teamID,
		Appearance: widget.Appearance{Greeting: "你好！有什么可以帮你？"},
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "已在示例工作区中创建聊天插件 官网：", w.ID)
	return nil
}

// seedTeamMembers 把管理员 manager@ 和坐席 agent@ 加入示例工作区的团队收件箱 teamID。
func seedTeamMembers(ctx context.Context, out io.Writer, db *bun.DB, roles *authz.Store, memberLogins *login.Realm, files *filesystem.Service, inDemo identity.Subject, teamID string) error {
	put := team.NewPutMember(db, roles, memberLogins, files)
	for _, email := range []string{"manager@example.com", "agent@example.com"} {
		ref, err := memberLogins.Lookup(ctx, login.Email(email))
		if err != nil {
			return err
		}
		if _, err := put.Handle(ctx, inDemo, teamID, ref.ID); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "已在示例工作区中创建团队收件箱 一线客服：成员 manager@example.com、agent@example.com")
	return nil
}

// seedChat 是一个示例会话：客户在测试渠道中的编号、资料以及按顺序的消息，agent 为 true 的由坐席回复。
type seedChat struct {
	from, name string
	userID     string // 非空时设为已登录用户（user），否则为线索（lead）
	email      string
	location   string
	timeZone   string
	referrer   string
	notes      string
	turns      []seedTurn
}

// seedTurn 是示例会话中的一条消息。
type seedTurn struct {
	agent bool
	text  string
}

// seedChats 是示例会话：客户用英语、印地语、Hinglish 提问，最后一条是客户发的会话在等待回复。
var seedChats = []seedChat{
	{
		from: "rahul", name: "Rahul Sharma", userID: "u-1001", email: "rahul.sharma@example.com",
		location: "Mumbai, India", timeZone: "Asia/Kolkata", referrer: "https://shop.example.com/orders",
		notes: "老客户，经常在周末下单",
		turns: []seedTurn{{false, "mera order abhi tak nahi aaya, 3 din ho gaye"}, {true, "您好，我帮您查一下订单，请稍等"}, {false, "theek hai, kab tak aayega?"}},
	},
	{
		from: "priya", name: "Priya Nair", userID: "u-1002", email: "priya.nair@example.com",
		location: "Bengaluru, India", timeZone: "Asia/Kolkata", referrer: "https://shop.example.com/wallet",
		turns: []seedTurn{{false, "My withdrawal has been pending for two days"}},
	},
	{
		from: "visitor-7f3a", location: "New Delhi, India", timeZone: "Asia/Kolkata", referrer: "https://shop.example.com/pricing",
		turns: []seedTurn{{false, "आप कौन से भुगतान तरीके स्वीकार करते हैं?"}, {true, "我们支持 UPI、银行卡和钱包"}},
	},
	{
		from: "anil", name: "Anil Kumar", userID: "u-1003", email: "anil.kumar@example.com",
		location: "Hyderabad, India", timeZone: "Asia/Kolkata",
		turns: []seedTurn{{false, "refund kab tak aayega?"}},
	},
	{
		from: "sneha", name: "Sneha Iyer", userID: "u-1004", email: "sneha.iyer@example.com",
		location: "Chennai, India", timeZone: "Asia/Kolkata", referrer: "https://shop.example.com/login",
		turns: []seedTurn{{false, "I can't log in, it says the code is wrong"}, {true, "请您退出后重新获取一次验证码"}, {false, "Still not working"}},
	},
	{
		from: "vikram", name: "Vikram Singh", userID: "u-1005", email: "vikram.singh@example.com",
		location: "Pune, India", timeZone: "Asia/Kolkata",
		turns: []seedTurn{{false, "payment account kaise change kare"}, {true, "在“我的 → 收款账户”中修改"}},
	},
	{
		from: "visitor-2c91", referrer: "https://shop.example.com/checkout",
		turns: []seedTurn{{false, "क्या इस कीमत में टैक्स शामिल है?"}},
	},
	{
		from: "meera", name: "Meera Joshi", userID: "u-1006", email: "meera.joshi@example.com",
		location: "Singapore", timeZone: "Asia/Singapore",
		turns: []seedTurn{{false, "I want to cancel my order"}},
	},
	{
		from: "arjun", name: "Arjun Das", email: "arjun.das@example.com",
		location: "Kolkata, India", timeZone: "Asia/Kolkata",
		turns: []seedTurn{{false, "seller ship hi nahi kar raha"}, {true, "我们已经联系卖家，今天之内回复您"}},
	},
	{
		from: "kavya", name: "Kavya Reddy", userID: "u-1007", email: "kavya.reddy@example.com",
		location: "Bengaluru, India", timeZone: "Asia/Kolkata", referrer: "https://shop.example.com/promo",
		turns: []seedTurn{{false, "The coupon code doesn't work"}},
	},
}

// seedConversations 在示例工作区中建一个测试渠道，新会话进入团队收件箱“一线客服”，并写入示例会话。
// 示例工作区中已经有渠道时跳过。坐席的回复以 agent@ 的身份发出。
func seedConversations(ctx context.Context, out io.Writer, db *bun.DB, roles *authz.Store, memberLogins *login.Realm, inDemo identity.Subject, channels channel.Registry, down conversation.Downstream) error {
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
	agent, err := member.Resolve(db, roles)(identity.WithTenant(ctx, inDemo.Tenant), identity.Subject{Type: member.Type, ID: ref.ID})
	if err != nil {
		return err
	}
	receive, reply, closeConv := conversation.NewReceive(db, channels, down), conversation.NewReply(db, down), conversation.NewClose(db, down)
	addConvTag := conversation.NewAddTag(db, down)
	patchConvAttr := conversation.NewPatchAttributes(db, down)
	addContactTag := conversation.NewAddContactTag(db, down)
	patchContactAttr := conversation.NewPatchContactAttributes(db, down)
	tagsByName := map[string]string{}
	var demoTags []tag.Tag
	if err := db.NewSelect().Model(&demoTags).Where("tenant_id = ?", inDemo.Tenant).Scan(ctx); err != nil {
		return errors.Wrap(err, "load demo tags")
	}
	for _, t := range demoTags {
		tagsByName[t.Name] = t.ID
	}
	for i, chat := range seedChats {
		var id string
		for _, turn := range chat.turns {
			if turn.agent {
				if _, err := reply.Handle(ctx, agent, id, conversation.ReplyBody{Text: turn.text}); err != nil {
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
		// 补充示例联系人的类型和资料，方便联系人列表、筛选和资料页演示
		if chat.userID != "" || chat.email != "" || chat.location != "" || chat.timeZone != "" || chat.referrer != "" || chat.notes != "" {
			q := db.NewUpdate().Model((*contact.Contact)(nil)).
				Set("email = ?", chat.email).
				Set("location = ?", chat.location).
				Set("time_zone = ?", chat.timeZone).
				Set("referrer = ?", chat.referrer).
				Set("notes = ?", chat.notes).
				Where("id = (SELECT contact_id FROM contact_identities WHERE channel_id = ? AND external_id = ?)", ch.ID, chat.from)
			if chat.userID != "" {
				q = q.Set("kind = ?", contact.User).Set("external_user_id = ?", chat.userID)
			}
			if _, err := q.Exec(ctx); err != nil {
				return errors.Wrap(err, "enrich seed contact")
			}
		}
		// 给部分示例会话和联系人打上标签和自定义属性，方便直接体验筛选和详情编辑
		switch i {
		case 0:
			if tid := tagsByName["待跟进"]; tid != "" {
				if _, err := addConvTag.Handle(ctx, inDemo, id, tid); err != nil {
					return err
				}
			}
			if tid := tagsByName["VIP"]; tid != "" {
				if _, err := addContactTag.Handle(ctx, inDemo, id, tid); err != nil {
					return err
				}
			}
			if _, err := patchConvAttr.Handle(ctx, inDemo, id, map[string]any{"issue_type": "物流", "order_no": "ORD-20261120-001"}); err != nil {
				return err
			}
			if _, err := patchContactAttr.Handle(ctx, inDemo, id, map[string]any{"plan": "专业版", "is_vip": true}); err != nil {
				return err
			}
		case 3:
			if tid := tagsByName["退款"]; tid != "" {
				if _, err := addConvTag.Handle(ctx, inDemo, id, tid); err != nil {
					return err
				}
			}
			if _, err := patchConvAttr.Handle(ctx, inDemo, id, map[string]any{"issue_type": "退款"}); err != nil {
				return err
			}
		case 4:
			if tid := tagsByName["Bug"]; tid != "" {
				if _, err := addConvTag.Handle(ctx, inDemo, id, tid); err != nil {
					return err
				}
			}
			if _, err := patchConvAttr.Handle(ctx, inDemo, id, map[string]any{"issue_type": "账号"}); err != nil {
				return err
			}
		case 5:
			// 关闭一个已回复完毕的示例会话，生成真实的关闭轮次记录
			if _, err := closeConv.Handle(ctx, agent, id); err != nil {
				return err
			}
		}
	}
	var itemIDs []string
	if err := db.NewSelect().Model((*conversation.Item)(nil)).ColumnExpr("id::text").
		Where("tenant_id = ? AND kind IN (?) AND actor_type <> ?",
			inDemo.Tenant, bun.List([]conversation.ItemKind{conversation.Message, conversation.Note}), conversation.SystemActor).
		OrderExpr("id").Scan(ctx, &itemIDs); err != nil {
		return errors.Wrap(err, "list seed searchable items")
	}
	if len(itemIDs) > 0 {
		if err := conversation.SearchIndex.Refresh(ctx, db, conversation.LoadSearchDocs, itemIDs...); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "已在示例工作区中创建测试渠道和", len(seedChats), "个示例会话")
	return nil
}

// seedMacros 在示例工作区中写入共享和个人示例快捷回复，已存在相同快捷码的跳过。
func seedMacros(ctx context.Context, out io.Writer, db *bun.DB, roles *authz.Store, memberLogins *login.Realm, inDemo identity.Subject) error {
	create := macro.NewCreate(db, roles)
	high := conversation.High
	tomorrow := macro.SnoozeTomorrow

	// 查出常用标签，用于附带操作示例
	var tagRows []struct {
		ID   string `bun:"id"`
		Name string `bun:"name"`
	}
	if err := db.NewSelect().Table("tags").Column("id", "name").Where("tenant_id = ?", inDemo.Tenant).Scan(ctx, &tagRows); err != nil {
		return errors.Wrap(err, "load seed tags for macros")
	}
	tagsByName := map[string]string{}
	for _, row := range tagRows {
		tagsByName[row.Name] = row.ID
	}

	resolvedActions := macro.Actions{
		Status: &macro.StatusAction{Kind: conversation.StatusActionClose},
	}
	orderActions := macro.Actions{
		Priority:      &high,
		SetAttributes: map[string]any{"issue_type": "退款"},
	}
	if tid := tagsByName["退款"]; tid != "" {
		orderActions.AddTagIDs = []string{tid}
	}

	shared := []macro.CreateParams{
		{
			Scope:    macro.ScopeShared,
			Shortcut: "greeting",
			Title:    "欢迎语",
			Body:     "您好 {{contact.name | 朋友}}，我是客服 {{member.name}}，请问有什么可以帮您？",
		},
		{
			Scope:    macro.ScopeShared,
			Shortcut: "order",
			Title:    "核对订单与退款",
			Body:     "您好 {{contact.name | 朋友}}，我来为您优先核实订单进度。请确认您的联系邮箱是否为 {{contact.email | 您下单时填写的邮箱}}，并告知订单号。",
			Actions:  orderActions,
		},
		{
			Scope:    macro.ScopeShared,
			Shortcut: "resolved",
			Title:    "问题已解决并关闭",
			Body:     "很高兴为您解决问题，{{contact.name | 朋友}}！如后续还有需要，随时联系我们。",
			Actions:  resolvedActions,
		},
	}

	created := 0
	for _, p := range shared {
		_, err := create.Handle(ctx, inDemo, p)
		switch {
		case errors.Is(err, macro.ErrShortcutTaken):
		case err != nil:
			return err
		default:
			created++
		}
	}

	agentRef, err := memberLogins.Lookup(ctx, login.Email("agent@example.com"))
	if err != nil {
		return err
	}
	agentSub := identity.Subject{Type: member.Type, ID: agentRef.ID, Tenant: inDemo.Tenant}
	_, err = create.Handle(ctx, agentSub, macro.CreateParams{
		Scope:    macro.ScopePersonal,
		Shortcut: "tomorrow",
		Title:    "明日跟进提醒",
		Body:     "您好 {{contact.name | 朋友}}，这边已记录您的问题，将在明天为您同步最新进展。",
		Actions: macro.Actions{
			Assignee: &conversation.AssigneeChange{Mode: conversation.AssigneeMe},
			Status:   &macro.StatusAction{Kind: conversation.StatusActionSnooze, Snooze: &tomorrow},
		},
	})
	switch {
	case errors.Is(err, macro.ErrShortcutTaken):
	case err != nil:
		return err
	default:
		created++
	}

	if created > 0 {
		fmt.Fprintf(out, "已在示例工作区中创建 %d 条示例快捷回复\n", created)
	} else {
		fmt.Fprintln(out, "示例工作区已有示例快捷回复，跳过")
	}
	return nil
}

// seedReportHistory 为示例工作区补全跨最近 6 天的历史服务轮次、多星级客户评价、消息语言识别结果与翻译模型调用记录，
// 让基础报表开箱即可展示趋势、分布与费用明细；已写入过翻译调用记录时跳过，保持幂等。
func seedReportHistory(ctx context.Context, out io.Writer, db *bun.DB, memberLogins *login.Realm, inDemo identity.Subject) error {
	n, err := db.NewSelect().Model((*llm.Call)(nil)).Where("tenant_id = ?", inDemo.Tenant).Count(ctx)
	if err != nil {
		return errors.Wrap(err, "count seed llm calls")
	}
	if n > 0 {
		fmt.Fprintln(out, "示例工作区已有报表历史与翻译记录，跳过")
		return nil
	}

	var teamID string
	if err := db.NewRaw("SELECT id::text FROM teams WHERE tenant_id = ? AND name = '一线客服'", inDemo.Tenant).Scan(ctx, &teamID); err != nil {
		return errors.Wrap(err, "find demo team for report seed")
	}
	agentRef, err := memberLogins.Lookup(ctx, login.Email("agent@example.com"))
	if err != nil {
		return err
	}
	managerRef, err := memberLogins.Lookup(ctx, login.Email("manager@example.com"))
	if err != nil {
		return err
	}

	var convIDs []string
	if err := db.NewSelect().Table("conversations").ColumnExpr("id::text").
		Where("tenant_id = ?", inDemo.Tenant).OrderExpr("created_at ASC, id ASC").Scan(ctx, &convIDs); err != nil {
		return errors.Wrap(err, "list demo conversations for report seed")
	}
	if len(convIDs) < 6 {
		fmt.Fprintln(out, "示例工作区会话不足，跳过报表历史")
		return nil
	}

	base := time.Now().UTC().Truncate(time.Minute)
	// 把部分示例会话的创建时间分散到最近 6 天并补充识别语言，便于趋势与语言分布展示。
	dayOffsets := []int{6, 5, 4, 4, 3, 2, 2, 1, 1, 0}
	langs := []string{"hi-Latn", "en", "hi", "hi-Latn", "en", "hi-Latn", "hi", "en", "hi-Latn", "en"}
	for i, cid := range convIDs {
		if i >= len(dayOffsets) {
			break
		}
		ts := base.Add(-time.Duration(dayOffsets[i])*24*time.Hour - time.Duration(i*37)*time.Minute)
		if _, err := db.NewUpdate().Table("conversations").
			Set("created_at = ?", ts).
			Set("language = ?", langs[i]).
			Where("id = ?", cid).Exec(ctx); err != nil {
			return errors.Wrap(err, "spread seed conversation created_at")
		}
		if _, err := db.NewUpdate().Table("timeline_items").
			Set("created_at = ?", ts).
			Set("derived = jsonb_set(derived, '{language}', to_jsonb(?::text), true)", langs[i]).
			Where("conversation_id = ? AND kind = ? AND actor_type = ?", cid, conversation.Message, conversation.ContactActor).Exec(ctx); err != nil {
			return errors.Wrap(err, "seed contact item language")
		}
		if _, err := db.NewUpdate().Table("timeline_items").
			Set("created_at = ?", ts.Add(3*time.Minute)).
			Where("conversation_id = ? AND kind = ? AND actor_type = ?", cid, conversation.Message, conversation.MemberActor).Exec(ctx); err != nil {
			return errors.Wrap(err, "spread seed member reply created_at")
		}
	}

	// 写入跨多天的已完成服务轮次与客户评价（含 2–5 星及文字意见），轮次号从 10 开始避免与当前活跃轮次冲突。
	type seedRound struct {
		convIdx  int
		roundNo  int
		daysAgo  int
		frtSec   int
		resSec   int
		firstBy  string
		assignee string
		rating   int
		comment  string
	}
	samples := []seedRound{
		{0, 10, 5, 110, 960, agentRef.ID, agentRef.ID, 5, "响应非常快，马上帮我查到物流进度！"},
		{1, 10, 4, 240, 1800, managerRef.ID, managerRef.ID, 5, "解释很清楚，提现已到账"},
		{2, 10, 3, 420, 2700, agentRef.ID, agentRef.ID, 4, "回答准确，流程顺畅"},
		{3, 10, 3, 95, 1200, agentRef.ID, managerRef.ID, 5, "转接处理很高效"},
		{4, 10, 2, 1320, 5400, managerRef.ID, managerRef.ID, 2, "等待验证码排查时间偏长"},
		{5, 10, 1, 180, 1500, agentRef.ID, agentRef.ID, 5, "操作指引一看就懂"},
		{5, 11, 0, 75, 620, agentRef.ID, agentRef.ID, 4, "二次追问也很快解决了"},
	}
	for _, s := range samples {
		opened := base.Add(-time.Duration(s.daysAgo)*24*time.Hour - 2*time.Hour)
		firstReply := opened.Add(time.Duration(s.frtSec) * time.Second)
		closed := opened.Add(time.Duration(s.resSec) * time.Second)
		rated := closed.Add(4 * time.Minute)
		score := s.rating
		r := &conversation.Round{
			ID:               uuid.Must(uuid.NewV7()).String(),
			TenantID:         inDemo.Tenant,
			ConversationID:   convIDs[s.convIdx],
			Round:            s.roundNo,
			TeamID:           &teamID,
			AssigneeType:     conversation.MemberActor,
			AssigneeID:       s.assignee,
			ClosedByType:     conversation.MemberActor,
			ClosedByID:       s.assignee,
			OpenedAt:         opened,
			FirstReplyAt:     &firstReply,
			FirstReplyByType: conversation.MemberActor,
			FirstReplyByID:   s.firstBy,
			ClosedAt:         &closed,
			Rating:           &score,
			RatingComment:    s.comment,
			RatedAt:          &rated,
		}
		if _, err := db.NewInsert().Model(r).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert seed report round")
		}
	}

	// 写入跨多天的翻译模型调用记录（含 incoming / reply / back_translate / fill 及 1 条失败样本）。
	type seedCall struct {
		daysAgo    int
		operation  string
		model      string
		prompt     int
		completion int
		durationMS int
		cost       float64
		errText    string
	}
	calls := []seedCall{
		{5, "incoming", "google/gemini-2.5-flash", 260, 48, 520, 0.000180, ""},
		{5, "reply", "google/gemini-2.5-flash", 310, 56, 610, 0.000210, ""},
		{5, "back_translate", "google/gemini-2.5-flash", 140, 44, 390, 0.000110, ""},
		{4, "incoming", "google/gemini-2.5-flash", 280, 52, 540, 0.000195, ""},
		{3, "incoming", "google/gemini-2.5-flash", 340, 62, 680, 0.000235, ""},
		{3, "fill", "google/gemini-2.5-flash", 220, 46, 470, 0.000160, ""},
		{2, "incoming", "google/gemini-2.5-flash", 290, 50, 510, 0.000190, ""},
		{2, "reply", "google/gemini-2.5-flash", 350, 0, 1240, 0, "model returned status 429: rate limited"},
		{1, "incoming", "google/gemini-2.5-flash", 275, 54, 495, 0.000188, ""},
		{1, "reply", "google/gemini-2.5-flash", 320, 60, 590, 0.000220, ""},
		{1, "back_translate", "google/gemini-2.5-flash", 155, 42, 410, 0.000115, ""},
		{0, "incoming", "google/gemini-2.5-flash", 300, 58, 530, 0.000205, ""},
	}
	tenantID := inDemo.Tenant
	for i, c := range calls {
		ts := base.Add(-time.Duration(c.daysAgo)*24*time.Hour - time.Duration(i*19)*time.Minute)
		var costPtr *float64
		if c.cost > 0 {
			v := c.cost
			costPtr = &v
		}
		rec := &llm.Call{
			ID:               uuid.Must(uuid.NewV7()).String(),
			CreatedAt:        ts,
			TenantID:         &tenantID,
			CallType:         llm.Translation,
			Operation:        c.operation,
			Meta:             map[string]any{"seed": true},
			Endpoint:         "https://openrouter.ai/api/v1",
			Model:            c.model,
			Request:          map[string]any{"model": c.model},
			Response:         "{}",
			PromptTokens:     c.prompt,
			CompletionTokens: c.completion,
			ReasoningTokens:  0,
			Cost:             costPtr,
			DurationMS:       c.durationMS,
			Error:            c.errText,
			TargetType:       "conversation",
			TargetID:         convIDs[i%len(convIDs)],
		}
		if _, err := db.NewInsert().Model(rec).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert seed llm call")
		}
	}
	fmt.Fprintf(out, "已在示例工作区中写入 %d 轮报表历史与 %d 条翻译调用记录\n", len(samples), len(calls))
	return nil
}

// seedViews 在示例工作区中写入共享和个人示例视图，已存在同名视图的跳过。
func seedViews(ctx context.Context, out io.Writer, db *bun.DB, memberLogins *login.Realm, inDemo identity.Subject, down conversation.Downstream) error {
	create := conversation.NewCreateView(db, down)
	waiting := true
	shared := []conversation.ViewCreateParams{
		{
			Scope: savedview.ScopeShared,
			Name:  "待回复会话",
			Config: conversation.ViewConfig{
				Filter: conversation.Filter{
					Statuses: []conversation.Status{conversation.Open},
					Waiting:  &waiting,
				},
				Sort: conversation.ByWaiting,
			},
		},
		{
			Scope: savedview.ScopeShared,
			Name:  "退款咨询",
			Config: conversation.ViewConfig{
				Filter: conversation.Filter{
					Statuses: []conversation.Status{conversation.Open},
					Attributes: []conversation.AttributeFilter{
						{Key: "issue_type", Op: conversation.AttrOpEq, Value: "退款"},
					},
				},
				Sort: conversation.ByWaiting,
			},
		},
	}

	created := 0
	for _, p := range shared {
		_, err := create.Handle(ctx, inDemo, p)
		switch {
		case errors.Is(err, savedview.ErrNameTaken):
		case err != nil:
			return err
		default:
			created++
		}
	}

	agentRef, err := memberLogins.Lookup(ctx, login.Email("agent@example.com"))
	if err != nil {
		return err
	}
	agentSub := identity.Subject{Type: member.Type, ID: agentRef.ID, Tenant: inDemo.Tenant, Permissions: []string{string(auth.ConversationsRead)}}
	_, err = create.Handle(ctx, agentSub, conversation.ViewCreateParams{
		Scope: savedview.ScopePersonal,
		Name:  "我负责的待回复",
		Config: conversation.ViewConfig{
			Filter: conversation.Filter{
				Statuses: []conversation.Status{conversation.Open},
				Assignee: &conversation.AssigneeFilter{Mode: conversation.AssigneeFilterMe},
				Waiting:  &waiting,
			},
			Sort: conversation.ByWaiting,
		},
	})
	switch {
	case errors.Is(err, savedview.ErrNameTaken):
	case err != nil:
		return err
	default:
		created++
	}

	if created > 0 {
		fmt.Fprintf(out, "已在示例工作区中创建 %d 个示例视图\n", created)
	} else {
		fmt.Fprintln(out, "示例工作区已有示例视图，跳过")
	}
	return nil
}
