package llm

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"agents-desk/internal/apptest"
)

// TestCleanupCalls 验证 CleanupCalls 按保留期和单批上限分批删除过期调用日志，同时保留未过期记录。
func TestCleanupCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	old1 := now.Add(-100 * 24 * time.Hour)
	old2 := now.Add(-95 * 24 * time.Hour)
	fresh := now.Add(-10 * 24 * time.Hour)

	for _, c := range []Call{
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: old1, UpdatedAt: old1, CallType: Translation, Operation: "incoming", Meta: map[string]any{}, Endpoint: "http://gw", Model: "m", Request: map[string]any{}},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: old2, UpdatedAt: old2, CallType: Translation, Operation: "incoming", Meta: map[string]any{}, Endpoint: "http://gw", Model: "m", Request: map[string]any{}},
		{ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: fresh, UpdatedAt: fresh, CallType: Translation, Operation: "incoming", Meta: map[string]any{}, Endpoint: "http://gw", Model: "m", Request: map[string]any{}},
	} {
		if _, err := env.DB.NewInsert().Model(&c).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	clean := CleanupCalls(env.DB, 90*24*time.Hour)
	n1, err := clean(ctx, 1)
	if err != nil || n1 != 1 {
		t.Fatalf("首批应受 limit=1 限制只删 1 条，实际 n=%d err=%v", n1, err)
	}
	n2, err := clean(ctx, 10)
	if err != nil || n2 != 1 {
		t.Fatalf("第二批应删掉剩余 1 条过期记录，实际 n=%d err=%v", n2, err)
	}
	n3, err := clean(ctx, 10)
	if err != nil || n3 != 0 {
		t.Fatalf("未过期记录不应被删除，实际 n=%d err=%v", n3, err)
	}
	var left int
	if err := env.DB.NewRaw(`SELECT count(*) FROM llm_calls`).Scan(ctx, &left); err != nil || left != 1 {
		t.Fatalf("应仅剩 1 条近期记录，实际 %d err=%v", left, err)
	}
}
