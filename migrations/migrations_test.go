package migrations_test

import (
	"testing"

	"agents-desk/migrations"

	"github.com/go-tenon/tenon/database/dbtest"
)

// TestSchema 检查所有迁移建立的表符合框架的时间列和表/字段注释约定。
func TestSchema(t *testing.T) {
	t.Parallel()
	db := dbtest.Open(t, migrations.FS)
	dbtest.CheckSchema(t, db)
}
