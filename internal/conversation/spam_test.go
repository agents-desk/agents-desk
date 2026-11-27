package conversation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// TestMarkSpamUnmarkSpamAndBlockContact 验证标垃圾会话、同步拉黑联系人及权限校验、收件箱统计/列表/搜索过滤以及取消垃圾标记与跨工作区隔离。
func TestMarkSpamUnmarkSpamAndBlockContact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setup(t, env, "spam-alpha")
	beta := setup(t, env, "spam-beta")

	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour})
	down := alpha.down
	down.RevokeContactTokens = func(ctx context.Context, tx database.Tx, _ string, contactID string) error {
		_, err := tokens.WithDB(tx).RevokeSubject(ctx, identity.Ref{Type: contact.Type, ID: contactID})
		return err
	}

	normalRec := alpha.receive(t, "u-normal", "normal billing question", "msg-1")
	spamRec := alpha.receive(t, "u-spam", "crypto spam offer unique token", "msg-2")

	var spamContactID string
	if err := env.DB.NewRaw("SELECT contact_id FROM conversations WHERE id = ?", spamRec.ConversationID).Scan(ctx, &spamContactID); err != nil {
		t.Fatal(err)
	}
	tok, err := tokens.Login(ctx, identity.Subject{Type: contact.Type, ID: spamContactID, Tenant: alpha.system.Tenant}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}

	// 只有 conversations:reply 没有 contacts:update 时，勾选 block_contact 应被拒绝
	replyOnly := identity.Subject{
		Type: member.Type, ID: alpha.agent.ID, Tenant: alpha.system.Tenant,
		Permissions: []string{string(auth.ConversationsReadAll), string(auth.ConversationsReply)},
	}
	mark := conversation.NewMarkSpam(env.DB, down)
	if _, err := mark.Handle(ctx, replyOnly, spamRec.ConversationID, conversation.MarkSpamBody{BlockContact: true, Reason: "no perm"}); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("无 contacts:update 权限勾选拉黑应返回 ErrForbidden，实际 %v", err)
	}

	// 跨工作区标记返回 ErrNotFound
	if _, err := mark.Handle(ctx, beta.agent, spamRec.ConversationID, conversation.MarkSpamBody{}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("跨工作区标垃圾应返回 ErrNotFound，实际 %v", err)
	}

	// 正常标记为垃圾并同步拉黑联系人：会话关闭、SpamAt 非空、联系人被拉黑且网页令牌失效
	dto, err := mark.Handle(ctx, alpha.agent, spamRec.ConversationID, conversation.MarkSpamBody{BlockContact: true, Reason: "广告骚扰"})
	if err != nil {
		t.Fatal(err)
	}
	if dto.SpamAt == nil || dto.Status != conversation.Closed {
		t.Fatalf("标垃圾后会话状态不符：%+v", dto)
	}
	var blockedAt *time.Time
	var blockedReason string
	if err := env.DB.NewRaw("SELECT blocked_at, blocked_reason FROM contacts WHERE id = ?", spamContactID).Scan(ctx, &blockedAt, &blockedReason); err != nil {
		t.Fatal(err)
	}
	if blockedAt == nil || blockedReason != "广告骚扰" {
		t.Fatalf("联系人未被正确拉黑：blocked_at=%v reason=%q", blockedAt, blockedReason)
	}
	if _, err := tokens.Validate(ctx, tok); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("标垃圾拉黑后联系人令牌应失效，实际 %v", err)
	}

	// 收件箱统计：常规计数排除垃圾会话，Spam 计入
	inbox, err := conversation.NewGetInbox(env.DB).Handle(ctx, alpha.agent)
	if err != nil {
		t.Fatal(err)
	}
	if inbox.All.Count != 1 || inbox.Spam != 1 {
		t.Fatalf("收件箱计数不符：all=%d spam=%d", inbox.All.Count, inbox.Spam)
	}

	// 列表查询：all 只返回正常会话，spam 只返回垃圾会话
	allList, err := conversation.NewList(env.DB).Handle(ctx, alpha.agent, &conversation.ListInput{Inbox: conversation.InboxAll, Status: conversation.Open})
	if err != nil {
		t.Fatal(err)
	}
	if len(allList.Items) != 1 || allList.Items[0].ID != normalRec.ConversationID {
		t.Fatalf("all 列表应仅含正常会话：%d", len(allList.Items))
	}
	spamList, err := conversation.NewList(env.DB).Handle(ctx, alpha.agent, &conversation.ListInput{Inbox: conversation.InboxSpam, Status: conversation.Open})
	if err != nil {
		t.Fatal(err)
	}
	if len(spamList.Items) != 1 || spamList.Items[0].ID != spamRec.ConversationID {
		t.Fatalf("spam 列表应返回标垃圾会话：len=%d", len(spamList.Items))
	}

	// 搜索：默认排除垃圾会话，指定 inbox=spam 可搜到
	refreshAllSearch(t, env)
	search := conversation.NewSearch(env.DB)
	defaultHits, err := search.Handle(ctx, alpha.agent, &conversation.SearchInput{Q: "crypto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultHits.Items) != 0 {
		t.Fatalf("常规搜索不应命中垃圾会话：len=%d", len(defaultHits.Items))
	}
	spamHits, err := search.Handle(ctx, alpha.agent, &conversation.SearchInput{Q: "crypto", Inbox: conversation.InboxSpam})
	if err != nil {
		t.Fatal(err)
	}
	if len(spamHits.Items) != 1 || spamHits.Items[0].Conversation.ID != spamRec.ConversationID {
		t.Fatalf("spam 搜索应命中垃圾会话：len=%d", len(spamHits.Items))
	}

	// 取消垃圾标记：清除 SpamAt、移出垃圾列表、保持关闭状态
	unmark := conversation.NewUnmarkSpam(env.DB, down)
	if _, err := unmark.Handle(ctx, beta.agent, spamRec.ConversationID); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("跨工作区取消垃圾标记应返回 ErrNotFound，实际 %v", err)
	}
	restored, err := unmark.Handle(ctx, alpha.agent, spamRec.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SpamAt != nil || restored.Status != conversation.Closed {
		t.Fatalf("取消垃圾标记后状态不符：%+v", restored)
	}
	inboxAfter, err := conversation.NewGetInbox(env.DB).Handle(ctx, alpha.agent)
	if err != nil {
		t.Fatal(err)
	}
	if inboxAfter.Spam != 0 {
		t.Fatalf("取消垃圾标记后 Spam 计数应归零，实际 %d", inboxAfter.Spam)
	}
}
