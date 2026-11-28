package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/coder/websocket"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/extgroup"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// fakeWeComServer 在进程内模拟企业微信智能机器人 WebSocket 长连接与加密媒体下载服务。
type fakeWeComServer struct {
	t           *testing.T
	validBotID  string
	validSecret string

	mu            sync.Mutex
	conn          *websocket.Conn
	connectedCh   chan struct{}
	received      []Frame
	failRespond   bool
	mediaPayloads map[string][]byte
}

func newFakeWeComServer(t *testing.T, botID, secret string) (*fakeWeComServer, *httptest.Server, string) {
	t.Helper()
	f := &fakeWeComServer{
		t:             t,
		validBotID:    botID,
		validSecret:   secret,
		connectedCh:   make(chan struct{}, 4),
		mediaPayloads: map[string][]byte{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	return f, srv, wsURL
}

func (f *fakeWeComServer) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/media/") {
		key := strings.TrimPrefix(r.URL.Path, "/media/")
		f.mu.Lock()
		data, ok := f.mediaPayloads[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	defer func() { _ = conn.CloseNow() }()

	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()

	ctx := r.Context()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var frame Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			return
		}
		f.mu.Lock()
		f.received = append(f.received, frame)
		failResp := f.failRespond
		f.mu.Unlock()

		ack := Frame{Headers: FrameHeaders{ReqID: frame.Headers.ReqID}}
		switch frame.Cmd {
		case CmdSubscribe:
			var body struct {
				BotID  string `json:"bot_id"`
				Secret string `json:"secret"`
			}
			_ = json.Unmarshal(frame.Body, &body)
			if body.BotID != f.validBotID || body.Secret != f.validSecret {
				ack.ErrCode = 40001
				ack.ErrMsg = "invalid secret"
			} else {
				select {
				case f.connectedCh <- struct{}{}:
				default:
				}
			}
		case CmdRespondMsg:
			if failResp {
				ack.ErrCode = 84009
				ack.ErrMsg = "req_id expired"
			}
		case CmdUploadInit:
			ack.Body = json.RawMessage(`{"upload_id":"up-test-1"}`)
		case CmdUploadFinish:
			ack.Body = json.RawMessage(`{"media_id":"media-test-1"}`)
		}
		rawAck, _ := json.Marshal(ack)
		if err := conn.Write(ctx, websocket.MessageText, rawAck); err != nil {
			return
		}
	}
}

func (f *fakeWeComServer) push(ctx context.Context, cmd, reqID string, body any) error {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	if conn == nil {
		return errors.New("no active websocket connection")
	}
	rawBody, _ := json.Marshal(body)
	raw, _ := json.Marshal(Frame{
		Cmd:     cmd,
		Headers: FrameHeaders{ReqID: reqID},
		Body:    rawBody,
	})
	return conn.Write(ctx, websocket.MessageText, raw)
}

func (f *fakeWeComServer) framesByCmd(cmd string) []Frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Frame
	for _, fr := range f.received {
		if fr.Cmd == cmd {
			out = append(out, fr)
		}
	}
	return out
}

func encryptTestAES256CBC(plain, key []byte) []byte {
	pad := aes.BlockSize - (len(plain) % aes.BlockSize)
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(out, padded)
	return out
}

func TestWeComLongConnectionLifecycleAndIsolation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	env := apptest.New(t)
	fake, srv, wsURL := newFakeWeComServer(t, "bot-it-001", "secret-it-xyz")

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Alpha", Slug: "alpha", OwnerEmail: "owner@alpha.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "Beta", Slug: "beta", OwnerEmail: "owner@beta.test", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	allPerms := auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))
	ownerAlpha := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: allPerms}
	ownerBeta := identity.Subject{Type: member.Type, ID: beta.OwnerID, Tenant: beta.ID, Permissions: allPerms}

	mgr := NewManager(env.DB, env.Crypt, env.Files, nil, nil)
	mgr.SetHTTPClients(srv.Client(), srv.Client())
	reg := channel.DefaultRegistry()
	reg[channel.WeCom] = mgr.Connector()
	down := conversation.Downstream{Roles: env.Roles, Registry: reg}
	mgr.SetReceive(conversation.NewReceive(env.DB, reg, down))

	// 1. 创建企业微信机器人（默认 internal）并启动长连接运行器
	bot, err := NewCreate(env.DB, env.Crypt, mgr.Wake).Handle(ctx, ownerAlpha, CreateBody{
		Name:        "IT 内部企微机器人",
		BotID:       "bot-it-001",
		Secret:      "secret-it-xyz",
		WSURL:       wsURL,
		WelcomeText: "你好，我是 IT 服务助手，请直接描述问题。",
	})
	if err != nil {
		t.Fatalf("创建企微机器人失败: %v", err)
	}
	if bot.Audience != channel.AudienceInternal || bot.BotID != "bot-it-001" {
		t.Fatalf("创建结果不对: %+v", bot)
	}
	// 跨工作区隔离
	if _, err := NewGet(env.DB).Handle(ctx, ownerBeta, bot.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("其他工作区不应读取 Alpha 的企微机器人: %v", err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- mgr.Run(ctx) }()

	select {
	case <-fake.connectedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("等待企微机器人 WebSocket 建链超时")
	}

	// 等待状态写回 connected
	waitUntil(t, 3*time.Second, func() bool {
		got, err := NewGet(env.DB).Handle(ctx, ownerAlpha, bot.ID)
		return err == nil && got.Status == StatusConnected
	})

	// 2. enter_chat 事件 -> 自动调用 aibot_respond_welcome_msg
	if err := fake.push(ctx, CmdEventCallback, "req-welcome-1", EventCallbackBody{
		MsgID:    "ev-1",
		ChatType: "single",
		From:     MsgSender{UserID: "emp_zhang", Name: "张工"},
		Event:    EventPayload{EventType: "enter_chat"},
	}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		return len(fake.framesByCmd(CmdRespondWelcome)) == 1
	})

	// 3. 员工 1:1 单聊 -> 创建 internal 联系人并进入收件箱，坐席回复走 aibot_respond_msg
	if err := fake.push(ctx, CmdMsgCallback, "req-dm-1", MsgCallbackBody{
		MsgID:    "wecom-msg-dm-1",
		ChatType: "single",
		From:     MsgSender{UserID: "emp_zhang", Name: "张工"},
		MsgType:  "text",
		Text:     &TextPayload{Content: "帮我重置邮箱密码"},
	}); err != nil {
		t.Fatal(err)
	}
	var dmConvID string
	waitUntil(t, 3*time.Second, func() bool {
		list, err := conversation.NewList(env.DB).Handle(ctx, ownerAlpha, &conversation.ListInput{Audience: channel.AudienceInternal})
		if err != nil || len(list.Items) == 0 {
			return false
		}
		dmConvID = list.Items[0].ID
		return true
	})
	contacts, err := contact.NewList(env.DB).Handle(ctx, ownerAlpha, &contact.ListInput{Kind: contact.Internal})
	if err != nil || len(contacts.Items) != 1 || contacts.Items[0].Name != "张工" {
		t.Fatalf("企微单聊应建内部联系人: %+v %v", contacts, err)
	}

	replyItem, err := conversation.NewReply(env.DB, down).Handle(ctx, ownerAlpha, dmConvID, conversation.ReplyBody{
		Text: "已为你重置，请查收短信。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.NewDeliver(env.DB, reg, down).Job(ctx, conversation.Appended{
		TenantID: alpha.ID, ConversationID: dmConvID, ItemID: replyItem.ID,
	}); err != nil {
		t.Fatalf("单聊回复投递失败: %v", err)
	}
	respFrames := fake.framesByCmd(CmdRespondMsg)
	if len(respFrames) != 1 || respFrames[0].Headers.ReqID != "req-dm-1" {
		t.Fatalf("期望使用 req-dm-1 走 aibot_respond_msg，实际: %+v", respFrames)
	}

	// 4. 群聊 @ 机器人 -> 创建外部群与 external_user，坐席回复带 <@userid>；
	//    模拟 req_id 失效时自动降级为 aibot_send_msg 主动发送
	fake.mu.Lock()
	fake.failRespond = true
	fake.mu.Unlock()

	if err := fake.push(ctx, CmdMsgCallback, "req-grp-1", MsgCallbackBody{
		MsgID:    "wecom-msg-grp-1",
		ChatID:   "wr-ops-001",
		ChatName: "基础设施运维群",
		ChatType: "group",
		From:     MsgSender{UserID: "emp_li", Name: "李工"},
		MsgType:  "text",
		Text:     &TextPayload{Content: "K8s 集群节点磁盘告警"},
	}); err != nil {
		t.Fatal(err)
	}
	var grpConvID string
	var grpID string
	waitUntil(t, 3*time.Second, func() bool {
		gs, err := extgroup.NewList(env.DB).Handle(ctx, ownerAlpha, &extgroup.ListInput{})
		if err != nil || len(gs) != 1 {
			return false
		}
		grpID = gs[0].ID
		list, err := conversation.NewList(env.DB).Handle(ctx, ownerAlpha, &conversation.ListInput{ExternalGroupID: grpID})
		if err != nil || len(list.Items) != 1 {
			return false
		}
		grpConvID = list.Items[0].ID
		return true
	})

	grpReply, err := conversation.NewReply(env.DB, down).Handle(ctx, ownerAlpha, grpConvID, conversation.ReplyBody{
		Text:             "已清理旧镜像，正在观察水位",
		ExternalMentions: []channel.ExternalMention{{ExternalID: "emp_li", Name: "李工"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conversation.NewDeliver(env.DB, reg, down).Job(ctx, conversation.Appended{
		TenantID: alpha.ID, ConversationID: grpConvID, ItemID: grpReply.ID,
	}); err != nil {
		t.Fatalf("群回复降级 send_msg 投递失败: %v", err)
	}
	sendFrames := fake.framesByCmd(CmdSendMsg)
	if len(sendFrames) != 1 {
		t.Fatalf("期望降级触发 1 次 aibot_send_msg，实际 %d", len(sendFrames))
	}
	var sentBody map[string]any
	_ = json.Unmarshal(sendFrames[0].Body, &sentBody)
	if sentBody["chatid"] != "wr-ops-001" || sentBody["chat_type"] != float64(2) {
		t.Fatalf("群主动发送目标不对: %+v", sentBody)
	}
	if md, _ := sentBody["markdown"].(map[string]any); md == nil || !strings.Contains(md["content"].(string), "<@emp_li>") {
		t.Fatalf("群回复应包含企微 <@emp_li> 提及: %+v", sentBody)
	}

	// 5. 测试 AES-256-CBC 加密媒体拉取
	aesKey := []byte("0123456789abcdef0123456789abcdef")
	plainData := []byte("hello encrypted wecom image bytes")
	fake.mu.Lock()
	fake.mediaPayloads["pic1.bin"] = encryptTestAES256CBC(plainData, aesKey)
	fake.mu.Unlock()

	ref := encodeMediaSourceRef(mediaSourceRef{
		URL:      srv.URL + "/media/pic1.bin",
		AESKey:   base64.StdEncoding.EncodeToString(aesKey),
		Filename: "screenshot.png",
	})
	fetcher := mgr.Connector().(channel.MediaFetcher)
	fetched, err := fetcher.FetchMedia(ctx, nil, ref, 1024*1024)
	if err != nil {
		t.Fatalf("加密媒体拉取解密失败: %v", err)
	}
	gotBytes, _ := io.ReadAll(fetched.Body)
	_ = fetched.Body.Close()
	if !bytes.Equal(gotBytes, plainData) {
		t.Fatalf("解密内容不匹配: got %q, want %q", gotBytes, plainData)
	}

	cancel()
	<-runDone

	// 手动重连企业微信机器人时保留 channels.settings.ai_reception
	if _, err := env.DB.NewRaw(`UPDATE channels SET settings = jsonb_set(settings, '{ai_reception}', '{"enabled":true,"bot_id":"bot-x"}'::jsonb) WHERE id = ?`, bot.ID).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReconnect(env.DB, nil).Handle(t.Context(), ownerAlpha, bot.ID); err != nil {
		t.Fatalf("重连企业微信机器人失败: %v", err)
	}
	var aiReception string
	if err := env.DB.NewRaw(`SELECT COALESCE(settings->'ai_reception'->>'bot_id', '') FROM channels WHERE id = ?`, bot.ID).Scan(t.Context(), &aiReception); err != nil || aiReception != "bot-x" {
		t.Fatalf("企微 Reconnect 不应覆盖 ai_reception：got %q err=%v", aiReception, err)
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
