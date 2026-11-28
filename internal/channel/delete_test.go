package channel_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
	"agents-desk/internal/widget"
)

// TestDeleteAndArchiveChannelAndIsolation 验证公共渠道无引用物理删除、有引用软归档（从配置列表隐藏、保留只读列表）、
// 个人商务渠道保护及跨工作区隔离。
func TestDeleteAndArchiveChannelAndIsolation(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "ch-a", Slug: "ch-a", OwnerEmail: "owner@cha.com", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "ch-b", Slug: "ch-b", OwnerEmail: "owner@chb.com", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	alphaSub := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values()))}
	betaSub := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, auth.TenantPermissions.Values()))}
	sysA := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}

	createWidget := widget.NewCreate(env.DB, env.Crypt)
	wClean, err := createWidget.Handle(ctx, alphaSub, widget.Changes{Name: "未使用插件"})
	if err != nil {
		t.Fatal(err)
	}
	wUsed, err := createWidget.Handle(ctx, alphaSub, widget.Changes{Name: "已产生会话插件"})
	if err != nil {
		t.Fatal(err)
	}
	reg := channel.DefaultRegistry()
	reg[channel.Widget] = channel.TestConnector{}
	if _, err := conversation.NewReceive(env.DB, reg, conversation.Downstream{}).Handle(ctx, sysA, conversation.ReceiveParams{
		ChannelID: wUsed.ID, From: "v-1", Text: "hello",
	}); err != nil {
		t.Fatal(err)
	}

	// 创建一个个人商务渠道验证删除保护
	personalCh := &channel.Channel{
		ID:            "019a0000-0000-7000-8000-000000000901",
		TenantID:      alpha.ID,
		Kind:          channel.BusinessLink,
		Audience:      channel.AudiencePartner,
		Name:          "个人名片",
		Settings:      map[string]any{},
		OwnerMemberID: &alpha.OwnerID,
		Enabled:       true,
	}
	if _, err := env.DB.NewInsert().Model(personalCh).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	var changedKinds []channel.Kind
	del := channel.NewDelete(env.DB, func(k channel.Kind) { changedKinds = append(changedKinds, k) })

	// 1. 跨工作区拦截与个人渠道保护
	if _, err := del.Handle(ctx, betaSub, wClean.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("跨工作区删除应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := del.Handle(ctx, alphaSub, personalCh.ID); !errors.Is(err, channel.ErrPersonalProtected) {
		t.Fatalf("个人渠道应返回 ErrPersonalProtected，实际 %v", err)
	}

	// 2. 无历史引用的插件应物理删除
	resClean, err := del.Handle(ctx, alphaSub, wClean.ID)
	if err != nil || resClean.Mode != "deleted" {
		t.Fatalf("无引用插件应物理删除，实际 %+v err=%v", resClean, err)
	}
	if _, err := channel.Find(ctx, env.DB, alpha.ID, wClean.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("物理删除后应查不到，实际 %v", err)
	}

	// 3. 有历史会话的插件应软归档（停用 + archived_at）
	resUsed, err := del.Handle(ctx, alphaSub, wUsed.ID)
	if err != nil || resUsed.Mode != "archived" {
		t.Fatalf("有引用插件应软归档，实际 %+v err=%v", resUsed, err)
	}
	if len(changedKinds) != 2 {
		t.Fatalf("删除与归档均应触发变更回调，实际 %+v", changedKinds)
	}
	archivedRow, err := channel.Find(ctx, env.DB, alpha.ID, wUsed.ID)
	if err != nil || archivedRow.Enabled || archivedRow.ArchivedAt == nil {
		t.Fatalf("归档后状态异常：%+v err=%v", archivedRow, err)
	}

	// 配置列表（widget.List）不再显示已归档插件，且禁止再修改
	widgetList, err := widget.NewList(env.DB).Handle(ctx, alphaSub, &widget.ListInput{})
	if err != nil || len(widgetList.Items) != 0 {
		t.Fatalf("归档插件应从配置列表隐藏：%+v err=%v", widgetList, err)
	}
	if _, err := widget.NewUpdate(env.DB).Handle(ctx, alphaSub, wUsed.ID, widget.UpdateBody{Changes: widget.Changes{Name: "改名"}, Enabled: true}); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("已归档插件不应允许再次修改，实际 %v", err)
	}

	// 只读列表（channel.List）保留已归档渠道且标记 Archived == true，供历史会话/报表筛选
	chList, err := channel.NewList(env.DB).Handle(ctx, alphaSub)
	if err != nil {
		t.Fatal(err)
	}
	foundArchived := false
	for _, it := range chList {
		if it.ID == wUsed.ID {
			foundArchived = it.Archived && !it.Enabled
		}
	}
	if !foundArchived {
		t.Fatalf("channel.List 应保留已归档渠道并标出 Archived=true：%+v", chList)
	}
}
