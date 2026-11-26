package tgaccount

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/session"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/platformsettings"
	"agents-desk/internal/tenant"
)

// fakeLoginDriver 模拟纯 Go MTProto 交互式登录状态机（支持手机号 + 验证码 + 2FA 密码及扫码登录）。
type fakeLoginDriver struct {
	mu      sync.Mutex
	state   LoginState
	profile TelegramProfile
	need2FA bool
	closed  bool
}

func (f *fakeLoginDriver) State() LoginState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeLoginDriver) SubmitCode(_ context.Context, code string) (LoginState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code != "12345" {
		return f.state, ErrLoginFailed.New()
	}
	if f.need2FA {
		f.state.Step = StepAwaitingPassword
		f.state.PasswordHint = "work-2fa"
		return f.state, nil
	}
	f.state.Step = StepConnected
	f.state.Profile = f.profile
	f.state.SessionData = []byte(`{"dc":2,"key":"secret-session-payload"}`)
	return f.state, nil
}

func (f *fakeLoginDriver) SubmitPassword(_ context.Context, password string) (LoginState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if password != "s3cret" {
		return f.state, ErrLoginFailed.New()
	}
	f.state.Step = StepConnected
	f.state.Profile = f.profile
	f.state.SessionData = []byte(`{"dc":2,"key":"secret-2fa-session-payload"}`)
	return f.state, nil
}

func (f *fakeLoginDriver) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// fakeLiveClient 模拟已连接的 Telegram 个人号客户端，记录所有出站文本消息并提供预设对话和历史记录。
type fakeLiveClient struct {
	mu      sync.Mutex
	dialogs []DialogDTO
	history map[string][]IncomingMessage
	sent    []sentRecord
	nextID  int
}

type sentRecord struct {
	Peer SyncedPeer
	Text string
}

func (c *fakeLiveClient) ListDialogs(_ context.Context, _ int) ([]DialogDTO, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]DialogDTO, len(c.dialogs))
	copy(out, c.dialogs)
	return out, nil
}

func (c *fakeLiveClient) FetchHistory(_ context.Context, peer SyncedPeer, _ int) ([]IncomingMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]IncomingMessage(nil), c.history[peer.ExternalChatID]...), nil
}

func (c *fakeLiveClient) SendText(_ context.Context, peer SyncedPeer, text string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	c.sent = append(c.sent, sentRecord{Peer: peer, Text: text})
	return 500 + c.nextID, nil
}

func (c *fakeLiveClient) SendMedia(_ context.Context, peer SyncedPeer, _ channel.Attachment, _ io.Reader, caption string) (int, error) {
	return c.SendText(context.Background(), peer, caption)
}

// fakeBackend 模拟 MTProto 后端：可在测试中按会话标识精确注入实时更新并校验会话密文存取。
type fakeBackend struct {
	mu          sync.Mutex
	nextProfile TelegramProfile
	need2FA     bool
	live        *fakeLiveClient
	onMessages  map[string]func(context.Context, IncomingMessage) error
	startedCh   chan string
}

func (b *fakeBackend) StartPhoneLogin(_ context.Context, _ int, _, phone string) (LoginDriver, error) {
	b.mu.Lock()
	prof := b.nextProfile
	prof.Phone = phone
	need2FA := b.need2FA
	b.mu.Unlock()
	return &fakeLoginDriver{
		state:   LoginState{Step: StepAwaitingCode},
		profile: prof,
		need2FA: need2FA,
	}, nil
}

func (b *fakeBackend) StartQRLogin(_ context.Context, _ int, _ string) (LoginDriver, error) {
	b.mu.Lock()
	prof := b.nextProfile
	b.mu.Unlock()
	exp := time.Now().UTC().Add(time.Minute)
	return &fakeLoginDriver{
		state:   LoginState{Step: StepAwaitingQR, QRDataURL: "data:image/png;base64,AAAA", QRExpiresAt: &exp},
		profile: prof,
	}, nil
}

func (b *fakeBackend) RunAccount(ctx context.Context, _ int, _ string, storage session.Storage,
	onMessage func(context.Context, IncomingMessage) error, onReady func(LiveClient)) error {
	raw, err := storage.LoadSession(ctx)
	if err != nil {
		return err
	}
	key := string(raw)
	b.mu.Lock()
	if b.onMessages == nil {
		b.onMessages = map[string]func(context.Context, IncomingMessage) error{}
	}
	b.onMessages[key] = onMessage
	live := b.live
	ch := b.startedCh
	b.mu.Unlock()
	if onReady != nil {
		onReady(live)
	}
	if ch != nil {
		select {
		case ch <- key:
		default:
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (b *fakeBackend) RunOneShot(ctx context.Context, _ int, _ string, storage session.Storage, fn func(context.Context, LiveClient) error) error {
	if _, err := storage.LoadSession(ctx); err != nil {
		return err
	}
	return fn(ctx, b.live)
}

func TestTelegramPersonalAccountFullFlow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner@alpha.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sysAlpha := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	addMember := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files)
	alice, err := addMember.Handle(ctx, sysAlpha, member.AddParams{Email: "alice@alpha.test", Name: "Alice", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := addMember.Handle(ctx, sysAlpha, member.AddParams{Email: "bob@alpha.test", Name: "Bob", Password: "password", RoleID: agentRole.ID})
	if err != nil {
		t.Fatal(err)
	}
	subAlice := identity.Subject{Type: member.Type, ID: alice.ID, Tenant: alpha.ID, Permissions: auth.WithBase(nil)}
	subBob := identity.Subject{Type: member.Type, ID: bob.ID, Tenant: alpha.ID, Permissions: auth.WithBase(nil)}

	tgSettings := platformsettings.NewTelegramGroup(env.DB, env.Crypt)
	dmPeer := SyncedPeer{
		ExternalChatID: "tg:user:2001", PeerType: "user", PeerID: 2001, AccessHash: 11111,
		Title: "Rajesh Partner", Username: "rajesh_pay", IsGroup: false,
	}
	groupPeer := SyncedPeer{
		ExternalChatID: "tg:channel:3001", PeerType: "channel", PeerID: 3001, AccessHash: 22222,
		Title: "South Asia Settlement Group", IsGroup: true,
	}
	privatePeer := SyncedPeer{
		ExternalChatID: "tg:user:9999", PeerType: "user", PeerID: 9999, AccessHash: 33333,
		Title: "Family Chat (Unsynced)", IsGroup: false,
	}
	live := &fakeLiveClient{
		dialogs: []DialogDTO{{SyncedPeer: dmPeer}, {SyncedPeer: groupPeer}, {SyncedPeer: privatePeer}},
		history: map[string][]IncomingMessage{
			groupPeer.ExternalChatID: {
				{
					ExternalChatID: groupPeer.ExternalChatID,
					MessageID:      10,
					SenderID:       "tg:user:2001",
					SenderName:     "Rajesh Partner",
					Text:           "Historic group message",
					CreatedAt:      time.Now().UTC().Add(-5 * time.Minute),
				},
			},
		},
	}
	backend := &fakeBackend{
		nextProfile: TelegramProfile{UserID: 88001, Username: "alice_bd", DisplayName: "Alice BD"},
		need2FA:     true,
		live:        live,
		startedCh:   make(chan string, 4),
	}

	mgr := NewManager(env.DB, env.Crypt, env.Files, tgSettings, backend)
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	reg := channel.DefaultRegistry()
	reg[channel.TelegramAccount] = mgr.Connector()
	down := conversation.Downstream{Hub: hub, Roles: env.Roles}
	mgr.SetReceive(conversation.NewReceive(env.DB, reg, down))

	// 1. 平台未配置 Telegram API 凭据时，拒绝发起登录
	if _, err := mgr.StartLogin(ctx, subAlice, LoginModePhone, "Alice 主号", "+8613800138000"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("未配置 API 凭据应返回 ErrNotConfigured：%v", err)
	}
	if _, err := platformsettings.NewSaveTelegram(env.DB, tgSettings).Handle(ctx, apptest.Admin, platformsettings.Telegram{
		APIID: 123456, APIHash: "test-api-hash-0123456789abcdef",
	}); err != nil {
		t.Fatalf("保存平台 Telegram 凭据失败：%v", err)
	}

	// 2. 手机号 -> 验证码 -> 2FA 两步验证密码完整登录流程，并验证加密会话密文落库
	loginRes, err := mgr.StartLogin(ctx, subAlice, LoginModePhone, "Alice 主号", "+8613800138000")
	if err != nil || loginRes.Step != StepAwaitingCode {
		t.Fatalf("发起手机号登录失败：%v %+v", err, loginRes)
	}
	// 其他成员无法劫持 Alice 的登录会话
	if _, err := mgr.SubmitCode(ctx, subBob, loginRes.LoginID, "12345"); !errors.Is(err, ErrLoginNotFound) {
		t.Fatalf("Bob 不应访问 Alice 的登录会话：%v", err)
	}
	codeRes, err := mgr.SubmitCode(ctx, subAlice, loginRes.LoginID, "12345")
	if err != nil || codeRes.Step != StepAwaitingPassword || codeRes.PasswordHint != "work-2fa" {
		t.Fatalf("提交验证码后应进入 2FA 密码阶段：%v %+v", err, codeRes)
	}
	doneRes, err := mgr.SubmitPassword(ctx, subAlice, loginRes.LoginID, "s3cret")
	if err != nil || doneRes.Step != StepConnected || doneRes.Account == nil || doneRes.Account.Username != "alice_bd" {
		t.Fatalf("提交 2FA 密码完成登录失败：%v %+v", err, doneRes)
	}
	acctID := doneRes.Account.ID

	// 确认数据库中的 session_cipher 是密文，不泄露明文会话内容，且能正常解密回原字节
	rawCh, err := channel.Find(ctx, env.DB, alpha.ID, acctID)
	if err != nil {
		t.Fatal(err)
	}
	rawCfg, err := settingsOf(rawCh)
	if err != nil || rawCfg.SessionCipher == "" || strings.Contains(rawCfg.SessionCipher, "secret-2fa-session-payload") {
		t.Fatalf("Telegram 会话未正确加密落库：%+v", rawCfg)
	}
	plainSession, err := decryptSessionBytes(env.Crypt, rawCfg.SessionCipher)
	if err != nil || string(plainSession) != `{"dc":2,"key":"secret-2fa-session-payload"}` {
		t.Fatalf("解密 Telegram 会话异常：%v %q", err, string(plainSession))
	}

	// 同成员支持绑定第二个 Telegram 个人号（多号绑定）
	backend.mu.Lock()
	backend.nextProfile = TelegramProfile{UserID: 88002, Username: "alice_eu", DisplayName: "Alice EU"}
	backend.need2FA = false
	backend.mu.Unlock()
	login2, err := mgr.StartLogin(ctx, subAlice, LoginModePhone, "Alice 欧洲号", "+447700900123")
	if err != nil {
		t.Fatal(err)
	}
	done2, err := mgr.SubmitCode(ctx, subAlice, login2.LoginID, "12345")
	if err != nil || done2.Account == nil || done2.Account.ID == acctID {
		t.Fatalf("绑定第二个 Telegram 个人号失败：%v %+v", err, done2)
	}
	aliceAccounts, err := mgr.ListAccounts(ctx, subAlice)
	if err != nil || len(aliceAccounts) != 2 {
		t.Fatalf("Alice 应有 2 个绑定账号：%v %+v", err, aliceAccounts)
	}
	bobAccounts, err := mgr.ListAccounts(ctx, subBob)
	if err != nil || len(bobAccounts) != 0 {
		t.Fatalf("Bob 不应看到 Alice 的个人号：%v %+v", err, bobAccounts)
	}

	// 3. 选择性勾选同步 1:1 单聊与群聊（并回填群历史），忽略未勾选的私人聊天
	dialogs, err := mgr.ListDialogs(ctx, subAlice, acctID)
	if err != nil || len(dialogs) != 3 {
		t.Fatalf("拉取候选对话失败：%v %+v", err, dialogs)
	}
	updatedAcct, err := mgr.UpdateSyncedPeers(ctx, subAlice, acctID, []SyncedPeer{dmPeer, groupPeer}, 20)
	if err != nil || len(updatedAcct.SyncedPeers) != 2 {
		t.Fatalf("保存同步白名单失败：%v %+v", err, updatedAcct)
	}

	// 启动常驻同步循环，测试白名单过滤 + 双向收发 + 出站回声去重 + 手机端发消息同步
	runCtx, stopRun := context.WithCancel(ctx)
	doneRun := make(chan struct{})
	go func() {
		_ = mgr.Run(runCtx)
		close(doneRun)
	}()
	t.Cleanup(func() {
		stopRun()
		<-doneRun
	})

	const acct1Session = `{"dc":2,"key":"secret-2fa-session-payload"}`
	deadline := time.After(3 * time.Second)
	for {
		backend.mu.Lock()
		_, ready := backend.onMessages[acct1Session]
		backend.mu.Unlock()
		if ready {
			break
		}
		select {
		case <-backend.startedCh:
		case <-deadline:
			t.Fatal("等待 Telegram 个人号长连接就绪超时")
		}
	}

	// 推送一条未勾选的私人聊天消息 -> 应被严格忽略
	backend.mu.Lock()
	onMsg := backend.onMessages[acct1Session]
	backend.mu.Unlock()
	if err := onMsg(ctx, IncomingMessage{
		ExternalChatID: privatePeer.ExternalChatID,
		MessageID:      1,
		SenderID:       privatePeer.ExternalChatID,
		SenderName:     privatePeer.Title,
		Text:           "Private dinner tonight?",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// 推送一条已勾选的 1:1 商务单聊消息
	if err := onMsg(ctx, IncomingMessage{
		ExternalChatID: dmPeer.ExternalChatID,
		MessageID:      20,
		SenderID:       dmPeer.ExternalChatID,
		SenderName:     dmPeer.Title,
		Text:           "Hi Alice, checking contract status",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	listChats := conversation.NewListChats(env.DB)
	chats, err := listChats.Handle(ctx, subAlice, &conversation.ListChatsInput{
		CursorParams: page.CursorParams{PerPage: 20},
		Scope:        conversation.ChatExternal,
		ChannelID:    acctID,
	})
	if err != nil || len(chats.Items) != 2 {
		t.Fatalf("仅应同步白名单中的 1 个单聊 + 1 个回填群聊（私人聊天被忽略）：%v %+v", err, chats)
	}

	// 找回外部群聊并从平台发出一条回复 -> Deliver 通过 Connector.Send 发到 Telegram -> 模拟 Telegram 回声不重复入库
	var grpChatID string
	for _, ch := range chats.Items {
		if ch.Mode == conversation.ChatGroup {
			grpChatID = ch.ID
		}
	}
	reply, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, subAlice, grpChatID, conversation.SendChatMessageBody{
		Text: "Approved from agents-desk!",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.NewDeliver(env.DB, reg, down).Job(ctx, conversation.Appended{
		TenantID: alpha.ID, ConversationID: grpChatID, ItemID: reply.ID, Seq: reply.Seq,
	}); err != nil {
		t.Fatalf("投递 Telegram 个人号消息失败：%v", err)
	}
	if len(live.sent) != 1 || live.sent[0].Peer.ExternalChatID != groupPeer.ExternalChatID || live.sent[0].Text != "Approved from agents-desk!" {
		t.Fatalf("出站消息记录异常：%+v", live.sent)
	}

	// 模拟 Telegram 回声该条已发出消息（MessageID = 501）：应被去重跳过
	if err := onMsg(ctx, IncomingMessage{
		ExternalChatID: groupPeer.ExternalChatID,
		MessageID:      501,
		Text:           "Approved from agents-desk!",
		FromOwner:      true,
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// 模拟成员在手机 Telegram App 中直接发出的另一条消息（MessageID = 502）：应同步入库
	if err := onMsg(ctx, IncomingMessage{
		ExternalChatID: groupPeer.ExternalChatID,
		MessageID:      502,
		Text:           "Sent directly from my phone TG",
		FromOwner:      true,
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, subAlice, grpChatID, 0, 50)
	if err != nil || len(items) != 3 {
		t.Fatalf("外部群聊应包含 1 条历史 + 1 条平台回复（回声已去重）+ 1 条手机端同步消息：%v %+v", err, items)
	}

	// 4. 解绑未使用的第二个账号直接删除；解绑已有聊天记录的主账号则清空密文并停用
	if err := mgr.DisconnectAccount(ctx, subAlice, done2.Account.ID); err != nil {
		t.Fatalf("解绑未使用账号失败：%v", err)
	}
	if err := mgr.DisconnectAccount(ctx, subAlice, acctID); err != nil {
		t.Fatalf("解绑已使用账号失败：%v", err)
	}
	after, err := mgr.ListAccounts(ctx, subAlice)
	if err != nil || len(after) != 1 || after[0].ID != acctID || after[0].Enabled || after[0].Status != StatusDisconnected {
		t.Fatalf("解绑后账号列表异常：%v %+v", err, after)
	}
	rawAfter, err := channel.Find(ctx, env.DB, alpha.ID, acctID)
	if err != nil {
		t.Fatal(err)
	}
	cfgAfter, _ := settingsOf(rawAfter)
	if cfgAfter.SessionCipher != "" {
		t.Fatal("解绑后应清空加密会话密文")
	}
}
