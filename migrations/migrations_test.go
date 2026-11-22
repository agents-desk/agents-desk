package migrations_test

import (
	"testing"

	"agents-desk/migrations"

	"github.com/go-tenon/tenon/database/dbtest"
)

// TestSchema 检查所有迁移建立的表符合框架的时间列和表/字段注释约定。
// 全部表均受 CheckComments 约束；早期已发布的 5 张表因历史列序或只追加设计豁免时间列顺序检查，后续新表不再豁免。
func TestSchema(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, migrations.FS)
	dbtest.CheckComments(t, db)
	dbtest.CheckSchema(t, db, "llm_calls", "members", "platform_admins", "tenants", "timeline_items")
}
