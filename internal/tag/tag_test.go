package tag_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/tag"
	"agents-desk/internal/tenant"
)

func TestTagCRUDAndIsolation(t *testing.T) {
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

	create, get, list, update, del := tag.NewCreate(env.DB), tag.NewGet(env.DB), tag.NewList(env.DB), tag.NewUpdate(env.DB), tag.NewDelete(env.DB)

	// 新建：不传颜色时默认 blue-gray
	vip, err := create.Handle(ctx, inA, tag.Changes{Name: "VIP", Description: "重要客户"})
	if err != nil {
		t.Fatalf("新建标签：%v", err)
	}
	if vip.Color != tag.BlueGray || vip.Name != "VIP" || vip.Description != "重要客户" {
		t.Fatalf("新建结果不符合预期：%+v", vip)
	}

	// 同工作区按小写去重；不同工作区允许同名
	if _, err := create.Handle(ctx, inA, tag.Changes{Name: "vip"}); !errors.Is(err, tag.ErrNameTaken) {
		t.Fatalf("重名应返回 ErrNameTaken：%v", err)
	}
	betaVIP, err := create.Handle(ctx, inB, tag.Changes{Name: "VIP", Color: tag.Yellow})
	if err != nil {
		t.Fatalf("别的工作区同名应成功：%v", err)
	}

	// 列表与名称筛选
	if _, err := create.Handle(ctx, inA, tag.Changes{Name: "退款", Color: tag.DeepOrange}); err != nil {
		t.Fatal(err)
	}
	page, err := list.Handle(ctx, inA, &tag.ListInput{})
	if err != nil || len(page.Items) != 2 || page.Total != 2 {
		t.Fatalf("列表：%v %+v", err, page)
	}
	filtered, err := list.Handle(ctx, inA, &tag.ListInput{Name: "退"})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Name != "退款" {
		t.Fatalf("按名称筛选：%v %+v", err, filtered)
	}

	// 修改
	updated, err := update.Handle(ctx, inA, vip.ID, tag.Changes{Name: "VIP 客户", Color: tag.Yellow, Description: "高价值"})
	if err != nil || updated.Name != "VIP 客户" || updated.Color != tag.Yellow {
		t.Fatalf("修改标签：%v %+v", err, updated)
	}
	if _, err := update.Handle(ctx, inA, vip.ID, tag.Changes{Name: "退款"}); !errors.Is(err, tag.ErrNameTaken) {
		t.Fatalf("修改为已存在名称应返回 ErrNameTaken：%v", err)
	}

	// 跨工作区隔离
	if _, err := get.Handle(ctx, inA, betaVIP.ID); !errors.Is(err, tag.ErrNotFound) {
		t.Fatalf("读取别的工作区标签：%v", err)
	}
	if _, err := update.Handle(ctx, inA, betaVIP.ID, tag.Changes{Name: "x"}); !errors.Is(err, tag.ErrNotFound) {
		t.Fatalf("修改别的工作区标签：%v", err)
	}
	if err := del.Handle(ctx, inA, betaVIP.ID); !errors.Is(err, tag.ErrNotFound) {
		t.Fatalf("删除别的工作区标签：%v", err)
	}

	// 删除
	if err := del.Handle(ctx, inA, vip.ID); err != nil {
		t.Fatalf("删除标签：%v", err)
	}
	if _, err := get.Handle(ctx, inA, vip.ID); !errors.Is(err, tag.ErrNotFound) {
		t.Fatalf("删除后读取：%v", err)
	}
}
