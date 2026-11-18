// Package apptest 是测试共用的装配：执行了全部迁移的临时库、两套账号的 login.Realm 和应用密钥。
// 它按测试的需要调低密码哈希的成本、放宽限流，其余与入口的装配相同。
package apptest

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database/dbtest"
	"github.com/go-tenon/tenon/hashing"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/ratelimit"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/migrations"
)

// Env 是一个测试的数据库和账号体系。
type Env struct {
	DB           *bun.DB
	Crypt        *crypt.Crypt
	Roles        *authz.Store
	AdminLogins  *login.Realm // 平台管理员的 login.Realm
	MemberLogins *login.Realm // 工作区成员的 login.Realm
}

// New 为测试 t 创建一个执行了全部迁移的临时库，以及使用它的账号体系。需要环境变量 POSTGRES_TEST_URL。
func New(t testing.TB) *Env {
	t.Helper()
	db := dbtest.Open(t, migrations.FS)
	keys := Crypt()
	hcfg := hashing.Config{}
	hcfg.SetDefaults()
	hcfg.Driver, hcfg.Bcrypt.Cost = "bcrypt", 4 // 生产配置的哈希很慢，测试用 bcrypt 的最低成本
	hasher := hashing.New(hcfg)
	loose := ratelimit.NewMemory(ratelimit.Config{Rate: 100, Interval: time.Minute, Burst: 100})
	roles := authz.NewStore(db, auth.Catalog)
	realm := func(typ identity.SubjectType, resolve identity.Resolver) *login.Realm {
		return login.New(db, login.Options{
			Type: typ, Hasher: hasher, Crypt: keys, Resolve: resolve,
			Policy:   login.Policy{PasswordKinds: []string{login.KindEmail}},
			Attempts: loose, Sends: loose,
		})
	}
	return &Env{
		DB: db, Crypt: keys, Roles: roles,
		AdminLogins:  realm(platformadmin.Type, platformadmin.Resolve(db, roles)),
		MemberLogins: realm(member.Type, nil),
	}
}

// Crypt 返回使用固定测试密钥的 Crypt。
func Crypt() *crypt.Crypt {
	return crypt.New(crypt.Config{Key: base64.StdEncoding.EncodeToString(make([]byte, 32))})
}

// Admin 是测试中执行操作的平台管理员主体，拥有平台的全部权限。
var Admin = identity.Subject{
	Type: platformadmin.Type, ID: "01900000-0000-7000-8000-0000000000aa",
	Permissions: []string{string(auth.TenantsRead), string(auth.TenantsManage), string(auth.PlatformSettingsManage)},
}
