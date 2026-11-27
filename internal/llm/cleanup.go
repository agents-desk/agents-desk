package llm

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// CleanupCalls 返回按保留期 keep 分批清理 llm_calls 历史调用记录的清理函数，供 schedule.Cleanup 定期调用。
func CleanupCalls(db *bun.DB, keep time.Duration) func(ctx context.Context, limit int) (int, error) {
	if db == nil {
		panic("llm: nil database")
	}
	if keep <= 0 {
		panic("llm: keep must be positive")
	}
	return func(ctx context.Context, limit int) (int, error) {
		if limit <= 0 {
			return 0, errors.New("llm: cleanup limit must be positive")
		}
		cutoff := time.Now().UTC().Add(-keep)
		res, err := db.NewRaw(`DELETE FROM llm_calls WHERE id IN (
			SELECT id FROM llm_calls WHERE created_at < ? ORDER BY created_at ASC LIMIT ?
		)`, cutoff, limit).Exec(ctx)
		if err != nil {
			return 0, errors.Wrap(err, "cleanup llm calls")
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, errors.Wrap(err, "count cleaned llm calls")
		}
		return int(n), nil
	}
}
