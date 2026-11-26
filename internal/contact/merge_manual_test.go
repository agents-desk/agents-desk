package contact_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/member"
)

func TestManualMergeContacts(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	ws := setupWorkspace(t, env, "merge-co")
	other := setupWorkspace(t, env, "other-co")
	tenantID, agent, sys := ws.tenant, ws.agent, ws.system

	chA, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Widget, Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	chB, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Telegram, Name: "Telegram"})
	if err != nil {
		t.Fatal(err)
	}

	var leadTarget, userSource, userConflict *contact.Contact
	if err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		leadTarget, _, err = contact.ResolveIdentity(ctx, tx, tenantID, chA.ID, "v-1", contact.Profile{Name: "访客 A"})
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*contact.Contact)(nil)).Set("notes = ?", "来自官网").Where("id = ?", leadTarget.ID).Exec(ctx); err != nil {
			return err
		}
		userSource, err = contact.ResolveUser(ctx, tx, tenantID, chB.ID, "tg-1", "ext-u-1", contact.Profile{Name: "正式用户 A", Email: "a@example.com"})
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*contact.Contact)(nil)).Set("notes = ?", "来自 TG").Where("id = ?", userSource.ID).Exec(ctx); err != nil {
			return err
		}
		userConflict, err = contact.ResolveUser(ctx, tx, tenantID, chB.ID, "tg-2", "ext-u-2", contact.Profile{Name: "另一用户 B", Email: "b@example.com"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	merge := contact.NewMerge(env.DB)

	// 1. 权限与边界校验
	noPerm := identity.Subject{Type: member.Type, ID: agent.ID, Tenant: tenantID}
	if _, err := merge.Handle(ctx, noPerm, leadTarget.ID, contact.MergeBody{SourceID: userSource.ID}); !errors.Is(err, contact.ErrForbidden) {
		t.Fatalf("无 contacts:update 权限应返回 ErrForbidden，实际 %v", err)
	}
	if _, err := merge.Handle(ctx, agent, leadTarget.ID, contact.MergeBody{SourceID: leadTarget.ID}); !errors.Is(err, contact.ErrMergeSelf) {
		t.Fatalf("合并自己应返回 ErrMergeSelf，实际 %v", err)
	}
	if _, err := merge.Handle(ctx, other.agent, leadTarget.ID, contact.MergeBody{SourceID: userSource.ID}); !errors.Is(err, contact.ErrNotFound) {
		t.Fatalf("其他工作区应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := merge.Handle(ctx, agent, userSource.ID, contact.MergeBody{SourceID: userConflict.ID}); !errors.Is(err, contact.ErrMergeConflict) {
		t.Fatalf("不同 external_user_id 应返回 ErrMergeConflict，实际 %v", err)
	}

	// 2. 目标为线索、来源为已登录用户：合并后目标升级为 user 并继承 external_user_id、邮箱、拼接备注与双渠道身份
	res, err := merge.Handle(ctx, agent, leadTarget.ID, contact.MergeBody{SourceID: userSource.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != leadTarget.ID || res.Kind != contact.User || res.ExternalUserID != "ext-u-1" {
		t.Fatalf("目标联系人应升级为 user 并继承 external_user_id，实际 %+v", res)
	}
	if res.Email != "a@example.com" || res.Notes != "来自官网\n\n来自 TG" {
		t.Fatalf("资料与备注合并异常：email=%q notes=%q", res.Email, res.Notes)
	}
	if len(res.Identities) != 2 {
		t.Fatalf("合并后应有 2 个渠道身份，实际 %d", len(res.Identities))
	}
	if _, err := contact.NewGet(env.DB).Handle(ctx, agent, userSource.ID); !errors.Is(err, contact.ErrNotFound) {
		t.Fatalf("来源联系人合并后应已删除，实际 %v", err)
	}
}
