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
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/tag"
)

// TestDeleteAndAnonymizeContactAndIsolation 验证联系人删除（无历史物理删除）与 GDPR 匿名化（有历史清空 PII+吊销令牌）及跨工作区隔离。
func TestDeleteAndAnonymizeContactAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setupWorkspace(t, env, "del-alpha")
	beta := setupWorkspace(t, env, "del-beta")

	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour})
	del := contact.NewDelete(env.DB, tokens)

	// 1. 创建无历史会话联系人与有历史会话联系人
	var cClean, cWithConv *contact.Contact
	err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		cClean, _, err = contact.ResolveIdentity(ctx, tx, alpha.tenant, alpha.channel.ID, "ext-clean", contact.Profile{
			Name: "Clean User", Email: "clean@example.com",
		})
		if err != nil {
			return err
		}
		cWithConv, err = contact.ResolveUser(ctx, tx, alpha.tenant, alpha.channel.ID, "ext-hist", "u-hist", contact.Profile{
			Name: "History User", Email: "hist@example.com", Company: "Acme Corp",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// 为有历史联系人打标签、写备注、创建会话、签发访问令牌
	tg, err := tag.NewCreate(env.DB).Handle(ctx, alpha.agent, tag.Changes{Name: "VIP", Color: "green"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		_, _, err := contact.AddTag(ctx, tx, alpha.tenant, cWithConv.ID, tg.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := contact.NewUpdate(env.DB, env.Roles).Handle(ctx, alpha.agent, cWithConv.ID, contact.UpdateBody{
		Location: strPtr("Shanghai"),
		Notes:    strPtr("高价值客户手机号 13800000000"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{}).Handle(ctx, alpha.system, conversation.ReceiveParams{
		ChannelID: alpha.channel.ID, From: "ext-hist", Text: "需要发票",
	}); err != nil {
		t.Fatal(err)
	}
	tokClean, err := tokens.Login(ctx, identity.Subject{Type: contact.Type, ID: cClean.ID, Tenant: alpha.tenant}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	tokHist, err := tokens.Login(ctx, identity.Subject{Type: contact.Type, ID: cWithConv.ID, Tenant: alpha.tenant}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}

	// 2. 跨工作区不能删除或匿名化
	if _, err := del.Handle(ctx, beta.agent, cClean.ID); !errors.Is(err, contact.ErrNotFound) {
		t.Fatalf("跨工作区删除应返回 ErrNotFound，实际 %v", err)
	}

	// 3. 删除无历史联系人 -> 物理删除，令牌失效
	resClean, err := del.Handle(ctx, alpha.agent, cClean.ID)
	if err != nil || resClean.Mode != "deleted" {
		t.Fatalf("无历史联系人应物理删除，实际 %+v err=%v", resClean, err)
	}
	if _, err := contact.Find(ctx, env.DB, alpha.tenant, cClean.ID); !errors.Is(err, contact.ErrNotFound) {
		t.Fatalf("物理删除后应查不到联系人，实际 err=%v", err)
	}
	if _, err := tokens.Validate(ctx, tokClean); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("删除联系人后令牌应立即失效，实际 %v", err)
	}

	// 4. 删除有历史会话联系人 -> 不可逆匿名化，清空所有 PII、身份、标签并吊销令牌
	resHist, err := del.Handle(ctx, alpha.agent, cWithConv.ID)
	if err != nil || resHist.Mode != "anonymized" {
		t.Fatalf("有历史联系人应匿名化，实际 %+v err=%v", resHist, err)
	}
	if _, err := tokens.Validate(ctx, tokHist); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("匿名化后令牌应立即失效，实际 %v", err)
	}
	anon, err := contact.Find(ctx, env.DB, alpha.tenant, cWithConv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if anon.Name != "" || anon.Email != "" || anon.Company != "" || anon.Location != "" || anon.Notes != "" || anon.ExternalUserID != "" {
		t.Fatalf("匿名化后应清空全部 PII 字段，实际 %+v", anon)
	}
	if anon.BlockedAt == nil || anon.BlockedReason != "anonymized" {
		t.Fatalf("匿名化后应封禁并标记 anonymized：%+v", anon)
	}
	var idents, tagLinks int
	if err := env.DB.NewRaw("SELECT COUNT(*) FROM contact_identities WHERE contact_id = ?", cWithConv.ID).Scan(ctx, &idents); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.NewRaw("SELECT COUNT(*) FROM contact_tags WHERE contact_id = ?", cWithConv.ID).Scan(ctx, &tagLinks); err != nil {
		t.Fatal(err)
	}
	if idents != 0 || tagLinks != 0 {
		t.Fatalf("匿名化后应清空身份与标签关联：idents=%d tags=%d", idents, tagLinks)
	}
}
