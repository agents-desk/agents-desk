package attribute_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/attribute"
	"agents-desk/internal/tenant"
)

func TestAttributeCRUDAndPatch(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	createTenant := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{})
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

	create, get, list, update, del := attribute.NewCreate(env.DB), attribute.NewGet(env.DB), attribute.NewList(env.DB), attribute.NewUpdate(env.DB), attribute.NewDelete(env.DB)

	// 单选/多选必须有选项且不能重复
	if _, err := create.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "cat", Name: "分类", Type: attribute.TypeSelect,
	}); !errors.Is(err, attribute.ErrOptionsRequired) {
		t.Fatalf("缺少选项：%v", err)
	}
	if _, err := create.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "cat", Name: "分类", Type: attribute.TypeSelect, Options: []string{"A", "A"},
	}); !errors.Is(err, attribute.ErrDuplicateOption) {
		t.Fatalf("重复选项：%v", err)
	}

	// 新建 6 种类型的会话属性
	defs := []attribute.CreateParams{
		{Target: attribute.TargetConversation, Key: "order_no", Name: "订单号", Type: attribute.TypeText},
		{Target: attribute.TargetConversation, Key: "amount", Name: "金额", Type: attribute.TypeNumber},
		{Target: attribute.TargetConversation, Key: "urgent", Name: "加急", Type: attribute.TypeBoolean},
		{Target: attribute.TargetConversation, Key: "due_date", Name: "截止日期", Type: attribute.TypeDate},
		{Target: attribute.TargetConversation, Key: "category", Name: "分类", Type: attribute.TypeSelect, Options: []string{"物流", "退款"}},
		{Target: attribute.TargetConversation, Key: "channels", Name: "涉及渠道", Type: attribute.TypeMultiSelect, Options: []string{"官网", "App", "小程序"}},
	}
	var firstID string
	for i, d := range defs {
		created, err := create.Handle(ctx, inA, d)
		if err != nil {
			t.Fatalf("创建 %s：%v", d.Key, err)
		}
		if i == 0 {
			firstID = created.ID
		}
	}

	// 同对象同 key 冲突；不同对象和不同工作区允许同 key
	if _, err := create.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "order_no", Name: "重复", Type: attribute.TypeText,
	}); !errors.Is(err, attribute.ErrKeyTaken) {
		t.Fatalf("同对象重名 key：%v", err)
	}
	if _, err := create.Handle(ctx, inA, attribute.CreateParams{
		Target: attribute.TargetContact, Key: "order_no", Name: "联系人订单号", Type: attribute.TypeText,
	}); err != nil {
		t.Fatalf("不同对象同 key 应成功：%v", err)
	}
	betaDef, err := create.Handle(ctx, inB, attribute.CreateParams{
		Target: attribute.TargetConversation, Key: "order_no", Name: "B订单号", Type: attribute.TypeText,
	})
	if err != nil {
		t.Fatalf("别的工作区同 key 应成功：%v", err)
	}

	// 列表按 target 过滤，以及批量调整顺序
	convList, err := list.Handle(ctx, inA, &attribute.ListInput{Target: attribute.TargetConversation})
	if err != nil || len(convList) != 6 {
		t.Fatalf("会话属性列表：%v %d", err, len(convList))
	}
	reorder := attribute.NewReorder(env.DB)
	reordered, err := reorder.Handle(ctx, inA, attribute.ReorderParams{
		Target: attribute.TargetConversation,
		IDs:    []string{convList[2].ID, convList[0].ID, convList[1].ID},
	})
	if err != nil || len(reordered) != 6 || reordered[0].ID != convList[2].ID || reordered[1].ID != convList[0].ID || reordered[2].ID != convList[1].ID {
		t.Fatalf("调整顺序：%v %+v", err, reordered)
	}
	if _, err := reorder.Handle(ctx, inA, attribute.ReorderParams{
		Target: attribute.TargetConversation,
		IDs:    []string{betaDef.ID},
	}); !errors.Is(err, attribute.ErrNotFound) {
		t.Fatalf("跨工作区调整顺序应返回 ErrNotFound：%v", err)
	}
	allList, err := list.Handle(ctx, inA, &attribute.ListInput{})
	if err != nil || len(allList) != 7 {
		t.Fatalf("全部属性列表：%v %d", err, len(allList))
	}

	// ApplyPatch：正常写入、无变化不生成变更、清空属性
	next, changes, err := attribute.ApplyPatch(ctx, env.DB, alpha.ID, attribute.TargetConversation, map[string]any{}, map[string]any{
		"order_no": "ORD-1",
		"amount":   99.5,
		"urgent":   true,
		"due_date": "2026-12-01",
		"category": "物流",
		"channels": []any{"官网", "App", "官网"},
	})
	if err != nil || len(changes) != 6 {
		t.Fatalf("首次写入：%v %+v", err, changes)
	}
	if got := next["channels"].([]string); len(got) != 2 || got[0] != "官网" || got[1] != "App" {
		t.Fatalf("多选未去重：%#v", got)
	}
	// 相同值再写一次无变化
	_, noChanges, err := attribute.ApplyPatch(ctx, env.DB, alpha.ID, attribute.TargetConversation, next, map[string]any{
		"order_no": "ORD-1",
		"amount":   float64(99.5),
		"channels": []string{"官网", "App"},
	})
	if err != nil || len(noChanges) != 0 {
		t.Fatalf("无变化应返回空变更：%v %+v", err, noChanges)
	}
	// 清空属性（null、空字符串、空切片）
	cleared, clearChanges, err := attribute.ApplyPatch(ctx, env.DB, alpha.ID, attribute.TargetConversation, next, map[string]any{
		"order_no": "",
		"amount":   nil,
		"channels": []string{},
	})
	if err != nil || len(clearChanges) != 3 {
		t.Fatalf("清空属性：%v %+v", err, clearChanges)
	}
	if _, ok := cleared["order_no"]; ok {
		t.Fatalf("order_no 应已删除：%#v", cleared)
	}

	// 非法 key 与非法值
	if _, _, err := attribute.ApplyPatch(ctx, env.DB, alpha.ID, attribute.TargetConversation, next, map[string]any{"nope": "x"}); !errors.Is(err, attribute.ErrUnknownKey) {
		t.Fatalf("未知 key：%v", err)
	}
	for name, bad := range map[string]map[string]any{
		"number as string":  {"amount": "123"},
		"invalid date":      {"due_date": "2026/12/01"},
		"invalid option":    {"category": "不存在"},
		"invalid multi":     {"channels": []string{"官网", "未知"}},
		"boolean as string": {"urgent": "yes"},
	} {
		if _, _, err := attribute.ApplyPatch(ctx, env.DB, alpha.ID, attribute.TargetConversation, next, bad); !errors.Is(err, attribute.ErrInvalidValue) {
			t.Errorf("%s 应返回 ErrInvalidValue：%v", name, err)
		}
	}

	// 更新与跨工作区隔离
	pos := 10
	upd, err := update.Handle(ctx, inA, firstID, attribute.UpdateParams{Name: "业务订单号", Description: "说明", Position: &pos})
	if err != nil || upd.Name != "业务订单号" || upd.Position != 10 {
		t.Fatalf("更新属性：%v %+v", err, upd)
	}
	if _, err := get.Handle(ctx, inA, betaDef.ID); !errors.Is(err, attribute.ErrNotFound) {
		t.Fatalf("跨工作区读取：%v", err)
	}
	if _, err := update.Handle(ctx, inA, betaDef.ID, attribute.UpdateParams{Name: "x"}); !errors.Is(err, attribute.ErrNotFound) {
		t.Fatalf("跨工作区更新：%v", err)
	}
	if err := del.Handle(ctx, inA, betaDef.ID); !errors.Is(err, attribute.ErrNotFound) {
		t.Fatalf("跨工作区删除：%v", err)
	}
	if err := del.Handle(ctx, inA, firstID); err != nil {
		t.Fatalf("删除属性：%v", err)
	}
	if _, err := get.Handle(ctx, inA, firstID); !errors.Is(err, attribute.ErrNotFound) {
		t.Fatalf("删除后读取：%v", err)
	}
}
