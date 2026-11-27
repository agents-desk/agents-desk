package wecom

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"

	"agents-desk/internal/apptest"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestWeComLockNotAcquiredPreservesConnectedStatus 验证多实例抢锁返回 ErrNotAcquired 时不覆写在线状态。
func TestWeComLockNotAcquiredPreservesConnectedStatus(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "LockAlpha", Slug: "wecom-lock", OwnerEmail: "owner@wecom-lock.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	locker := lock.New(pglock.New(env.DB))
	mgr := NewManager(env.DB, env.Crypt, env.Files, locker, nil)
	dto, err := NewCreate(env.DB, env.Crypt, mgr.Wake).Handle(ctx, sub, CreateBody{
		Name:   "企微客服",
		BotID:  "bot-lock-001",
		Secret: "sec-lock-001",
		WSURL:  "ws://127.0.0.1:1/unused",
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.recordStatus(ctx, tn.ID, dto.ID, StatusConnected, "", true)

	holdEntered := make(chan struct{})
	holdCtx, cancelHold := context.WithCancel(ctx)
	defer cancelHold()
	go func() {
		_ = locker.Hold(holdCtx, "wecom:conn:"+dto.ID, 30*time.Second, func(hctx context.Context) error {
			close(holdEntered)
			<-hctx.Done()
			return hctx.Err()
		})
	}()
	<-holdEntered

	runCtx, cancelRun := context.WithCancel(ctx)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = mgr.Run(runCtx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancelRun()
	<-runDone

	got, err := NewGet(env.DB).Handle(ctx, sub, dto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusConnected {
		t.Fatalf("未抢到锁时不应覆盖在线状态，实际状态=%s", got.Status)
	}
}

// TestWeComConcurrentReplyReqIDAndPendingFIFO 验证同一入站 req_id 并发认领互斥与 pending FIFO 唤醒队列。
func TestWeComConcurrentReplyReqIDAndPendingFIFO(t *testing.T) {
	t.Parallel()
	runner := &botRunner{pending: map[string][]chan Frame{}}

	const workers = 8
	var wg sync.WaitGroup
	var wins int
	var mu sync.Mutex
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			if runner.tryClaimReplyReqID("req-inbound-1") {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("同一 req_id 仅允许一次被动回复认领，实际成功 %d 次", wins)
	}

	ch1 := make(chan Frame, 1)
	ch2 := make(chan Frame, 1)
	runner.mu.Lock()
	runner.pending["dup"] = []chan Frame{ch1, ch2}
	runner.mu.Unlock()

	if !runner.resolveAck(Frame{Headers: FrameHeaders{ReqID: "dup"}, ErrCode: 1}) {
		t.Fatal("第一次 resolveAck 应唤醒首个等待者")
	}
	if !runner.resolveAck(Frame{Headers: FrameHeaders{ReqID: "dup"}, ErrCode: 2}) {
		t.Fatal("第二次 resolveAck 应唤醒第二个等待者")
	}
	f1 := <-ch1
	f2 := <-ch2
	if f1.ErrCode != 1 || f2.ErrCode != 2 {
		t.Fatalf("pending 唤醒顺序错误：f1=%d f2=%d", f1.ErrCode, f2.ErrCode)
	}
}

// TestWeComUploadMediaValidation 验证企微附件上传拒绝缺失文件并按 512 KiB 精确分片发送。
func TestWeComUploadMediaValidation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	fake, srv, wsURL := newFakeWeComServer(t, "bot-up", "sec-up")

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "UploadAlpha", Slug: "wecom-up", OwnerEmail: "owner@wecom-up.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	mgr := NewManager(env.DB, env.Crypt, env.Files, nil, nil)
	mgr.SetHTTPClients(srv.Client(), srv.Client())
	dto, err := NewCreate(env.DB, env.Crypt, mgr.Wake).Handle(ctx, sub, CreateBody{
		Name:   "企微上传",
		BotID:  "bot-up",
		Secret: "sec-up",
		WSURL:  wsURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	go func() { _ = mgr.Run(runCtx) }()

	select {
	case <-fake.connectedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("wecom fake server 未建立连接")
	}

	runner := mgr.localRunner(dto.ID)
	if runner == nil {
		t.Fatal("期望本实例持有 runner")
	}

	// 1. 缺失文件或 0 字节文件应直接返回错误，不发 init 帧
	if _, err := mgr.uploadMedia(ctx, runner, rpcAttachment{
		FileID:   uuid.NewString(),
		Kind:     "file",
		Filename: "missing.pdf",
		Size:     10,
	}, "file"); err == nil {
		t.Fatal("缺失文件应返回错误")
	}

	payload := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("A"), uploadChunkBytes+128)...)
	uploaded, err := env.Files.Upload(ctx, sub, filesystem.FileInput{
		Disk:        env.Files.PrivateDisk(),
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Size:        int64(len(payload)),
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := env.Files.Confirm(ctx, sub, uploaded.ID, filesystem.Accept{
		Types:   []string{"application/pdf"},
		MaxSize: 2 * filesystem.MiB,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.uploadMedia(ctx, runner, rpcAttachment{
		FileID:   confirmed.ID.String(),
		Kind:     "file",
		Filename: "report.pdf",
		Size:     0,
	}, "file"); err != nil {
		t.Fatalf("跨分片上传 PDF 失败：%v", err)
	}
	if got := len(fake.framesByCmd(CmdUploadChunk)); got != 2 {
		t.Fatalf("期望发送 2 个分片帧，实际 %d", got)
	}
}

// TestWeComCrossInstanceNATSRestart 验证跨实例经 NATS 触发重启后直接重连且不将上下文取消记入错误状态。
func TestWeComCrossInstanceNATSRestart(t *testing.T) {
	t.Parallel()
	natsURL := os.Getenv("NATS_TEST_URL")
	if natsURL == "" {
		t.Skip("NATS_TEST_URL 未设置，跳过跨实例 NATS 测试")
	}
	ctx := t.Context()
	env := apptest.New(t)
	nc1, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc1.Close)
	nc2, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc2.Close)

	fake, srv, wsURL := newFakeWeComServer(t, "bot-ctrl", "sec-ctrl")
	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "CtrlAlpha", Slug: "wecom-ctrl", OwnerEmail: "owner@wecom-ctrl.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	mgr1 := NewManager(env.DB, env.Crypt, env.Files, nil, nc1)
	mgr1.SetHTTPClients(srv.Client(), srv.Client())
	mgr2 := NewManager(env.DB, env.Crypt, env.Files, nil, nc2)
	mgr2.SetHTTPClients(srv.Client(), srv.Client())
	dto, err := NewCreate(env.DB, env.Crypt, mgr1.Wake).Handle(ctx, sub, CreateBody{
		Name:   "企微跨实例重启",
		BotID:  "bot-ctrl",
		Secret: "sec-ctrl",
		WSURL:  wsURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	go func() { _ = mgr1.Run(runCtx) }()

	select {
	case <-fake.connectedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("初次建连超时")
	}
	waitUntil(t, 3*time.Second, func() bool {
		got, err := NewGet(env.DB).Handle(ctx, sub, dto.ID)
		return err == nil && got.Status == StatusConnected
	})

	mgr2.Restart(dto.ID)

	select {
	case <-fake.connectedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("跨实例重启通知未触发立即重连")
	}

	waitUntil(t, 3*time.Second, func() bool {
		got, err := NewGet(env.DB).Handle(ctx, sub, dto.ID)
		return err == nil && got.Status == StatusConnected && got.LastError == ""
	})
}
