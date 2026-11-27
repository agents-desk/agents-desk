package tgaccount

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/tgerr"
	"github.com/nats-io/nats.go"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/lock/pglock"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestDeterministicRandomIDAndWrapTGError 验证 deterministicRandomID 幂等确定性与 wrapTGError 全语义分类。
func TestDeterministicRandomIDAndWrapTGError(t *testing.T) {
	t.Parallel()

	id1 := deterministicRandomID("item-001", 0)
	id2 := deterministicRandomID("item-001", 0)
	id3 := deterministicRandomID("item-001", 1)
	if id1 == 0 || id1 != id2 || id1 == id3 {
		t.Fatalf("deterministicRandomID 异常：id1=%d id2=%d id3=%d", id1, id2, id3)
	}

	flood := wrapTGError(tgerr.New(420, "FLOOD_WAIT_17"), "send")
	if flood == nil || queue.IsPermanent(flood) {
		t.Fatalf("FLOOD_WAIT 应可重试，实际 %v", flood)
	}

	blocked := wrapTGError(tgerr.New(400, "PEER_ID_INVALID"), "send")
	if blocked == nil || !queue.IsPermanent(blocked) {
		t.Fatalf("PEER_ID_INVALID 应标为 Permanent，实际 %v", blocked)
	}

	revoked := wrapTGError(tgerr.New(401, "SESSION_REVOKED"), "send")
	if !errors.Is(revoked, ErrNotConnected) {
		t.Fatalf("SESSION_REVOKED 应归为 ErrNotConnected，实际 %v", revoked)
	}

	transient := wrapTGError(errors.New("temporary network glitch"), "send")
	if transient == nil || queue.IsPermanent(transient) {
		t.Fatalf("普通网络错误应保持可退避重试，实际 %v", transient)
	}
}

// TestEncodeDecodeRPCErrorRoundTrip 验证跨 NATS RPC 错误编解码保留错误码、限流等待与永久失败语义。
func TestEncodeDecodeRPCErrorRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		err       error
		wantIs    error
		wantPerm  bool
		wantRetry int
	}{
		{"not_connected", ErrNotConnected.New(), ErrNotConnected, true, 0},
		{"temp_unavail", ErrTemporarilyUnavailable.New(), ErrTemporarilyUnavailable, false, 0},
		{"media_unavail", channel.ErrMediaUnavailable.New(), channel.ErrMediaUnavailable, true, 0},
		{"media_too_large", channel.ErrMediaTooLarge.New(), channel.ErrMediaTooLarge, true, 0},
		{"flood_wait", tgerr.New(420, "FLOOD_WAIT_9"), nil, false, 9},
		{"permanent_custom", queue.Permanent(errors.New("peer blocked")), nil, true, 0},
	} {
		re := encodeRPCError(tc.err)
		if re == nil {
			t.Fatalf("%s: encodeRPCError 返回 nil", tc.name)
		}
		if re.RetryAfterSec != tc.wantRetry {
			t.Fatalf("%s: RetryAfterSec=%d want=%d", tc.name, re.RetryAfterSec, tc.wantRetry)
		}
		decoded := decodeRPCError(re)
		if tc.wantIs != nil && !errors.Is(decoded, tc.wantIs) {
			t.Fatalf("%s: decoded=%v 未保留 %v", tc.name, decoded, tc.wantIs)
		}
		if gotPerm := queue.IsPermanent(decoded); gotPerm != tc.wantPerm {
			t.Fatalf("%s: IsPermanent=%v want=%v (%v)", tc.name, gotPerm, tc.wantPerm, decoded)
		}
	}
}

// TestLockErrNotAcquiredPreservesConnectedStatus 验证多实例抢锁返回 ErrNotAcquired 时不覆写在线状态与错误字段。
func TestLockErrNotAcquiredPreservesConnectedStatus(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "TGLock", Slug: "tg-lock", OwnerEmail: "owner@tg-lock.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	tgCreds := NewCredentialsGroup(env.DB, env.Crypt)
	if _, err := NewSaveCredentials(env.DB, tgCreds, nil).Handle(ctx, sub, Credentials{APIID: 10001, APIHash: "lock-hash"}); err != nil {
		t.Fatal(err)
	}

	be := &fakeBackend{
		nextProfile: TelegramProfile{UserID: 551, Username: "lockuser", DisplayName: "Lock User", Phone: "+8613800000551"},
		live:        &fakeLiveClient{},
	}
	mgr := NewManager(env.DB, env.Crypt, env.Files, tgCreds, be)
	sess, err := mgr.StartLogin(ctx, sub, LoginModePhone, "Lock 号", "+8613800000551", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := mgr.SubmitCode(ctx, sub, sess.LoginID, "12345")
	if err != nil || res.Account == nil {
		t.Fatalf("登录失败：%v", err)
	}

	locker := lock.New(pglock.New(env.DB))
	mgr.SetLocker(locker)

	held := make(chan struct{})
	holdCtx, cancelHold := context.WithCancel(ctx)
	defer cancelHold()
	go func() {
		_ = locker.Hold(holdCtx, "tgaccount:conn:"+res.Account.ID, 30*time.Second, func(hctx context.Context) error {
			close(held)
			<-hctx.Done()
			return hctx.Err()
		})
	}()
	<-held

	runCtx, cancelRun := context.WithCancel(ctx)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = mgr.Run(runCtx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancelRun()
	<-runDone

	got, err := mgr.GetAccount(ctx, sub, res.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusConnected || got.LastError != "" {
		t.Fatalf("未抢到锁时不应覆盖连接状态：%+v", got)
	}
}

type oneShotStreamBackend struct {
	fakeBackend
	media      map[string][]byte
	inFlight   atomic.Bool
	exitedCh   chan struct{}
	exitedOnce sync.Once
}

func (b *oneShotStreamBackend) RunOneShot(ctx context.Context, cfg MTProtoConfig, storage session.Storage, fn func(context.Context, LiveClient) error) error {
	if _, err := storage.LoadSession(ctx); err != nil {
		return err
	}
	cli := &oneShotLiveClient{media: b.media, active: &b.inFlight}
	b.inFlight.Store(true)
	err := fn(ctx, cli)
	b.inFlight.Store(false)
	b.exitedOnce.Do(func() { close(b.exitedCh) })
	return err
}

type oneShotLiveClient struct {
	fakeLiveClient
	media  map[string][]byte
	active *atomic.Bool
}

func (c *oneShotLiveClient) DownloadMedia(_ context.Context, ref string, _ int64) (*channel.FetchedMedia, error) {
	data := c.media[ref]
	pr, pw := io.Pipe()
	go func() {
		if !c.active.Load() {
			_ = pw.CloseWithError(errors.New("oneshot connection closed before stream read"))
			return
		}
		_, _ = pw.Write(data)
		_ = pw.Close()
	}()
	return &channel.FetchedMedia{Body: pr, Filename: "clip.mp4", ContentType: "video/mp4", Size: int64(len(data))}, nil
}

// TestRunOneShotFetchMediaHoldsConnectionUntilBodyClosed 验证无 NATS 时 RunOneShot 媒体拉取直到 Body.Close 才关闭连接。
func TestRunOneShotFetchMediaHoldsConnectionUntilBodyClosed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "OneShot", Slug: "tg-oneshot", OwnerEmail: "owner@tg-oneshot.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	tgCreds := NewCredentialsGroup(env.DB, env.Crypt)
	if _, err := NewSaveCredentials(env.DB, tgCreds, nil).Handle(ctx, sub, Credentials{APIID: 10002, APIHash: "oneshot-hash"}); err != nil {
		t.Fatal(err)
	}

	payload := []byte("streamed mtproto video bytes")
	be := &oneShotStreamBackend{
		fakeBackend: fakeBackend{
			nextProfile: TelegramProfile{UserID: 552, Username: "oneshotuser", DisplayName: "OneShot", Phone: "+8613800000552"},
			live:        &fakeLiveClient{},
		},
		media:    map[string][]byte{"ref-1": payload},
		exitedCh: make(chan struct{}),
	}
	mgr := NewManager(env.DB, env.Crypt, env.Files, tgCreds, be)
	sess, err := mgr.StartLogin(ctx, sub, LoginModePhone, "OS 号", "+8613800000552", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := mgr.SubmitCode(ctx, sub, sess.LoginID, "12345")
	if err != nil {
		t.Fatal(err)
	}
	chModel, err := channel.Find(ctx, env.DB, tn.ID, res.Account.ID)
	if err != nil {
		t.Fatal(err)
	}

	fetcher := mgr.Connector().(channel.MediaFetcher)
	fetched, err := fetcher.FetchMedia(ctx, chModel, "ref-1", 1024*1024)
	if err != nil {
		t.Fatalf("FetchMedia 失败：%v", err)
	}
	got, err := io.ReadAll(fetched.Body)
	if err != nil {
		t.Fatalf("读取流失败：%v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("流内容不一致：got=%q want=%q", got, payload)
	}
	if !be.inFlight.Load() {
		t.Fatal("Body.Close() 调用前 RunOneShot 不应提前退出")
	}
	_ = fetched.Body.Close()
	select {
	case <-be.exitedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Body.Close() 后 RunOneShot 未及时退出")
	}
}

// TestCrossInstanceNATSRPCControlAndLogin 验证跨实例 NATS RPC（发送、暂存媒体拉取并清理、Dialog/Folder/History、配置重载与跨实例登录）。
func TestCrossInstanceNATSRPCControlAndLogin(t *testing.T) {
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

	tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "RPCAlpha", Slug: "tg-rpc", OwnerEmail: "owner@tg-rpc.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID}
	otherSub := identity.Subject{Type: member.Type, ID: "other-member", Tenant: tn.ID}
	tgCreds := NewCredentialsGroup(env.DB, env.Crypt)
	if _, err := NewSaveCredentials(env.DB, tgCreds, nil).Handle(ctx, sub, Credentials{APIID: 20001, APIHash: "rpc-hash"}); err != nil {
		t.Fatal(err)
	}

	mediaBytes := []byte("cross-instance-telegram-photo-bytes")
	peer1 := SyncedPeer{ExternalChatID: "tg:user:888", PeerType: "user", PeerID: 888, AccessHash: 999, Username: "bob", Title: "Bob"}
	live1 := &fakeLiveClient{
		dialogs: []DialogDTO{{SyncedPeer: peer1}},
		folders: []FolderDTO{{ID: 3, Title: "VIP", PeerCount: 1, Peers: []SyncedPeer{peer1}}},
		history: map[string][]IncomingMessage{
			peer1.ExternalChatID: {{
				ExternalChatID: peer1.ExternalChatID,
				MessageID:      11,
				SenderID:       "tg:user:888",
				SenderName:     "Bob",
				Text:           "backfill history msg",
				CreatedAt:      time.Now().UTC(),
			}},
		},
		media: map[string][]byte{"photo-rpc": mediaBytes},
	}
	be1 := &fakeBackend{
		nextProfile: TelegramProfile{UserID: 777, Username: "holder", DisplayName: "Holder", Phone: "+8613800000777"},
		live:        live1,
		startedCh:   make(chan string, 2),
	}
	be2 := &fakeBackend{live: &fakeLiveClient{}}

	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	down := conversation.Downstream{Hub: hub, Roles: env.Roles, Media: conversation.Media{Files: env.Files}}

	mgr1 := NewManager(env.DB, env.Crypt, env.Files, tgCreds, be1)
	mgr1.SetNATS(nc1)
	mgr1.SetReceive(conversation.NewReceive(env.DB, channel.Registry{channel.TelegramAccount: mgr1.Connector()}, down))

	mgr2 := NewManager(env.DB, env.Crypt, env.Files, tgCreds, be2)
	mgr2.SetNATS(nc2)
	mgr2.SetReceive(conversation.NewReceive(env.DB, channel.Registry{channel.TelegramAccount: mgr2.Connector()}, down))

	// 1. 跨实例登录：mgr1 发起，mgr2 提交验证码并校验越权成员不可读取
	sess, err := mgr1.StartLogin(ctx, sub, LoginModePhone, "RPC 号", "+8613800000777", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr2.GetLogin(ctx, otherSub, sess.LoginID); !errors.Is(err, ErrLoginNotFound) {
		t.Fatalf("越权成员不应读取登录会话，实际 %v", err)
	}
	gotSess, err := mgr2.GetLogin(ctx, sub, sess.LoginID)
	if err != nil || gotSess.Step != StepAwaitingCode {
		t.Fatalf("跨实例 GetLogin 失败：step=%v err=%v", gotSess, err)
	}
	doneSess, err := mgr2.SubmitCode(ctx, sub, sess.LoginID, "12345")
	if err != nil || doneSess.Step != StepConnected || doneSess.Account == nil {
		t.Fatalf("跨实例 SubmitCode 失败：%+v err=%v", doneSess, err)
	}
	acctID := doneSess.Account.ID

	// 2. 启动 mgr1 长连接，验证 mgr2 经 NATS RPC 调用全部操作
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	go func() { _ = mgr1.Run(runCtx) }()
	select {
	case <-be1.startedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("mgr1 未在超时内就绪")
	}

	// 更新白名单（通过 mgr2 触发 ctrlReload 广播到 mgr1）
	if _, err := mgr2.UpdateSyncedPeers(ctx, sub, acctID, []SyncedPeer{peer1}, 0); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		mgr1.runnersMu.RLock()
		r := mgr1.runners[acctID]
		mgr1.runnersMu.RUnlock()
		if r == nil {
			return false
		}
		_, ok := r.lookupPeer(peer1.ExternalChatID)
		return ok
	})

	if dialogs, err := mgr2.ListDialogs(ctx, sub, acctID); err != nil || len(dialogs) != 1 {
		t.Fatalf("跨实例 ListDialogs 失败：len=%d err=%v", len(dialogs), err)
	}
	if folders, err := mgr2.ListFolders(ctx, sub, acctID); err != nil || len(folders) != 1 {
		t.Fatalf("跨实例 ListFolders 失败：len=%d err=%v", len(folders), err)
	}
	if imported, err := mgr2.BackfillPeer(ctx, sub, acctID, peer1.ExternalChatID, 10); err != nil || imported != 1 {
		t.Fatalf("跨实例 BackfillPeer 失败：imported=%d err=%v", imported, err)
	}

	chModel, err := channel.Find(ctx, env.DB, tn.ID, acctID)
	if err != nil {
		t.Fatal(err)
	}
	extID, ok, err := mgr2.Connector().Send(ctx, channel.Outgoing{
		Channel:    chModel,
		ExternalTo: peer1.ExternalChatID,
		ItemID:     "item-rpc-001",
		Text:       "hello over nats rpc",
	})
	if err != nil || !ok || extID == "" {
		t.Fatalf("跨实例 Send 失败：extID=%q ok=%v err=%v", extID, ok, err)
	}
	live1.mu.Lock()
	sentCount := len(live1.sent)
	var gotRandomID int64
	if sentCount > 0 {
		gotRandomID = live1.sent[0].RandomID
	}
	live1.mu.Unlock()
	if sentCount != 1 || gotRandomID != deterministicRandomID("item-rpc-001", 0) {
		t.Fatalf("跨实例 Send 未保留确定性 random_id：count=%d got=%d", sentCount, gotRandomID)
	}

	// 验证 FetchMedia 经临时文件流式返回并在 Close 后软删除临时文件
	fetched, err := mgr2.Connector().(channel.MediaFetcher).FetchMedia(ctx, chModel, "photo-rpc", 1024*1024)
	if err != nil {
		t.Fatalf("跨实例 FetchMedia 失败：%v", err)
	}
	gotMedia, err := io.ReadAll(fetched.Body)
	if err != nil || !bytes.Equal(gotMedia, mediaBytes) {
		t.Fatalf("跨实例 FetchMedia 内容不一致：err=%v len=%d", err, len(gotMedia))
	}
	_ = fetched.Body.Close()

	activeFiles, err := env.DB.NewSelect().Table("files").
		Where("owner_id = ? AND status <> 'deleted'", string(channel.TelegramAccount)+":"+acctID).
		Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activeFiles != 0 {
		t.Fatalf("跨实例 FetchMedia 关闭后应删除临时暂存文件，实际残留 %d 条", activeFiles)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
