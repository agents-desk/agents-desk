package contact_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/contact"
)

// TestBlockUnblockContactAndIsolation 验证联系人拉黑与解除拉黑、网页令牌吊销及跨工作区隔离。
func TestBlockUnblockContactAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setupWorkspace(t, env, "blk-alpha")
	beta := setupWorkspace(t, env, "blk-beta")

	var target *contact.Contact
	err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		target, _, err = contact.ResolveIdentity(ctx, tx, alpha.tenant, alpha.channel.ID, "ext-spam-1", contact.Profile{Name: "Spam Lead"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour})
	tok, err := tokens.Login(ctx, identity.Subject{Type: contact.Type, ID: target.ID, Tenant: alpha.tenant}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}

	block := contact.NewBlock(env.DB, tokens)
	unblock := contact.NewUnblock(env.DB)

	// 跨工作区无法拉黑或解除
	if _, err := block.Handle(ctx, beta.agent, target.ID, contact.BlockBody{Reason: "cross"}); !errors.Is(err, contact.ErrNotFound) {
		t.Fatalf("跨工作区拉黑应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := unblock.Handle(ctx, beta.agent, target.ID); !errors.Is(err, contact.ErrNotFound) {
		t.Fatalf("跨工作区解除拉黑应返回 ErrNotFound，实际 %v", err)
	}

	// 正常拉黑：写入 blocked_at/blocked_reason 并吊销已有访问令牌
	blocked, err := block.Handle(ctx, alpha.agent, target.ID, contact.BlockBody{Reason: " 恶意发送广告 "})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.BlockedAt == nil || blocked.BlockedReason != "恶意发送广告" {
		t.Fatalf("拉黑状态异常：%+v", blocked)
	}
	if _, err := tokens.Validate(ctx, tok); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("拉黑后令牌应立即失效，实际 %v", err)
	}

	var eventName string
	if err := env.DB.NewRaw("SELECT event FROM activity_log WHERE target_id = ? ORDER BY created_at DESC LIMIT 1", target.ID).Scan(ctx, &eventName); err != nil {
		t.Fatal(err)
	}
	if eventName != "contact.blocked" {
		t.Fatalf("应记录 contact.blocked 审计日志，实际 %q", eventName)
	}

	// 解除拉黑：清空拉黑字段并记录 contact.unblocked
	unblocked, err := unblock.Handle(ctx, alpha.agent, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unblocked.BlockedAt != nil || unblocked.BlockedReason != "" {
		t.Fatalf("解除拉黑后应清空状态：%+v", unblocked)
	}
	if err := env.DB.NewRaw("SELECT event FROM activity_log WHERE target_id = ? ORDER BY created_at DESC LIMIT 1", target.ID).Scan(ctx, &eventName); err != nil {
		t.Fatal(err)
	}
	if eventName != "contact.unblocked" {
		t.Fatalf("应记录 contact.unblocked 审计日志，实际 %q", eventName)
	}
}
