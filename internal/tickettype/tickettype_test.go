package tickettype_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/attribute"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
	"agents-desk/internal/tickettype"
)

func TestTicketTypeCRUDFieldsAndIsolation(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "A", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "B", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	inA := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	inB := identity.Subject{Type: identity.System, ID: "test", Tenant: beta.ID}

	teamA, err := team.NewCreate(env.DB).Handle(ctx, inA, team.Changes{Name: "支付组", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}
	teamB, err := team.NewCreate(env.DB).Handle(ctx, inB, team.Changes{Name: "B组", Assignment: team.Manual})
	if err != nil {
		t.Fatal(err)
	}

	create := tickettype.NewCreate(env.DB)
	get := tickettype.NewGet(env.DB)
	list := tickettype.NewList(env.DB)
	update := tickettype.NewUpdate(env.DB)
	reorder := tickettype.NewReorder(env.DB)
	disable := tickettype.NewDisable(env.DB)
	enable := tickettype.NewEnable(env.DB)
	del := tickettype.NewDelete(env.DB)

	// 跨工作区团队作为默认团队应拒绝
	if _, err := create.Handle(ctx, inA, tickettype.CreateParams{
		Name: "跨租户", DefaultTeamID: &teamB.ID,
	}); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("跨工作区默认团队应返回 team.ErrNotFound：%v", err)
	}

	// 新建两个工单类型
	billing, err := create.Handle(ctx, inA, tickettype.CreateParams{
		Name: "充值问题", Description: "处理支付掉单", DefaultTeamID: &teamA.ID, DefaultVisibility: tickettype.VisibilityAll,
	})
	if err != nil || billing.DefaultTeamName != "支付组" || billing.Status != tickettype.Active {
		t.Fatalf("新建工单类型：%v %+v", err, billing)
	}
	internal, err := create.Handle(ctx, inA, tickettype.CreateParams{
		Name: "内部审批", DefaultVisibility: tickettype.VisibilityMembers,
	})
	if err != nil || internal.DefaultVisibility != tickettype.VisibilityMembers {
		t.Fatalf("新建内部审批类型：%v %+v", err, internal)
	}

	// 同工作区忽略大小写重名冲突；不同工作区允许重名
	if _, err := create.Handle(ctx, inA, tickettype.CreateParams{Name: "充值问题"}); !errors.Is(err, tickettype.ErrNameTaken) {
		t.Fatalf("同工作区重名应返回 ErrNameTaken：%v", err)
	}
	betaType, err := create.Handle(ctx, inB, tickettype.CreateParams{Name: "充值问题"})
	if err != nil {
		t.Fatalf("其他工作区同名应成功：%v", err)
	}

	// 排序与跨工作区隔离
	reordered, err := reorder.Handle(ctx, inA, tickettype.ReorderParams{IDs: []string{internal.ID, billing.ID}})
	if err != nil || len(reordered) != 2 || reordered[0].ID != internal.ID || reordered[1].ID != billing.ID {
		t.Fatalf("排序结果异常：%v %+v", err, reordered)
	}
	if _, err := reorder.Handle(ctx, inA, tickettype.ReorderParams{IDs: []string{betaType.ID}}); !errors.Is(err, tickettype.ErrNotFound) {
		t.Fatalf("跨工作区排序应返回 ErrNotFound：%v", err)
	}
	if _, err := get.Handle(ctx, inA, betaType.ID); !errors.Is(err, tickettype.ErrNotFound) {
		t.Fatalf("跨工作区读取应返回 ErrNotFound：%v", err)
	}

	// 按工单类型隔离自定义字段：两个不同工单类型可以用相同的 key，且互不影响 ApplyTicketPatch
	attrCreate := attribute.NewCreate(env.DB)
	attrList := attribute.NewList(env.DB)
	if _, err := attrCreate.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetTicket, Key: "order_no", Name: "缺类型", Type: attribute.TypeText,
	}); !errors.Is(err, attribute.ErrTicketTypeRequired) {
		t.Fatalf("工单属性缺类型应返回 ErrTicketTypeRequired：%v", err)
	}
	if _, err := attrCreate.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetTicket, TicketTypeID: &betaType.ID, Key: "order_no", Name: "跨租户", Type: attribute.TypeText,
	}); !errors.Is(err, attribute.ErrTicketTypeInvalid) {
		t.Fatalf("跨租户工单类型应返回 ErrTicketTypeInvalid：%v", err)
	}
	if _, err := attrCreate.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetConversation, TicketTypeID: &billing.ID, Key: "order_no", Name: "非工单带类型", Type: attribute.TypeText,
	}); !errors.Is(err, attribute.ErrTicketTypeInvalid) {
		t.Fatalf("非工单属性传 ticket_type_id 应返回 ErrTicketTypeInvalid：%v", err)
	}

	if _, err := attrCreate.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetTicket, TicketTypeID: &billing.ID, Key: "order_no", Name: "订单号", Type: attribute.TypeText,
	}); err != nil {
		t.Fatalf("新建充值字段：%v", err)
	}
	if _, err := attrCreate.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetTicket, TicketTypeID: &billing.ID, Key: "order_no", Name: "重复", Type: attribute.TypeText,
	}); !errors.Is(err, attribute.ErrKeyTaken) {
		t.Fatalf("同工单类型同 key 应冲突：%v", err)
	}
	if _, err := attrCreate.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetTicket, TicketTypeID: &internal.ID, Key: "order_no", Name: "审批单号", Type: attribute.TypeText,
	}); err != nil {
		t.Fatalf("不同工单类型同 key 应允许：%v", err)
	}

	gotBilling, err := get.Handle(ctx, inA, billing.ID)
	if err != nil || gotBilling.FieldCount != 1 {
		t.Fatalf("字段统计应为 1：%v %+v", err, gotBilling)
	}
	fields, err := attrList.Handle(ctx, inA, &attribute.ListInput{Target: attribute.TargetTicket, TicketTypeID: billing.ID})
	if err != nil || len(fields) != 1 || fields[0].Key != "order_no" {
		t.Fatalf("按工单类型列出字段：%v %+v", err, fields)
	}
	nextAttrs, changes, err := attribute.ApplyTicketPatch(ctx, env.DB, alpha.ID, billing.ID, map[string]any{}, map[string]any{"order_no": "UPI-100"})
	if err != nil || len(changes) != 1 || nextAttrs["order_no"] != "UPI-100" {
		t.Fatalf("ApplyTicketPatch：%v %+v %+v", err, nextAttrs, changes)
	}

	// 被工单类型引用的团队收件箱不能删除
	if err := team.NewDelete(env.DB).Handle(ctx, inA, teamA.ID); !errors.Is(err, team.ErrInUse) {
		t.Fatalf("被工单类型引用的团队收件箱应不能删除：%v", err)
	}

	// 停用与启用、EnabledOnly 过滤、FindActive
	dis, err := disable.Handle(ctx, inA, billing.ID)
	if err != nil || dis.Status != tickettype.Disabled {
		t.Fatalf("停用工单类型：%v %+v", err, dis)
	}
	if _, err := tickettype.FindActive(ctx, env.DB, alpha.ID, billing.ID); !errors.Is(err, tickettype.ErrDisabled) {
		t.Fatalf("FindActive 应拒绝已停用类型：%v", err)
	}
	activeList, err := list.Handle(ctx, inA, &tickettype.ListInput{EnabledOnly: true})
	if err != nil || len(activeList) != 1 || activeList[0].ID != internal.ID {
		t.Fatalf("EnabledOnly 列表异常：%v %+v", err, activeList)
	}
	en, err := enable.Handle(ctx, inA, billing.ID)
	if err != nil || en.Status != tickettype.Active {
		t.Fatalf("重新启用工单类型：%v %+v", err, en)
	}

	// 停用团队后，不改变默认团队仍可更新说明，但换成已停用团队应拒绝
	if _, err := team.NewDisable(env.DB).Handle(ctx, inA, teamA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Handle(ctx, inA, billing.ID, tickettype.UpdateParams{
		Name: "充值问题", Description: "更新说明", DefaultTeamID: &teamA.ID, DefaultVisibility: tickettype.VisibilityAll,
	}); err != nil {
		t.Fatalf("保持原已停用默认团队应允许更新：%v", err)
	}
	if _, err := update.Handle(ctx, inA, internal.ID, tickettype.UpdateParams{
		Name: "内部审批", DefaultTeamID: &teamA.ID, DefaultVisibility: tickettype.VisibilityMembers,
	}); !errors.Is(err, team.ErrDisabled) {
		t.Fatalf("新选已停用默认团队应返回 team.ErrDisabled：%v", err)
	}

	// 删除无引用的工单类型（连带级联删除其自定义字段）
	if err := del.Handle(ctx, inA, internal.ID); err != nil {
		t.Fatalf("删除工单类型：%v", err)
	}
	leftFields, err := attrList.Handle(ctx, inA, &attribute.ListInput{Target: attribute.TargetTicket, TicketTypeID: internal.ID})
	if err != nil || len(leftFields) != 0 {
		t.Fatalf("删除工单类型后字段应级联清空：%v %+v", err, leftFields)
	}
}
