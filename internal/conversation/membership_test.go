package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestChatMembershipIntervals 验证成员关系是带有效期的区间：退出群聊后记录保留并记下离开时间，
// 成员随即看不到这个群；重新加入是新增的一段，而不是覆盖旧记录。
func TestChatMembershipIntervals(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	add := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	alice, err := add.Handle(ctx, sys, member.AddParams{Email: "alice@alpha.com", Name: "Alice", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	subOwner := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	subAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: tn.ID, Permissions: auth.WithBase(agentRole.Permissions)}

	chat, err := conversation.NewCreateGroupChat(env.DB, down).Handle(ctx, subOwner, conversation.CreateGroupChatParams{
		Name: "项目群", MemberIDs: []string{alice.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewGetChat(env.DB, env.Files).Handle(ctx, subAlice, chat.ID); err != nil {
		t.Fatalf("加入后应能看到群聊：%v", err)
	}

	// 1. 退出：记录保留，left_at 非空，成员随即看不到群聊
	if err := conversation.NewLeaveChat(env.DB, down).Handle(ctx, subAlice, chat.ID); err != nil {
		t.Fatal(err)
	}
	type row struct {
		LeftAt    *time.Time `bun:"left_at"`
		CreatedAt time.Time  `bun:"created_at"`
	}
	rows := func() []row {
		var rs []row
		if err := env.DB.NewSelect().Table("conversation_participants").Column("left_at", "created_at").
			Where("conversation_id = ? AND participant_type = ? AND participant_id = ?", chat.ID, conversation.MemberActor, alice.ID).
			OrderExpr("created_at, id").Scan(ctx, &rs); err != nil {
			t.Fatal(err)
		}
		return rs
	}
	afterLeave := rows()
	if len(afterLeave) != 1 || afterLeave[0].LeftAt == nil {
		t.Fatalf("退出后应保留记录并记下离开时间：%+v", afterLeave)
	}
	if _, err := conversation.NewGetChat(env.DB, env.Files).Handle(ctx, subAlice, chat.ID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("退出后不应再看到群聊：%v", err)
	}

	// 2. 重新加入：新增一段，旧的一段仍然保留
	if _, err := conversation.NewAddChatParticipants(env.DB, down).Handle(ctx, subOwner, chat.ID, []string{alice.ID}); err != nil {
		t.Fatal(err)
	}
	afterRejoin := rows()
	if len(afterRejoin) != 2 {
		t.Fatalf("重新加入应新增一段成员关系：%+v", afterRejoin)
	}
	active := 0
	for _, r := range afterRejoin {
		if r.LeftAt == nil {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("同时应只有一段有效：%+v", afterRejoin)
	}
	if _, err := conversation.NewGetChat(env.DB, env.Files).Handle(ctx, subAlice, chat.ID); err != nil {
		t.Fatalf("重新加入后应能再看到群聊：%v", err)
	}
}
