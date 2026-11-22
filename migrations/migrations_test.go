package migrations_test

import (
	"testing"

	"agents-desk/migrations"

	"github.com/go-tenon/tenon/database/dbtest"
)

// TestSchema 检查所有迁移建立的表符合框架的时间列和表/字段注释约定。
// 全部表均受 CheckComments 约束；llm_calls 与 timeline_items 为只追加表，豁免 updated_at 时间列检查。
func TestSchema(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, migrations.FS)
	dbtest.CheckComments(t, db)
	dbtest.CheckSchema(t, db, "llm_calls", "timeline_items")
}
