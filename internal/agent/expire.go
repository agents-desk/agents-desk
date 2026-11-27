package agent

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
)

// ExpireGenerations 把超过有效期仍未结束的生成标记为失败：模型调用卡住或 worker 退出时，气泡不会一直停在生成中。
// 返回本次标记的数量；每次最多处理 limit 条，由定时任务反复调用。
func ExpireGenerations(ctx context.Context, db bun.IDB, at time.Time, limit int) (int, error) {
	res, err := db.NewRaw(`UPDATE generations SET state = ?, error = ?, updated_at = ?
		WHERE id IN (
			SELECT id FROM generations
			WHERE state IN (?) AND expires_at < ?
			ORDER BY expires_at LIMIT ?
			FOR UPDATE SKIP LOCKED
		)`, Failed, "expired", at, bun.In([]State{Pending, Streaming, WaitingApproval}), at, limit).Exec(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "expire generations")
	}
	n, err := res.RowsAffected()
	return int(n), errors.Wrap(err, "expire generations")
}
