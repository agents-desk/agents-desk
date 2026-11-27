package wecom

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/lock"
	"github.com/go-tenon/tenon/outbound"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
)

const (
	natsSendPrefix   = "wecom.send."
	heartbeatPeriod  = 30 * time.Second
	frameAckTimeout  = 15 * time.Second
	uploadChunkBytes = 512 * 1024
)

// sendRPCRequest 是经 NATS 在多实例间转发给持有长连接实例的出站发送请求。
type sendRPCRequest struct {
	TargetKind   channel.DeliveryTargetKind `json:"target_kind"`
	ExternalTo   string                     `json:"external_to"`
	Text         string                     `json:"text"`
	ReplyReqID   string                     `json:"reply_req_id,omitempty"`
	Attachments  []rpcAttachment            `json:"attachments,omitempty"`
	Conversation string                     `json:"conversation_id"`
	ItemID       string                     `json:"item_id"`
}

type rpcAttachment struct {
	FileID   string `json:"file_id"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type sendRPCResponse struct {
	ExternalID string `json:"external_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// botRunner 管理单个企业微信机器人的 WebSocket 长连接、请求-回执配对和并发写序列化。
type botRunner struct {
	channelID string
	tenantID  string
	cancel    context.CancelFunc

	mu          sync.RWMutex
	conn        *websocket.Conn
	writeMu     sync.Mutex
	pending     map[string]chan Frame
	welcomeText string
}

func (r *botRunner) setConn(c *websocket.Conn) {
	r.mu.Lock()
	r.conn = c
	r.mu.Unlock()
}

func (r *botRunner) getConn() *websocket.Conn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conn
}

func (r *botRunner) setWelcomeText(text string) {
	r.mu.Lock()
	r.welcomeText = text
	r.mu.Unlock()
}

func (r *botRunner) getWelcomeText() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.welcomeText
}

// sendAndWait 向长连接发送一个命令帧并等待同 req_id 的回执帧。
func (r *botRunner) sendAndWait(ctx context.Context, cmd, reqID string, body any) (Frame, error) {
	conn := r.getConn()
	if conn == nil {
		return Frame{}, ErrNotConnected.New()
	}
	if reqID == "" {
		reqID = newID()
	}
	var rawBody json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Frame{}, errors.Wrap(err, "encode wecom frame body")
		}
		rawBody = b
	}
	frame := Frame{
		Cmd:     cmd,
		Headers: FrameHeaders{ReqID: reqID},
		Body:    rawBody,
	}
	payload, err := json.Marshal(frame)
	if err != nil {
		return Frame{}, errors.Wrap(err, "encode wecom frame")
	}

	ackCh := make(chan Frame, 1)
	r.mu.Lock()
	if r.pending == nil {
		r.pending = map[string]chan Frame{}
	}
	r.pending[reqID] = ackCh
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, reqID)
		r.mu.Unlock()
	}()

	r.writeMu.Lock()
	writeCtx, cancelWrite := context.WithTimeout(ctx, frameAckTimeout)
	err = conn.Write(writeCtx, websocket.MessageText, payload)
	cancelWrite()
	r.writeMu.Unlock()
	if err != nil {
		return Frame{}, errors.Wrap(err, "write wecom websocket frame")
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, frameAckTimeout)
	defer cancelWait()
	select {
	case resp := <-ackCh:
		if resp.ErrCode != 0 {
			return resp, errors.Newf("wecom %s error %d: %s", cmd, resp.ErrCode, resp.ErrMsg)
		}
		return resp, nil
	case <-waitCtx.Done():
		return Frame{}, errors.Wrapf(waitCtx.Err(), "wait wecom %s ack", cmd)
	}
}

func (r *botRunner) resolveAck(f Frame) bool {
	if f.Headers.ReqID == "" {
		return false
	}
	r.mu.RLock()
	ch := r.pending[f.Headers.ReqID]
	r.mu.RUnlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- f:
	default:
	}
	return true
}

// Manager 统一管理所有启用中的企业微信智能机器人 WebSocket 长连接、分布式租约与出站投递。
type Manager struct {
	db           *bun.DB
	crypt        *crypt.Crypt
	files        *filesystem.Service
	locker       *lock.Locker
	nc           *nats.Conn
	httpClient   *http.Client // 用于媒体下载（带超时与 SSRF 防护）
	wsHTTPClient *http.Client // 用于 WebSocket 建链（不设总请求超时，带 SSRF 防护）
	receive      *conversation.Receive

	runnersMu sync.RWMutex
	runners   map[string]*botRunner
	wakeCh    chan struct{}
}

var _ channel.Runner = (*Manager)(nil)

// NewManager 返回企业微信长连接管理器；httpClient 与 wsHTTPClient 默认使用带 SSRF 防护的 outbound 客户端。
func NewManager(db *bun.DB, c *crypt.Crypt, files *filesystem.Service, locker *lock.Locker, nc *nats.Conn) *Manager {
	return &Manager{
		db:           db,
		crypt:        c,
		files:        files,
		locker:       locker,
		nc:           nc,
		httpClient:   outbound.New(outbound.Options{Timeout: 30 * time.Second}),
		wsHTTPClient: outbound.New(outbound.Options{Timeout: -1}),
		runners:      map[string]*botRunner{},
		wakeCh:       make(chan struct{}, 1),
	}
}

// Name 返回运行器名称，实现 channel.Runner。
func (*Manager) Name() string { return "wecom" }

// SetReceive 绑定消息入库动作。
func (m *Manager) SetReceive(r *conversation.Receive) { m.receive = r }

// SetHTTPClients 替换媒体下载与 WebSocket 建链客户端（供进程内单元测试接入 httptest 服务端）。
func (m *Manager) SetHTTPClients(mediaClient, wsClient *http.Client) {
	if mediaClient != nil {
		m.httpClient = mediaClient
	}
	if wsClient != nil {
		m.wsHTTPClient = wsClient
	}
}

// Wake 通知长连接管理器立即重新对齐数据库中的已启用机器人列表。
func (m *Manager) Wake() {
	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
}

// Restart 主动断开指定机器人的现有长连接并触发立即重连。
func (m *Manager) Restart(channelID string) {
	m.stopRunner(channelID)
	m.Wake()
}

// Run 常驻循环：周期对齐数据库中的启用机器人并为每个机器人维持 WebSocket 长连接。
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer m.stopAllRunners()

	m.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.reconcile(ctx)
		case <-m.wakeCh:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	var rows []channel.Channel
	err := m.db.NewSelect().Model(&rows).
		Where("kind = ? AND enabled = TRUE AND owner_member_id IS NULL", channel.WeCom).
		Where("EXISTS (SELECT 1 FROM tenants t WHERE t.id = channel.tenant_id AND t.disabled_at IS NULL)").
		Scan(ctx)
	if err != nil {
		slog.WarnContext(ctx, "load active wecom bots for sync", "error", err)
		return
	}
	desired := map[string]channel.Channel{}
	for _, ch := range rows {
		s, err := settingsOf(&ch)
		if err != nil || s.BotID == "" || s.Secret == "" || s.Status == StatusAuthFailed {
			continue
		}
		desired[ch.ID] = ch
	}

	m.runnersMu.Lock()
	for id, r := range m.runners {
		if _, ok := desired[id]; !ok {
			r.cancel()
			delete(m.runners, id)
		}
	}
	for id, ch := range desired {
		s, _ := settingsOf(&ch)
		if existing := m.runners[id]; existing != nil {
			existing.setWelcomeText(s.WelcomeText)
			continue
		}
		rCtx, cancel := context.WithCancel(ctx)
		runner := &botRunner{
			channelID:   id,
			tenantID:    ch.TenantID,
			cancel:      cancel,
			pending:     map[string]chan Frame{},
			welcomeText: s.WelcomeText,
		}
		m.runners[id] = runner
		chCopy := ch
		go m.runLoop(rCtx, &chCopy, runner)
	}
	m.runnersMu.Unlock()
}

func (m *Manager) runLoop(ctx context.Context, ch *channel.Channel, runner *botRunner) {
	backoff := 2 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		runOnce := func(runCtx context.Context) error {
			return m.connectAndServe(runCtx, ch, runner, func() { backoff = 2 * time.Second })
		}
		var err error
		if m.locker != nil {
			err = m.locker.Hold(ctx, "wecom:conn:"+ch.ID, 45*time.Second, runOnce)
		} else {
			err = runOnce(ctx)
		}
		runner.setConn(nil)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, lock.ErrNotHeld) {
			// 其他实例持有该机器人连接租约，静默退避等待
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
			continue
		}
		var authErr *authError
		if errors.As(err, &authErr) {
			m.recordStatus(context.Background(), ch.TenantID, ch.ID, StatusAuthFailed, authErr.Error(), false)
			slog.WarnContext(ctx, "wecom bot auth failed, pausing reconnect", "channel_id", ch.ID, "error", authErr)
			return
		}
		m.recordStatus(context.Background(), ch.TenantID, ch.ID, StatusDisconnected, err.Error(), false)
		slog.WarnContext(ctx, "wecom bot disconnected, reconnecting", "channel_id", ch.ID, "backoff", backoff, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			if backoff < time.Minute {
				backoff *= 2
			}
		}
	}
}

type authError struct {
	msg string
}

func (e *authError) Error() string { return e.msg }

// connectAndServe 建立一次 WebSocket 长连接、完成 aibot_subscribe 鉴权、挂载 NATS 跨实例出站订阅并处理收帧循环。
func (m *Manager) connectAndServe(ctx context.Context, ch *channel.Channel, runner *botRunner, onConnected func()) error {
	// 每次建链重读一次最新配置，确保凭据或接入地址更新生效
	fresh, err := find(ctx, m.db, ch.TenantID, ch.ID, false)
	if err != nil {
		return err
	}
	s, err := settingsOf(fresh)
	if err != nil {
		return err
	}
	secret, err := m.crypt.Decrypt(s.Secret)
	if err != nil {
		return &authError{msg: "decrypt secret failed"}
	}
	runner.setWelcomeText(s.WelcomeText)

	dialCtx, cancelDial := context.WithTimeout(ctx, 20*time.Second)
	conn, _, err := websocket.Dial(dialCtx, s.EffectiveWSURL(), &websocket.DialOptions{
		HTTPClient: m.wsHTTPClient,
	})
	cancelDial()
	if err != nil {
		return errors.Wrap(err, "dial wecom websocket")
	}
	conn.SetReadLimit(4 << 20)
	defer func() {
		runner.setConn(nil)
		_ = conn.CloseNow()
	}()
	runner.setConn(conn)

	// 单独启动读循环，便于 subscribe 同步等待回执
	readErrCh := make(chan error, 1)
	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	go func() {
		readErrCh <- m.readLoop(serveCtx, fresh, runner, conn)
	}()

	// 1. 发送 aibot_subscribe 鉴权订阅帧
	resp, err := runner.sendAndWait(serveCtx, CmdSubscribe, newID(), map[string]any{
		"bot_id": s.BotID,
		"secret": secret,
	})
	if err != nil {
		if isWeComAuthError(resp.ErrCode) {
			return &authError{msg: err.Error()}
		}
		return err
	}
	if onConnected != nil {
		onConnected()
	}
	m.recordStatus(serveCtx, fresh.TenantID, fresh.ID, StatusConnected, "", true)
	slog.InfoContext(serveCtx, "wecom bot websocket connected", "channel_id", fresh.ID, "bot_id", s.BotID)

	// 2. 订阅 NATS 跨实例出站投递主题，使任意 worker 实例均可经本连接发送回复
	var sub *nats.Subscription
	if m.nc != nil {
		sub, err = m.nc.Subscribe(natsSendPrefix+fresh.ID, func(msg *nats.Msg) {
			var req sendRPCRequest
			if err := json.Unmarshal(msg.Data, &req); err != nil {
				m.replyNATS(msg, sendRPCResponse{Error: err.Error()})
				return
			}
			sendCtx, cancel := context.WithTimeout(serveCtx, 30*time.Second)
			defer cancel()
			extID, sendErr := m.executeSend(sendCtx, runner, req)
			if sendErr != nil {
				m.replyNATS(msg, sendRPCResponse{Error: sendErr.Error()})
				return
			}
			m.replyNATS(msg, sendRPCResponse{ExternalID: extID})
		})
		if err != nil {
			return errors.Wrap(err, "subscribe wecom nats send subject")
		}
		defer func() { _ = sub.Unsubscribe() }()
	}

	// 3. 心跳循环：每 30 秒发送 ping 帧，连续失败即断开重连
	ticker := time.NewTicker(heartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusNormalClosure, "shutdown")
			return nil
		case err := <-readErrCh:
			return err
		case <-ticker.C:
			if _, err := runner.sendAndWait(serveCtx, CmdPing, newID(), nil); err != nil {
				return errors.Wrap(err, "wecom heartbeat failed")
			}
		}
	}
}

func (m *Manager) replyNATS(msg *nats.Msg, resp sendRPCResponse) {
	if msg.Reply == "" {
		return
	}
	raw, _ := json.Marshal(resp)
	_ = msg.Respond(raw)
}

func (m *Manager) readLoop(ctx context.Context, ch *channel.Channel, runner *botRunner, conn *websocket.Conn) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return errors.Wrap(err, "read wecom websocket frame")
		}
		var f Frame
		if err := json.Unmarshal(data, &f); err != nil {
			slog.WarnContext(ctx, "ignore invalid wecom json frame", "channel_id", ch.ID, "error", err)
			continue
		}
		if f.Cmd == "" {
			// 无 cmd 字段为命令回执帧，唤醒等待中的 sendAndWait
			runner.resolveAck(f)
			continue
		}
		switch f.Cmd {
		case CmdMsgCallback:
			var body MsgCallbackBody
			if err := json.Unmarshal(f.Body, &body); err != nil {
				slog.WarnContext(ctx, "decode wecom msg callback", "channel_id", ch.ID, "error", err)
				continue
			}
			go m.handleMessage(ctx, ch, f.Headers.ReqID, body)
		case CmdEventCallback:
			var body EventCallbackBody
			if err := json.Unmarshal(f.Body, &body); err != nil {
				slog.WarnContext(ctx, "decode wecom event callback", "channel_id", ch.ID, "error", err)
				continue
			}
			if body.Event.EventType == "disconnected_event" {
				return errors.New("wecom server requested disconnect (new connection replaced)")
			}
			if body.Event.EventType == "enter_chat" {
				go m.handleEnterChat(ctx, ch, runner, f.Headers.ReqID)
			}
		default:
			// 回执帧有时也回显 cmd，尝试唤醒 pending
			runner.resolveAck(f)
		}
	}
}

func (m *Manager) handleEnterChat(ctx context.Context, ch *channel.Channel, runner *botRunner, reqID string) {
	welcome := strings.TrimSpace(runner.getWelcomeText())
	if welcome == "" || reqID == "" {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := runner.sendAndWait(sendCtx, CmdRespondWelcome, reqID, map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": welcome},
	}); err != nil {
		slog.WarnContext(ctx, "send wecom welcome message failed", "channel_id", ch.ID, "error", err)
	}
}

// handleMessage 将企微 aibot_msg_callback 转换为统一的 conversation.ReceiveParams 并落库。
func (m *Manager) handleMessage(ctx context.Context, ch *channel.Channel, reqID string, body MsgCallbackBody) {
	if m.receive == nil {
		return
	}
	handleCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	isGroup := body.ChatType == "group"
	fromID := strings.TrimSpace(body.From.UserID)
	if fromID == "" {
		slog.WarnContext(handleCtx, "ignore wecom message with empty userid", "channel_id", ch.ID, "msgid", body.MsgID)
		return
	}
	fromName := strings.TrimSpace(body.From.Name)
	if fromName == "" {
		fromName = fromID
	}

	text, atts := extractWeComContent(body)
	if text == "" && len(atts) == 0 {
		slog.InfoContext(handleCtx, "ignore empty or unsupported wecom message", "channel_id", ch.ID, "msgid", body.MsgID, "msgtype", body.MsgType)
		return
	}

	targetChatID := strings.TrimSpace(body.ChatID)
	if !isGroup && targetChatID == "" {
		targetChatID = fromID
	}
	replyCtx := map[string]any{
		"chattype":    body.ChatType,
		"chatid":      targetChatID,
		"received_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if reqID != "" {
		replyCtx["req_id"] = reqID
	}

	var replyToExtID string
	if body.Quote != nil && strings.TrimSpace(body.Quote.MsgID) != "" {
		replyToExtID = strings.TrimSpace(body.Quote.MsgID)
	}

	params := conversation.ReceiveParams{
		ChannelID:           ch.ID,
		From:                fromID,
		FromName:            fromName,
		ExternalID:          strings.TrimSpace(body.MsgID),
		ReplyToExternalID:   replyToExtID,
		Text:                text,
		IncomingAttachments: atts,
		ReplyContext:        replyCtx,
	}
	if isGroup {
		params.IsGroup = true
		params.ExternalChatID = targetChatID
		params.ChatTitle = strings.TrimSpace(body.ChatName)
		if params.ChatTitle == "" {
			params.ChatTitle = targetChatID
		}
	}
	sys := identity.Subject{Type: identity.System, ID: "wecom:" + ch.ID, Tenant: ch.TenantID}
	if _, err := m.receive.Handle(handleCtx, sys, params); err != nil {
		slog.ErrorContext(handleCtx, "receive wecom message failed", "channel_id", ch.ID, "msgid", body.MsgID, "error", err)
	}
}

func extractWeComContent(body MsgCallbackBody) (string, []conversation.IncomingAttachment) {
	var textParts []string
	var atts []conversation.IncomingAttachment

	addMedia := func(kind conversation.AttachmentKind, mp *MediaPayload, defaultName string, autoFetch bool) {
		if mp == nil || strings.TrimSpace(mp.URL) == "" {
			return
		}
		name := strings.TrimSpace(mp.FileName)
		if name == "" {
			name = defaultName
		}
		var unavail string
		if mp.FileSize > int64(MaxDownloadSize) {
			unavail = channel.ErrMediaTooLarge.Code()
		}
		atts = append(atts, conversation.IncomingAttachment{
			Kind:        kind,
			Filename:    name,
			Size:        mp.FileSize,
			SourceRef:   encodeMediaSourceRef(mediaSourceRef{URL: mp.URL, AESKey: mp.AESKey, Filename: name}),
			AutoFetch:   autoFetch && mp.FileSize <= int64(MaxDownloadSize),
			Unavailable: unavail,
		})
	}

	switch body.MsgType {
	case "text":
		if body.Text != nil {
			if s := strings.TrimSpace(body.Text.Content); s != "" {
				textParts = append(textParts, s)
			}
		}
	case "voice":
		if body.Voice != nil {
			if s := strings.TrimSpace(body.Voice.Content); s != "" {
				textParts = append(textParts, s)
			}
			if strings.TrimSpace(body.Voice.URL) != "" {
				addMedia(conversation.AttachmentAudio, &MediaPayload{URL: body.Voice.URL, AESKey: body.Voice.AESKey}, "voice.amr", true)
			}
		}
	case "image":
		addMedia(conversation.AttachmentImage, body.Image, "image.jpg", true)
	case "file":
		addMedia(conversation.AttachmentFile, body.File, "file", true)
	case "video":
		addMedia(conversation.AttachmentVideo, body.Video, "video.mp4", false)
	case "mixed":
		if body.Mixed != nil {
			for _, item := range body.Mixed.MsgItem {
				switch item.MsgType {
				case "text":
					if item.Text != nil {
						if s := strings.TrimSpace(item.Text.Content); s != "" {
							textParts = append(textParts, s)
						}
					}
				case "image":
					addMedia(conversation.AttachmentImage, item.Image, "image.jpg", true)
				}
			}
		}
	}
	return strings.Join(textParts, "\n"), atts
}

// executeSend 通过已在线的企微长连接发送文本和/或附件；优先在有效 req_id 窗口内用 aibot_respond_msg 回复，否则用 aibot_send_msg 主动发送。
func (m *Manager) executeSend(ctx context.Context, runner *botRunner, req sendRPCRequest) (string, error) {
	chatType := 1
	if req.TargetKind == channel.TargetGroup {
		chatType = 2
	}

	// 1. 发送文字内容
	var lastReqID string
	if strings.TrimSpace(req.Text) != "" {
		if req.ReplyReqID != "" && len(req.Attachments) == 0 {
			_, err := runner.sendAndWait(ctx, CmdRespondMsg, req.ReplyReqID, map[string]any{
				"msgtype": "markdown",
				"markdown": map[string]any{
					"content": req.Text,
				},
				"finish": true,
			})
			if err == nil {
				return fmt.Sprintf("wecom:%s", req.ItemID), nil
			}
			// 若被动回复窗口过期或 req_id 已用完，自动降级为 aibot_send_msg 主动发送
			slog.InfoContext(ctx, "wecom respond_msg failed, falling back to send_msg", "channel_id", runner.channelID, "error", err)
		}
		lastReqID = newID()
		if _, err := runner.sendAndWait(ctx, CmdSendMsg, lastReqID, map[string]any{
			"chatid":    req.ExternalTo,
			"chat_type": chatType,
			"msgtype":   "markdown",
			"markdown": map[string]any{
				"content": req.Text,
			},
		}); err != nil {
			return "", err
		}
	}

	// 2. 逐个分片上传并发送媒体附件
	if len(req.Attachments) > 0 {
		if m.files == nil {
			return "", errors.New("wecom: filesystem service is required to send attachments")
		}
		for _, att := range req.Attachments {
			mediaType := weComMediaType(att.Kind)
			mediaID, err := m.uploadMedia(ctx, runner, att, mediaType)
			if err != nil {
				return "", err
			}
			lastReqID = newID()
			if _, err := runner.sendAndWait(ctx, CmdSendMsg, lastReqID, map[string]any{
				"chatid":    req.ExternalTo,
				"chat_type": chatType,
				"msgtype":   mediaType,
				mediaType:   map[string]any{"media_id": mediaID},
			}); err != nil {
				return "", err
			}
		}
	}
	if lastReqID == "" {
		lastReqID = req.ItemID
	}
	return fmt.Sprintf("wecom:%s", lastReqID), nil
}

// uploadMedia 按企微智能机器人长连接三步协议（init -> chunk -> finish）分片上传媒体文件并返回 media_id。
func (m *Manager) uploadMedia(ctx context.Context, runner *botRunner, att rpcAttachment, mediaType string) (string, error) {
	fileUUID, err := parseUUID(att.FileID)
	if err != nil {
		return "", errors.Wrap(err, "parse wecom attachment file id")
	}
	rc, info, err := m.files.Open(ctx, fileUUID)
	if err != nil {
		return "", errors.Wrap(err, "open wecom attachment file")
	}
	defer func() { _ = rc.Close() }()

	size := att.Size
	if size <= 0 && info.Size > 0 {
		size = info.Size
	}
	filename := strings.TrimSpace(att.Filename)
	if filename == "" {
		filename = "file"
	}
	totalChunks := int((size + uploadChunkBytes - 1) / uploadChunkBytes)
	if totalChunks < 1 {
		totalChunks = 1
	}

	initResp, err := runner.sendAndWait(ctx, CmdUploadInit, newID(), map[string]any{
		"type":         mediaType,
		"filename":     filename,
		"total_size":   size,
		"total_chunks": totalChunks,
	})
	if err != nil {
		return "", errors.Wrap(err, "wecom upload init")
	}
	var initBody struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(initResp.Body, &initBody); err != nil || initBody.UploadID == "" {
		return "", errors.New("wecom: missing upload_id in init response")
	}

	buf := make([]byte, uploadChunkBytes)
	for idx := 0; ; idx++ {
		n, readErr := rc.Read(buf)
		if n > 0 {
			if _, err := runner.sendAndWait(ctx, CmdUploadChunk, newID(), map[string]any{
				"upload_id":   initBody.UploadID,
				"chunk_index": idx,
				"base64_data": base64.StdEncoding.EncodeToString(buf[:n]),
			}); err != nil {
				return "", errors.Wrapf(err, "wecom upload chunk %d", idx)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return "", readErr
			}
			break
		}
	}

	finishResp, err := runner.sendAndWait(ctx, CmdUploadFinish, newID(), map[string]any{
		"upload_id": initBody.UploadID,
	})
	if err != nil {
		return "", errors.Wrap(err, "wecom upload finish")
	}
	var finishBody struct {
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(finishResp.Body, &finishBody); err != nil || finishBody.MediaID == "" {
		return "", errors.New("wecom: missing media_id in finish response")
	}
	return finishBody.MediaID, nil
}

func weComMediaType(kind string) string {
	switch kind {
	case "image":
		return "image"
	case "video":
		return "video"
	case "audio":
		return "voice"
	default:
		return "file"
	}
}

func (m *Manager) recordStatus(ctx context.Context, tenantID, channelID string, status ConnectStatus, lastErr string, touchConnected bool) {
	nowUTC := time.Now().UTC()
	var err error
	if touchConnected {
		_, err = m.db.NewRaw(`UPDATE channels
			SET settings = jsonb_set(jsonb_set(jsonb_set(settings, '{status}', to_jsonb(?::text), true), '{last_error}', to_jsonb(?::text), true), '{last_connected_at}', to_jsonb(?::text), true),
			    updated_at = ?
			WHERE id = ? AND tenant_id = ?`,
			string(status), lastErr, nowUTC.Format(time.RFC3339Nano), nowUTC, channelID, tenantID).Exec(ctx)
	} else {
		_, err = m.db.NewRaw(`UPDATE channels
			SET settings = jsonb_set(jsonb_set(settings, '{status}', to_jsonb(?::text), true), '{last_error}', to_jsonb(?::text), true),
			    updated_at = ?
			WHERE id = ? AND tenant_id = ?`,
			string(status), lastErr, nowUTC, channelID, tenantID).Exec(ctx)
	}
	if err != nil {
		slog.WarnContext(ctx, "update wecom bot status", "channel_id", channelID, "error", err)
	}
}

func (m *Manager) stopRunner(channelID string) {
	m.runnersMu.Lock()
	if r := m.runners[channelID]; r != nil {
		r.cancel()
		delete(m.runners, channelID)
	}
	m.runnersMu.Unlock()
}

func (m *Manager) stopAllRunners() {
	m.runnersMu.Lock()
	for id, r := range m.runners {
		r.cancel()
		delete(m.runners, id)
	}
	m.runnersMu.Unlock()
}

func (m *Manager) localRunner(channelID string) *botRunner {
	m.runnersMu.RLock()
	defer m.runnersMu.RUnlock()
	return m.runners[channelID]
}
