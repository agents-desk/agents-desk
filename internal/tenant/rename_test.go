package tenant_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

func TestRenameLogo(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	created, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, addr).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Acme", Slug: "acme", OwnerEmail: "owner@acme.example", OwnerName: "Owner", OwnerPassword: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := identity.Subject{
		Type:        member.Type,
		ID:          created.OwnerID,
		Tenant:      created.ID,
		Permissions: []string{string(auth.TenantSettingsManage)},
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	uploaded, err := env.Files.Upload(ctx, owner, filesystem.FileInput{
		Filename: "workspace.png", ContentType: "image/png", Size: int64(pngBuf.Len()),
	}, bytes.NewReader(pngBuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	// 上传并设置工作区 Logo
	logoID := uploaded.ID.String()
	cur, err := tenant.NewRename(env.DB, env.Files).Handle(ctx, owner, tenant.GeneralSettings{
		Name: "Acme Support", LogoFileID: &logoID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cur.LogoFileID != logoID || !strings.HasPrefix(cur.LogoURL, "/storage/") {
		t.Fatalf("工作区 Logo 应已生效：%+v", cur)
	}

	// Mine 列表返回工作区 Logo
	mine, err := tenant.NewMine(env.DB, env.Roles, env.Files, addr).Handle(ctx, identity.Subject{Type: member.Type, ID: created.OwnerID})
	if err != nil || len(mine) != 1 || mine[0].LogoURL != cur.LogoURL {
		t.Fatalf("工作区列表应包含 LogoURL：%+v err=%v", mine, err)
	}

	// 不传 LogoFileID 时保留原有 Logo
	cur, err = tenant.NewRename(env.DB, env.Files).Handle(ctx, owner, tenant.GeneralSettings{Name: "Acme Help"})
	if err != nil || cur.LogoFileID != logoID {
		t.Fatalf("未传 logo_file_id 时应保留原有 Logo：%+v err=%v", cur, err)
	}

	// 传空字符串清除 Logo
	empty := ""
	cur, err = tenant.NewRename(env.DB, env.Files).Handle(ctx, owner, tenant.GeneralSettings{Name: "Acme Help", LogoFileID: &empty})
	if err != nil || cur.LogoFileID != "" || cur.LogoURL != "" {
		t.Fatalf("传空字符串后应清除 Logo：%+v err=%v", cur, err)
	}
}
