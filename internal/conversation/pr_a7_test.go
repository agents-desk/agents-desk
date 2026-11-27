package conversation_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/savedview"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
)

// TestGetInboxViewsSplitAndCounts 验证 GetInbox 在存在历史已关闭会话时，主聚合与按状态拆分的扩展视图计数均准确无误。
func TestGetInboxViewsSplitAndCounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "inbox-split")

	var ownerID string
	if err := env.DB.NewRaw(`SELECT owner_id FROM tenants WHERE id = ?`, f.system.Tenant).Scan(ctx, &ownerID); err != nil {
		t.Fatal(err)
	}
	ownerAgent := f.agent
	ownerAgent.ID = ownerID
	ownerAgent.Permissions = append(append([]string(nil), f.agent.Permissions...), string(auth.ViewsManage))

	openConv := f.receive(t, "c-open", "需要处理", "in-1")
	closedConv := f.receive(t, "c-closed", "已解决的问题", "in-2")
	unassignedConv := f.receive(t, "c-unassigned", "未指派会话", "in-3")
	if unassignedConv.ConversationID == openConv.ConversationID {
		t.Fatal("不同客户应创建不同会话")
	}

	if _, err := conversation.NewReply(env.DB, f.down).Handle(ctx, ownerAgent, openConv.ConversationID, conversation.ReplyBody{Text: "收到"}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewAddNote(env.DB, f.down).Handle(ctx, ownerAgent, closedConv.ConversationID, "提醒处理", []string{ownerID}); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.NewClose(env.DB, f.down).Handle(ctx, ownerAgent, closedConv.ConversationID); err != nil {
		t.Fatal(err)
	}

	views := conversation.NewViews(env.DB)
	vOpen, err := views.Create(ctx, ownerAgent, savedview.CreateInput[conversation.ViewConfig]{
		Scope:  savedview.ScopeShared,
		Name:   "仅打开会话",
		Config: conversation.ViewConfig{Filter: conversation.Filter{Statuses: []conversation.Status{conversation.Open}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	vClosed, err := views.Create(ctx, ownerAgent, savedview.CreateInput[conversation.ViewConfig]{
		Scope:  savedview.ScopeShared,
		Name:   "仅已关闭会话",
		Config: conversation.ViewConfig{Filter: conversation.Filter{Statuses: []conversation.Status{conversation.Closed}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	vAll, err := views.Create(ctx, ownerAgent, savedview.CreateInput[conversation.ViewConfig]{
		Scope:  savedview.ScopePersonal,
		Name:   "全部状态视图",
		Config: conversation.ViewConfig{Filter: conversation.Filter{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	dto, err := conversation.NewGetInbox(env.DB).Handle(ctx, ownerAgent)
	if err != nil {
		t.Fatal(err)
	}
	if dto.All.Count != 2 || dto.Mine.Count != 1 || dto.Unassigned.Count != 1 || dto.Mentions.Count != 1 {
		t.Fatalf("内置收件箱计数异常：%+v", dto)
	}
	countsByID := map[string]int{}
	for _, v := range dto.Views {
		countsByID[v.ID] = v.Count
	}
	if countsByID[vOpen.ID] != 2 || countsByID[vClosed.ID] != 1 || countsByID[vAll.ID] != 3 {
		t.Fatalf("视图拆分聚合计数异常：%+v", countsByID)
	}
}

// TestMergeConversationsBulkAndRelatedTables 验证合并双活会话时，timeline_items/mentions 批量重排正确，
// 且修订历史、撤回记录、服务轮次（含已关闭历史轮次与未关闭轮次合并）、AI 生成与自动化记录均完整迁移。
func TestMergeConversationsBulkAndRelatedTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "merge-bulk")
	tenantID, agent, sys := f.system.Tenant, f.agent, f.system

	widgetCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Widget, Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	tgCh, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Telegram, Name: "TG"})
	if err != nil {
		t.Fatal(err)
	}
	reg := channel.Registry{channel.Widget: &omniStubConnector{}, channel.Telegram: &omniStubConnector{}}
	receive := conversation.NewReceive(env.DB, reg, f.down)

	// 取当前工作区拥有者成员 ID（拥有合法工作区角色），满足 @ 成员校验及 message_revisions / message_recalls 外键约束
	var ownerID string
	if err := env.DB.NewRaw(`SELECT owner_id FROM tenants WHERE id = ?`, tenantID).Scan(ctx, &ownerID); err != nil {
		t.Fatal(err)
	}

	cTargetRecv, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: widgetCh.ID, From: "w-1", Text: "目标首条"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: widgetCh.ID, From: "w-1", Text: "目标第二条"}); err != nil {
		t.Fatal(err)
	}
	cSourceRecv, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: tgCh.ID, From: "t-1", Text: "来源首条"})
	if err != nil {
		t.Fatal(err)
	}
	sourceNote, err := conversation.NewAddNote(env.DB, f.down).Handle(ctx, agent, cSourceRecv.ConversationID, "内部备注提醒", []string{ownerID})
	if err != nil {
		t.Fatal(err)
	}

	// 写入关联表记录到源会话：revision、recall、关闭的历史 round、bot/generation、automation_run
	now := time.Now().UTC()
	botID := uuid.Must(uuid.NewV7()).String()
	ruleID := uuid.Must(uuid.NewV7()).String()
	genID := uuid.Must(uuid.NewV7()).String()
	runID := uuid.Must(uuid.NewV7()).String()
	roundClosedID := uuid.Must(uuid.NewV7()).String()

	if _, err := env.DB.NewRaw(`INSERT INTO message_revisions (item_id, revision, tenant_id, conversation_id, text, editor_id, created_at)
		VALUES (?, 1, ?, ?, '编辑版本', ?, ?)`, cSourceRecv.Item.ID, tenantID, cSourceRecv.ConversationID, ownerID, now).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO message_recalls (item_id, tenant_id, conversation_id, recalled_by, created_at)
		VALUES (?, ?, ?, ?, ?)`, sourceNote.ID, tenantID, cSourceRecv.ConversationID, ownerID, now).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 为源会话补一条历史已关闭轮次（先调整当前未关闭轮次号或插在 round=99），验证已关闭轮次保留且不冲突
	if _, err := env.DB.NewRaw(`INSERT INTO conversation_rounds (id, created_at, updated_at, tenant_id, conversation_id, round, opened_at, closed_at, rating)
		VALUES (?, ?, ?, ?, ?, 99, ?, ?, 5)`, roundClosedID, now, now, tenantID, cSourceRecv.ConversationID, now.Add(-2*time.Hour), now.Add(-time.Hour)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO bots (id, created_at, updated_at, tenant_id, name, enabled, settings)
		VALUES (?, ?, ?, ?, 'Bot', true, '{}')`, botID, now, now, tenantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO generations (id, created_at, updated_at, tenant_id, conversation_id, actor_id, trigger_item_id, state, parts, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'failed', '{}', ?)`, genID, now, now, tenantID, cSourceRecv.ConversationID, botID, cSourceRecv.Item.ID, now.Add(time.Hour)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO automation_rules (id, created_at, updated_at, tenant_id, name, trigger)
		VALUES (?, ?, ?, ?, 'Rule', 'conversation.created')`, ruleID, now, now, tenantID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO automation_runs (id, created_at, updated_at, tenant_id, rule_id, conversation_id, trigger_item_id, dedup_key, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'k1', 'succeeded')`, runID, now, now, tenantID, ruleID, cSourceRecv.ConversationID, cSourceRecv.Item.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	targetConv, err := conversation.NewGet(env.DB).Handle(ctx, agent, cTargetRecv.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	sourceConv, err := conversation.NewGet(env.DB).Handle(ctx, agent, cSourceRecv.ConversationID)
	if err != nil {
		t.Fatal(err)
	}

	mergeContact := contact.NewMerge(env.DB, conversation.OnContactMerge(f.down))
	if _, err := mergeContact.Handle(ctx, agent, targetConv.Contact.ID, contact.MergeBody{SourceID: sourceConv.Contact.ID}); err != nil {
		t.Fatalf("合并联系人及会话失败：%v", err)
	}

	// 检查时间线 seq 与 mention conversation_id/seq
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, agent, targetConv.ID, 0, 50)
	if err != nil || len(items) != 5 {
		t.Fatalf("合并后应有 5 条时间线项（4 消息/备注 + 1 merge 事件），实际 len=%d err=%v", len(items), err)
	}
	for i, it := range items {
		if it.Seq != int64(i+1) {
			t.Fatalf("seq 应从 1 连续，位置 %d 实际 seq=%d", i, it.Seq)
		}
	}
	var mentionConv string
	if err := env.DB.NewRaw(`SELECT conversation_id FROM conversation_mentions WHERE item_id = ?`, sourceNote.ID).Scan(ctx, &mentionConv); err != nil || mentionConv != targetConv.ID {
		t.Fatalf("conversation_mentions 未正确迁移：conv=%q err=%v", mentionConv, err)
	}

	// 检查所有关联表均已迁入 targetConv.ID 并幸存
	for _, check := range []struct {
		name  string
		query string
		arg   string
	}{
		{"message_revisions", "SELECT count(*) FROM message_revisions WHERE conversation_id = ?", targetConv.ID},
		{"message_recalls", "SELECT count(*) FROM message_recalls WHERE conversation_id = ?", targetConv.ID},
		{"generations", "SELECT count(*) FROM generations WHERE conversation_id = ?", targetConv.ID},
		{"automation_runs", "SELECT count(*) FROM automation_runs WHERE conversation_id = ?", targetConv.ID},
	} {
		var cnt int
		if err := env.DB.NewRaw(check.query, check.arg).Scan(ctx, &cnt); err != nil || cnt != 1 {
			t.Fatalf("%s 迁移计数期望 1，实际 %d err=%v", check.name, cnt, err)
		}
	}
	var openRounds, totalRounds int
	if err := env.DB.NewRaw(`SELECT count(*) FILTER (WHERE closed_at IS NULL), count(*) FROM conversation_rounds WHERE conversation_id = ?`, targetConv.ID).Scan(ctx, &openRounds, &totalRounds); err != nil {
		t.Fatal(err)
	}
	if openRounds != 1 || totalRounds != 2 {
		t.Fatalf("合并后应保留 1 条未关闭轮次和 1 条历史已关闭轮次，实际 open=%d total=%d", openRounds, totalRounds)
	}
}

// TestEvictCachedMediaSharedFileGuard 验证多个附件共享同一个 file_id 时，回收其一不会误删仍在引用的底层文件。
func TestEvictCachedMediaSharedFileGuard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "evict-shared")
	sys := f.system

	conv1 := f.receive(t, "cust-v1", "视频消息 1", "v-1")
	conv2 := f.receive(t, "cust-v2", "视频消息 2", "v-2")
	up, err := env.Files.Upload(ctx, sys, filesystem.FileInput{
		Disk:        env.Files.PrivateDisk(),
		Filename:    "demo.mp4",
		ContentType: "video/mp4",
		Size:        16,
	}, bytes.NewReader([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00")))
	if err != nil {
		t.Fatal(err)
	}
	fileID := up.ID
	now := time.Now().UTC()
	stale := now.Add(-40 * 24 * time.Hour)
	a1ID := uuid.Must(uuid.NewV7()).String()
	a2ID := uuid.Must(uuid.NewV7()).String()

	for _, row := range []struct {
		id     string
		itemID string
		at     time.Time
	}{
		{a1ID, conv1.Item.ID, stale},
		{a2ID, conv2.Item.ID, now},
	} {
		if _, err := env.DB.NewRaw(`INSERT INTO timeline_attachments
			(id, created_at, updated_at, tenant_id, item_id, position, kind, filename, content_type, size, file_id, source_channel_id, source_ref, fetch_status)
			VALUES (?, ?, ?, ?, ?, 0, 'video', 'demo.mp4', 'video/mp4', 16, ?, ?, 'ref-1', 'ready')`,
			row.id, row.at, row.at, sys.Tenant, row.itemID, fileID, f.ch.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := now.Add(-30 * 24 * time.Hour)
	evicted, err := conversation.EvictCachedMedia(ctx, env.DB, env.Files, cutoff, 10)
	if err != nil || evicted != 1 {
		t.Fatalf("应回收 1 条过期缓存，实际 evicted=%d err=%v", evicted, err)
	}
	// 共享文件仍被 a2 引用，files 记录必须保留
	if _, _, err := env.Files.Open(ctx, fileID); err != nil {
		t.Fatalf("共享附件文件不应被误删：%v", err)
	}

	// 将 a2 也设为过期后再次回收，此次应真正删除文件
	if _, err := env.DB.NewRaw(`UPDATE timeline_attachments SET updated_at = ? WHERE id = ?`, stale, a2ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	evicted2, err := conversation.EvictCachedMedia(ctx, env.DB, env.Files, cutoff, 10)
	if err != nil || evicted2 != 1 {
		t.Fatalf("第二次应回收剩余 1 条缓存，实际 evicted=%d err=%v", evicted2, err)
	}
	if _, _, err := env.Files.Open(ctx, fileID); err == nil {
		t.Fatal("所有共享引用都已回收，底层文件应被删除")
	}
}

// TestSaveTranslationAndSetLanguageConcurrentContactMergeNoDeadlock 验证高并发下 SaveTranslation、SetLanguage 与 contact.Merge 同时运行时不发生 AB-BA 死锁。
func TestSaveTranslationAndSetLanguageConcurrentContactMergeNoDeadlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "lock-order")
	tenantID, agent, sys := f.system.Tenant, f.agent, f.system

	ch1, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Widget, Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Telegram, Name: "TG"})
	if err != nil {
		t.Fatal(err)
	}
	reg := channel.Registry{channel.Widget: &omniStubConnector{}, channel.Telegram: &omniStubConnector{}}
	receive := conversation.NewReceive(env.DB, reg, f.down)
	mergeContact := contact.NewMerge(env.DB, conversation.OnContactMerge(f.down))
	setLang := conversation.NewSetLanguage(env.DB, f.down)

	// 测试 6 组并发合并与翻译/改语言交错执行，均不得出现 SQLSTATE 40P01
	for round := range 6 {
		prefix := fmt.Sprintf("r%d", round)
		rA, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch1.ID, From: prefix + "-a", Text: "hello world"})
		if err != nil {
			t.Fatal(err)
		}
		rB, err := receive.Handle(ctx, sys, conversation.ReceiveParams{ChannelID: ch2.ID, From: prefix + "-b", Text: "hola mundo"})
		if err != nil {
			t.Fatal(err)
		}
		cA, err := conversation.NewGet(env.DB).Handle(ctx, agent, rA.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		cB, err := conversation.NewGet(env.DB).Handle(ctx, agent, rB.ConversationID)
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		errs := make(chan error, 3)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_, mErr := mergeContact.Handle(ctx, agent, cA.Contact.ID, contact.MergeBody{SourceID: cB.Contact.ID})
			errs <- mErr
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- conversation.SaveTranslation(ctx, env.DB, f.down, tenantID, rB.Item.ID, conversation.Translated{
				Language:     "es",
				Translations: map[string]string{"zh-CN": "你好世界"},
			})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, sErr := setLang.Handle(ctx, agent, rB.ConversationID, "es")
			// 目标会话若恰好已被并发合并删除则 ErrNotFound 属预期，绝不允许出现 deadlock
			if sErr != nil && !strings.Contains(sErr.Error(), "not_found") && !database.IsUniqueViolation(sErr, "") {
				if strings.Contains(sErr.Error(), "40P01") || strings.Contains(strings.ToLower(sErr.Error()), "deadlock") {
					errs <- sErr
					return
				}
			}
			errs <- nil
		}()

		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatalf("轮次 %d 并发执行异常（不得发生死锁）：%v", round, e)
			}
		}
	}
}
