package business_test

import (
	"errors"
	"testing"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/business"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/flags"
	"agents-desk/internal/member"
	"agents-desk/internal/platformsettings"
	"agents-desk/internal/tenant"
	"agents-desk/internal/widget"
)

func TestBusinessPolicyAndGovernance(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)

	// 创建两个隔离工作区 alpha / beta
	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha Corp", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "Alice Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta Corp", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "Bob Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 在 alpha 新增一名协作成员
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	ownerPerms := auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values()))
	subAlphaOwner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: ownerPerms}
	subBetaOwner := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: ownerPerms}
	helper, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, subAlphaOwner, member.AddParams{
		Email: "helper@alpha.com", Name: "Helper", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	policyGroup := business.NewPolicyGroup(env.DB)
	featureStore := flags.NewStore(env.DB)
	tgSettings := platformsettings.NewTelegramGroup(env.DB, env.Crypt)
	policyActions := business.NewPolicyActions(env.DB, policyGroup, featureStore, tgSettings)
	guard := business.NewPolicyGuard(env.DB, policyGroup, featureStore, tgSettings)

	// 1. 默认策略读取与保存、非法语气校验
	view, err := policyActions.Get(ctx, subAlphaOwner)
	if err != nil {
		t.Fatalf("读取默认商务策略失败：%v", err)
	}
	if !view.LinksEnabled || !view.TelegramAccountsEnabled || view.MaxLinksPerMember != 10 || view.DefaultTranslationTone != "formal" {
		t.Fatalf("默认商务策略异常：%+v", view)
	}
	if _, err := policyActions.Save(ctx, subAlphaOwner, business.Policy{
		LinksEnabled: true, TelegramAccountsEnabled: true, MaxLinksPerMember: 1, MaxTelegramAccountsPerMember: 2,
		DefaultTranslationTone: "invalid",
	}); !errors.Is(err, business.ErrInvalidTone) {
		t.Fatalf("非法语气应返回 ErrInvalidTone：%v", err)
	}
	saved, err := policyActions.Save(ctx, subAlphaOwner, business.Policy{
		LinksEnabled:                 true,
		TelegramAccountsEnabled:      true,
		MaxLinksPerMember:            1,
		MaxTelegramAccountsPerMember: 2,
		DefaultRequireIntake:         true,
		DefaultCompanyName:           "Alpha Global",
		DefaultTranslationTone:       "formal",
	})
	if err != nil || !saved.DefaultRequireIntake || saved.DefaultCompanyName != "Alpha Global" || saved.MaxLinksPerMember != 1 {
		t.Fatalf("保存商务策略失败：%v %+v", err, saved)
	}

	// 2. 配额与开关校验：alpha 上限 1 条链接，第 2 条触发 ErrQuotaExceeded
	createLink := widget.NewCreateBusinessLink(env.DB, "https://desk.example.com", guard.CheckCreateLink)
	link, err := createLink.Handle(ctx, subAlphaOwner, widget.CreateBusinessLinkBody{Name: "Alice 专线"})
	if err != nil {
		t.Fatalf("创建第 1 条商务链接失败：%v", err)
	}
	if link.PublicCode == "" {
		t.Fatalf("新建商务链接应自动生成 public_code")
	}
	if _, err := createLink.Handle(ctx, subAlphaOwner, widget.CreateBusinessLinkBody{Name: "超额链接"}); !errors.Is(err, business.ErrQuotaExceeded) {
		t.Fatalf("超过配额应返回 ErrQuotaExceeded：%v", err)
	}

	// 平台开关关闭后拒绝创建
	if err := featureStore.Set(ctx, flags.BusinessLinks, feature.Tenant(alpha.ID), feature.Off); err != nil {
		t.Fatal(err)
	}
	if err := guard.CheckCreateLink(ctx, subAlphaOwner); !errors.Is(err, business.ErrFeatureDisabled) {
		t.Fatalf("平台开关关闭应返回 ErrFeatureDisabled：%v", err)
	}
	if err := featureStore.Clear(ctx, flags.BusinessLinks, feature.Tenant(alpha.ID)); err != nil {
		t.Fatal(err)
	}

	// 3. 写入一条外部商务对话，测试管理员治理列表、强制停用、应急添加协作成员及跨工作区隔离
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	reg := channel.DefaultRegistry()
	reg[channel.BusinessLink] = widget.NewConnector(hub)
	down := conversation.Downstream{Hub: hub, Roles: env.Roles, ContactPush: widget.Push(hub)}
	receive := conversation.NewReceive(env.DB, reg, down)
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	recv, err := receive.Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: link.ID,
		From:      "visitor:v1",
		FromName:  "Partner A",
		Text:      "Hello Alice",
	})
	if err != nil {
		t.Fatalf("写入外部商务对话失败：%v", err)
	}

	gov := business.NewGovernance(env.DB, env.Roles, env.Files, down, "https://desk.example.com", nil)
	list, err := gov.ListChannels(ctx, subAlphaOwner, business.ListChannelsInput{})
	if err != nil || len(list) != 1 || list[0].ID != link.ID || list[0].ConversationCount != 1 {
		t.Fatalf("治理列表结果异常：%v %+v", err, list)
	}
	// beta 看不到 alpha 的商务渠道，也不能停用或添加协作成员
	betaList, err := gov.ListChannels(ctx, subBetaOwner, business.ListChannelsInput{})
	if err != nil || len(betaList) != 0 {
		t.Fatalf("跨工作区隔离失败：%v %+v", err, betaList)
	}
	if _, err := gov.SetChannelEnabled(ctx, subBetaOwner, link.ID, false); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("beta 不应停用 alpha 渠道：%v", err)
	}

	// 应急添加协作成员（同时加入已有会话与默认协作名单），外工作区成员应被拒绝
	if _, err := gov.AddCollaborators(ctx, subAlphaOwner, link.ID, business.AddCollaboratorsBody{
		MemberIDs: []string{beta.OwnerID}, ApplyToExisting: true, AddToDefault: true,
	}); !errors.Is(err, business.ErrInvalidMember) {
		t.Fatalf("跨工作区成员应返回 ErrInvalidMember：%v", err)
	}
	collabRes, err := gov.AddCollaborators(ctx, subAlphaOwner, link.ID, business.AddCollaboratorsBody{
		MemberIDs: []string{helper.ID}, ApplyToExisting: true, AddToDefault: true,
	})
	if err != nil || collabRes.UpdatedChats != 1 || len(collabRes.DefaultCollaboratorIDs) != 1 || collabRes.DefaultCollaboratorIDs[0] != helper.ID {
		t.Fatalf("应急添加协作成员失败：%v %+v", err, collabRes)
	}
	// 验证 helper 已加入外部商务对话参与者表
	var exists bool
	exists, err = env.DB.NewSelect().Table("conversation_participants").
		Where("conversation_id = ? AND participant_type = 'member' AND participant_id = ?", recv.ConversationID, helper.ID).
		Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("协作成员未加入会话参与者：%v %v", err, exists)
	}

	// 管理员强制停用渠道
	disabled, err := gov.SetChannelEnabled(ctx, subAlphaOwner, link.ID, false)
	if err != nil || disabled.Enabled {
		t.Fatalf("管理员停用渠道失败：%v %+v", err, disabled)
	}
}
