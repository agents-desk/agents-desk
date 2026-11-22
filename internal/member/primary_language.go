package member

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// PrimaryLanguages 返回工作区 tenant 的主要语言：没有停用的成员中用的人最多的语言，人数一样多时都算，按字母排列。
// 收到的消息预先翻译成这些语言，同时也是全文检索索引译文所采用的语言口径；还没有成员时返回空。
func PrimaryLanguages(ctx context.Context, db bun.IDB, tenant string) ([]string, error) {
	var langs []string
	err := db.NewRaw(`WITH counts AS (
			SELECT m.language, COUNT(DISTINCT m.id) AS n FROM members m
			JOIN authz_assignments a ON a.subject_type = 'member' AND a.subject_id = m.id::text AND a.tenant = ?
			WHERE m.disabled_at IS NULL GROUP BY m.language)
		SELECT language FROM counts WHERE n = (SELECT MAX(n) FROM counts) ORDER BY language`, tenant).Scan(ctx, &langs)
	return langs, errors.Wrap(err, "load primary languages")
}
