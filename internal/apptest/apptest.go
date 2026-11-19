// Package apptest 是测试共用的装配：执行了全部迁移、预置了角色的临时库，两套账号的 login.Realm（logintest.Realm：
// 低成本的哈希、宽松的限流），临时目录中的文件服务，其余与入口的装配相同。
package apptest

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/filesystem/local"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/login/logintest"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/migrations"
)

// Env 是一个测试的数据库和账号体系。
type Env struct {
	DB           *bun.DB
	Roles        *authz.Store
	AdminLogins  *login.Realm // 平台管理员的 login.Realm
	MemberLogins *login.Realm // 工作区成员的 login.Realm
	Files        *filesystem.Service
	Disk         *local.Disk // Files 的本地磁盘，在测试的临时目录中，公开地址是 /storage
}

// New 为测试 t 创建一个执行了全部迁移、预置了角色的临时库，以及使用它的账号体系。需要环境变量 POSTGRES_TEST_URL。
func New(t testing.TB) *Env {
	t.Helper()
	db := dbtest.Open(t, migrations.FS)
	roles := authz.NewStore(db, auth.Catalog)
	if err := auth.EnsureRoles(context.Background(), roles); err != nil {
		t.Fatal(err)
	}
	realm := func(typ identity.SubjectType, resolve identity.Resolver) *login.Realm {
		return logintest.Realm(t, db, login.Options{
			Type: typ, Resolve: resolve,
			Policy: login.Policy{PasswordKinds: []string{login.KindEmail}},
		})
	}
	disk, err := local.Open(filesystem.LocalConfig{Root: t.TempDir(), PublicURL: "/storage"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	cfg := filesystem.Config{DefaultDisk: "local", MaxUploadSize: 20 * filesystem.MiB, TempTTL: time.Hour}
	files, err := filesystem.New(db, cfg, map[string]filesystem.Disk{"local": disk})
	if err != nil {
		t.Fatal(err)
	}
	return &Env{
		DB: db, Roles: roles, Files: files, Disk: disk,
		AdminLogins:  realm(platformadmin.Type, platformadmin.Resolve(db, roles)),
		MemberLogins: realm(member.Type, member.Resolve(db, roles)),
	}
}

// Admin 是测试中执行操作的平台管理员主体，拥有平台的全部权限。
var Admin = identity.Subject{
	Type: platformadmin.Type, ID: "01900000-0000-7000-8000-0000000000aa",
	Permissions: auth.PlatformAdmin.Permissions,
}
