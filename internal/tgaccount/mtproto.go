package tgaccount

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/gotd/td/session"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"rsc.io/qr"

	"agents-desk/internal/channel"
)

// TelegramProfile 是成功登录后的 Telegram 个人号基础资料。
type TelegramProfile struct {
	UserID      int64
	Phone       string
	Username    string
	DisplayName string
}

// LoginState 是交互式登录驱动器当前的快照状态。
type LoginState struct {
	Step         LoginStep
	QRDataURL    string
	QRExpiresAt  *time.Time
	PasswordHint string
	Error        string
	Profile      TelegramProfile
	SessionData  []byte
}

// IncomingMessage 是从 MTProto 实时更新或历史拉取中解析出的一条 Telegram 消息。
type IncomingMessage struct {
	ExternalChatID string
	MessageID      int
	SenderID       string // 发送者的渠道外部标识，如 tg:user:12345
	SenderName     string
	Text           string
	FromOwner      bool
	CreatedAt      time.Time
}

// LoginDriver 封装一次交互式登录（扫码或手机号）的后台 MTProto 生命周期，支持在测试中替换模拟实现。
type LoginDriver interface {
	State() LoginState
	SubmitCode(ctx context.Context, code string) (LoginState, error)
	SubmitPassword(ctx context.Context, password string) (LoginState, error)
	Close()
}

// LiveClient 封装单个已连接 Telegram 个人号的 MTProto 操作，供拉取对话列表、历史回填与发送消息使用。
type LiveClient interface {
	ListDialogs(ctx context.Context, limit int) ([]DialogDTO, error)
	FetchHistory(ctx context.Context, peer SyncedPeer, limit int) ([]IncomingMessage, error)
	SendText(ctx context.Context, peer SyncedPeer, text string) (int, error)
	SendMedia(ctx context.Context, peer SyncedPeer, att channel.Attachment, data io.Reader, caption string) (int, error)
}

// Backend 抽象底层 MTProto 运行时，默认使用纯 Go 的 GotdBackend（github.com/gotd/td）。
type Backend interface {
	StartPhoneLogin(ctx context.Context, appID int, appHash, phone string) (LoginDriver, error)
	StartQRLogin(ctx context.Context, appID int, appHash string) (LoginDriver, error)
	RunAccount(ctx context.Context, appID int, appHash string, storage session.Storage,
		onMessage func(context.Context, IncomingMessage) error, onReady func(LiveClient)) error
	RunOneShot(ctx context.Context, appID int, appHash string, storage session.Storage, fn func(context.Context, LiveClient) error) error
}

// GotdBackend 是基于纯 Go github.com/gotd/td 的默认 MTProto 实现。
type GotdBackend struct{}

// NewGotdBackend 返回纯 Go MTProto 后端。
func NewGotdBackend() Backend { return GotdBackend{} }

// StartPhoneLogin 启动后台 MTProto 客户端并向手机号 phone 发送登录验证码。
func (GotdBackend) StartPhoneLogin(ctx context.Context, appID int, appHash, phone string) (LoginDriver, error) {
	d := newGotdLoginDriver(appID, appHash, LoginModePhone, phone)
	if err := d.waitInitial(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// StartQRLogin 启动后台 MTProto 客户端并生成首个扫码登录二维码。
func (GotdBackend) StartQRLogin(ctx context.Context, appID int, appHash string) (LoginDriver, error) {
	d := newGotdLoginDriver(appID, appHash, LoginModeQR, "")
	if err := d.waitInitial(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// RunAccount 为已授权的 Telegram 个人号建立长连接，监听 1:1 单聊与群聊新消息并保持 LiveClient 可用。
func (GotdBackend) RunAccount(ctx context.Context, appID int, appHash string, storage session.Storage,
	onMessage func(context.Context, IncomingMessage) error, onReady func(LiveClient)) error {
	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		msg, ok := parseTGMessage(u.Message, e)
		if !ok {
			return nil
		}
		return onMessage(ctx, msg)
	})
	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		msg, ok := parseTGMessage(u.Message, e)
		if !ok {
			return nil
		}
		return onMessage(ctx, msg)
	})

	client := gotd.NewClient(appID, appHash, gotd.Options{
		SessionStorage: storage,
		UpdateHandler:  dispatcher,
	})
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return errors.Wrap(err, "check telegram auth status")
		}
		if !status.Authorized {
			return ErrNotConnected.New()
		}
		if onReady != nil {
			onReady(&gotdLiveClient{api: client.API()})
		}
		<-ctx.Done()
		return ctx.Err()
	})
}

// RunOneShot 用已保存的会话临时连接 Telegram 执行一次操作（用于独立 worker 进程投递消息时复用）。
func (GotdBackend) RunOneShot(ctx context.Context, appID int, appHash string, storage session.Storage, fn func(context.Context, LiveClient) error) error {
	client := gotd.NewClient(appID, appHash, gotd.Options{
		SessionStorage: storage,
		NoUpdates:      true,
	})
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return errors.Wrap(err, "check telegram auth status")
		}
		if !status.Authorized {
			return ErrNotConnected.New()
		}
		return fn(ctx, &gotdLiveClient{api: client.API()})
	})
}

// gotdLoginDriver 在单个长生命周期 MTProto 连接中驱动扫码或手机号登录（含 2FA），保证 DC 迁移与会话一致性。
type gotdLoginDriver struct {
	cancel     context.CancelFunc
	storage    *memSessionStorage
	readyCh    chan error
	codeCh     chan codeSubmission
	passwordCh chan passwordSubmission

	mu    sync.RWMutex
	state LoginState
}

type codeSubmission struct {
	code string
	resp chan submitResult
}

type passwordSubmission struct {
	password string
	resp     chan submitResult
}

type submitResult struct {
	state LoginState
	err   error
}

func newGotdLoginDriver(appID int, appHash string, mode LoginMode, phone string) *gotdLoginDriver {
	runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	d := &gotdLoginDriver{
		cancel:     cancel,
		storage:    &memSessionStorage{},
		readyCh:    make(chan error, 1),
		codeCh:     make(chan codeSubmission, 1),
		passwordCh: make(chan passwordSubmission, 1),
	}
	go d.run(runCtx, appID, appHash, mode, phone)
	return d
}

func (d *gotdLoginDriver) waitInitial(ctx context.Context) error {
	select {
	case err := <-d.readyCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *gotdLoginDriver) signalReady(err error) {
	select {
	case d.readyCh <- err:
	default:
	}
}

func (d *gotdLoginDriver) setState(fn func(*LoginState)) LoginState {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn(&d.state)
	return d.state
}

func (d *gotdLoginDriver) State() LoginState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}

func (d *gotdLoginDriver) Close() { d.cancel() }

func (d *gotdLoginDriver) SubmitCode(ctx context.Context, code string) (LoginState, error) {
	resp := make(chan submitResult, 1)
	select {
	case d.codeCh <- codeSubmission{code: strings.TrimSpace(code), resp: resp}:
	case <-ctx.Done():
		return d.State(), ctx.Err()
	}
	select {
	case r := <-resp:
		return r.state, r.err
	case <-ctx.Done():
		return d.State(), ctx.Err()
	}
}

func (d *gotdLoginDriver) SubmitPassword(ctx context.Context, password string) (LoginState, error) {
	resp := make(chan submitResult, 1)
	select {
	case d.passwordCh <- passwordSubmission{password: password, resp: resp}:
	case <-ctx.Done():
		return d.State(), ctx.Err()
	}
	select {
	case r := <-resp:
		return r.state, r.err
	case <-ctx.Done():
		return d.State(), ctx.Err()
	}
}

func (d *gotdLoginDriver) run(ctx context.Context, appID int, appHash string, mode LoginMode, phone string) {
	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(dispatcher)
	client := gotd.NewClient(appID, appHash, gotd.Options{
		SessionStorage: d.storage,
		UpdateHandler:  dispatcher,
	})
	err := client.Run(ctx, func(ctx context.Context) error {
		if mode == LoginModeQR {
			return d.runQR(ctx, client, appID, appHash, loggedIn)
		}
		return d.runPhone(ctx, client, phone)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("telegram account login ended with error", "mode", mode, "error", err)
		d.setState(func(s *LoginState) {
			if s.Step != StepConnected {
				s.Step = StepFailed
				s.Error = err.Error()
			}
		})
		d.signalReady(err)
	}
}

func (d *gotdLoginDriver) runQR(ctx context.Context, client *gotd.Client, appID int, appHash string, loggedIn qrlogin.LoggedIn) error {
	qrAuth := qrlogin.NewQR(client.API(), appID, appHash, qrlogin.Options{})
	authorized, err := qrAuth.Auth(ctx, loggedIn, func(_ context.Context, token qrlogin.Token) error {
		dataURL, encErr := encodeQRDataURL(token)
		if encErr != nil {
			return encErr
		}
		exp := token.Expires()
		d.setState(func(s *LoginState) {
			s.Step = StepAwaitingQR
			s.QRDataURL = dataURL
			s.QRExpiresAt = &exp
			s.Error = ""
		})
		d.signalReady(nil)
		return nil
	})
	if err != nil {
		if errors.Is(err, auth.ErrPasswordAuthNeeded) || tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
			return d.finishPassword(ctx, client)
		}
		return err
	}
	d.finishAuthorized(authorized, "")
	return nil
}

func (d *gotdLoginDriver) runPhone(ctx context.Context, client *gotd.Client, phone string) error {
	sent, err := client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return errors.Wrap(err, "send telegram login code")
	}
	sentCode, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return errors.New("telegram login code already confirmed")
	}
	codeHash := sentCode.PhoneCodeHash
	d.setState(func(s *LoginState) {
		s.Step = StepAwaitingCode
		s.Error = ""
	})
	d.signalReady(nil)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sub := <-d.codeCh:
			authorized, err := client.Auth().SignIn(ctx, phone, sub.code, codeHash)
			if err != nil {
				if errors.Is(err, auth.ErrPasswordAuthNeeded) || tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
					hint := loadPasswordHint(ctx, client.API())
					st := d.setState(func(s *LoginState) {
						s.Step = StepAwaitingPassword
						s.PasswordHint = hint
						s.Error = ""
					})
					sub.resp <- submitResult{state: st}
					return d.waitPasswordLoop(ctx, client, phone)
				}
				st := d.setState(func(s *LoginState) { s.Error = err.Error() })
				sub.resp <- submitResult{state: st, err: ErrLoginFailed.Wrap(err)}
				continue
			}
			st := d.finishAuthorized(authorized, phone)
			sub.resp <- submitResult{state: st}
			return nil
		}
	}
}

func (d *gotdLoginDriver) finishPassword(ctx context.Context, client *gotd.Client) error {
	hint := loadPasswordHint(ctx, client.API())
	d.setState(func(s *LoginState) {
		s.Step = StepAwaitingPassword
		s.PasswordHint = hint
		s.Error = ""
	})
	return d.waitPasswordLoop(ctx, client, "")
}

func (d *gotdLoginDriver) waitPasswordLoop(ctx context.Context, client *gotd.Client, phone string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sub := <-d.passwordCh:
			authorized, err := client.Auth().Password(ctx, sub.password)
			if err != nil {
				st := d.setState(func(s *LoginState) { s.Error = err.Error() })
				sub.resp <- submitResult{state: st, err: ErrLoginFailed.Wrap(err)}
				continue
			}
			st := d.finishAuthorized(authorized, phone)
			sub.resp <- submitResult{state: st}
			return nil
		}
	}
}

func (d *gotdLoginDriver) finishAuthorized(authorized *tg.AuthAuthorization, fallbackPhone string) LoginState {
	prof := TelegramProfile{Phone: fallbackPhone}
	if u, ok := authorized.User.AsNotEmpty(); ok {
		prof.UserID = u.ID
		prof.Username = u.Username
		if u.Phone != "" {
			prof.Phone = u.Phone
		}
		prof.DisplayName = formatUserName(u)
	}
	raw := d.storage.Bytes()
	return d.setState(func(s *LoginState) {
		s.Step = StepConnected
		s.Profile = prof
		s.SessionData = raw
		s.QRDataURL = ""
		s.Error = ""
	})
}

func loadPasswordHint(ctx context.Context, api *tg.Client) string {
	pw, err := api.AccountGetPassword(ctx)
	if err != nil {
		return ""
	}
	return pw.Hint
}

func encodeQRDataURL(token qrlogin.Token) (string, error) {
	img, err := token.Image(qr.M)
	if err != nil {
		return "", errors.Wrap(err, "render telegram qr image")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", errors.Wrap(err, "encode telegram qr png")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// gotdLiveClient 实现 LiveClient，直接通过 MTProto tg.Client 读取对话、拉取历史与发送文字/媒体消息。
type gotdLiveClient struct {
	api *tg.Client
}

func (c *gotdLiveClient) ListDialogs(ctx context.Context, limit int) ([]DialogDTO, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	res, err := c.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      limit,
	})
	if err != nil {
		return nil, errors.Wrap(err, "telegram get dialogs")
	}
	var dialogs []tg.DialogClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch d := res.(type) {
	case *tg.MessagesDialogs:
		dialogs, users, chats = d.Dialogs, d.Users, d.Chats
	case *tg.MessagesDialogsSlice:
		dialogs, users, chats = d.Dialogs, d.Users, d.Chats
	default:
		return []DialogDTO{}, nil
	}

	userMap := map[int64]*tg.User{}
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok {
			userMap[u.ID] = u
		}
	}
	chatMap := map[int64]*tg.Chat{}
	chanMap := map[int64]*tg.Channel{}
	for _, cc := range chats {
		switch ch := cc.(type) {
		case *tg.Chat:
			chatMap[ch.ID] = ch
		case *tg.Channel:
			chanMap[ch.ID] = ch
		}
	}

	out := make([]DialogDTO, 0, len(dialogs))
	for _, dc := range dialogs {
		dlg, ok := dc.(*tg.Dialog)
		if !ok {
			continue
		}
		switch p := dlg.Peer.(type) {
		case *tg.PeerUser:
			u, ok := userMap[p.UserID]
			if !ok || u.Self || u.Bot || u.Deleted {
				continue
			}
			out = append(out, DialogDTO{SyncedPeer: SyncedPeer{
				ExternalChatID: formatPeerKey("user", u.ID),
				PeerType:       "user",
				PeerID:         u.ID,
				AccessHash:     u.AccessHash,
				Title:          formatUserName(u),
				Username:       u.Username,
				IsGroup:        false,
			}})
		case *tg.PeerChat:
			ch, ok := chatMap[p.ChatID]
			if !ok || ch.Deactivated || ch.Left {
				continue
			}
			out = append(out, DialogDTO{SyncedPeer: SyncedPeer{
				ExternalChatID: formatPeerKey("chat", ch.ID),
				PeerType:       "chat",
				PeerID:         ch.ID,
				Title:          ch.Title,
				IsGroup:        true,
			}})
		case *tg.PeerChannel:
			ch, ok := chanMap[p.ChannelID]
			// 仅同步群聊（Megagroup），忽略单向广播频道
			if !ok || !ch.Megagroup || ch.Left {
				continue
			}
			out = append(out, DialogDTO{SyncedPeer: SyncedPeer{
				ExternalChatID: formatPeerKey("channel", ch.ID),
				PeerType:       "channel",
				PeerID:         ch.ID,
				AccessHash:     ch.AccessHash,
				Title:          ch.Title,
				Username:       ch.Username,
				IsGroup:        true,
			}})
		}
	}
	return out, nil
}

func (c *gotdLiveClient) FetchHistory(ctx context.Context, peer SyncedPeer, limit int) ([]IncomingMessage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	inputPeer, err := peer.toInputPeer()
	if err != nil {
		return nil, err
	}
	res, err := c.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  inputPeer,
		Limit: limit,
	})
	if err != nil {
		return nil, errors.Wrap(err, "telegram get history")
	}
	var msgs []tg.MessageClass
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesMessages:
		msgs, users, chats = m.Messages, m.Users, m.Chats
	case *tg.MessagesMessagesSlice:
		msgs, users, chats = m.Messages, m.Users, m.Chats
	case *tg.MessagesChannelMessages:
		msgs, users, chats = m.Messages, m.Users, m.Chats
	default:
		return nil, nil
	}

	entities := tg.Entities{
		Users:    map[int64]*tg.User{},
		Chats:    map[int64]*tg.Chat{},
		Channels: map[int64]*tg.Channel{},
	}
	for _, uc := range users {
		if u, ok := uc.(*tg.User); ok {
			entities.Users[u.ID] = u
		}
	}
	for _, cc := range chats {
		switch ch := cc.(type) {
		case *tg.Chat:
			entities.Chats[ch.ID] = ch
		case *tg.Channel:
			entities.Channels[ch.ID] = ch
		}
	}

	// MTProto 历史消息按时间倒序返回，反转为时间升序后依次回填
	out := make([]IncomingMessage, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		parsed, ok := parseTGMessage(msgs[i], entities)
		if !ok {
			continue
		}
		parsed.ExternalChatID = peer.ExternalChatID
		out = append(out, parsed)
	}
	return out, nil
}

func (c *gotdLiveClient) SendText(ctx context.Context, peer SyncedPeer, text string) (int, error) {
	inputPeer, err := peer.toInputPeer()
	if err != nil {
		return 0, err
	}
	upd, err := c.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     inputPeer,
		Message:  text,
		RandomID: randomInt64(),
	})
	if err != nil {
		return 0, errors.Wrap(err, "telegram send message")
	}
	return extractMessageID(upd), nil
}

func (c *gotdLiveClient) SendMedia(ctx context.Context, peer SyncedPeer, att channel.Attachment, data io.Reader, caption string) (int, error) {
	inputPeer, err := peer.toInputPeer()
	if err != nil {
		return 0, err
	}
	filename := att.Filename
	if filename == "" {
		filename = "attachment"
	}
	uploaded, err := uploader.NewUploader(c.api).FromReader(ctx, filename, data)
	if err != nil {
		return 0, errors.Wrap(err, "telegram upload media")
	}
	var media tg.InputMediaClass
	if att.Kind == "image" {
		media = &tg.InputMediaUploadedPhoto{File: uploaded}
	} else {
		mimeType := att.ContentType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		media = &tg.InputMediaUploadedDocument{
			File:       uploaded,
			MimeType:   mimeType,
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: filename}},
		}
	}
	upd, err := c.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer:     inputPeer,
		Media:    media,
		Message:  caption,
		RandomID: randomInt64(),
	})
	if err != nil {
		return 0, errors.Wrap(err, "telegram send media")
	}
	return extractMessageID(upd), nil
}

// parseTGMessage 将 MTProto 消息对象解析为统一的 IncomingMessage，过滤空文本与系统服务消息。
func parseTGMessage(mc tg.MessageClass, e tg.Entities) (IncomingMessage, bool) {
	m, ok := mc.(*tg.Message)
	if !ok {
		return IncomingMessage{}, false
	}
	text := strings.TrimSpace(m.Message)
	if text == "" {
		return IncomingMessage{}, false
	}
	extChatID, peerType, peerID, ok := peerKeyFromTG(m.PeerID)
	if !ok {
		return IncomingMessage{}, false
	}
	var senderID, senderName string
	if m.FromID != nil {
		if k, st, sid, ok := peerKeyFromTG(m.FromID); ok {
			senderID = k
			senderName = resolveEntityName(st, sid, e)
		}
	}
	if senderID == "" && peerType == "user" {
		senderID = formatPeerKey("user", peerID)
		senderName = resolveEntityName("user", peerID, e)
	}
	return IncomingMessage{
		ExternalChatID: extChatID,
		MessageID:      m.ID,
		SenderID:       senderID,
		SenderName:     senderName,
		Text:           text,
		FromOwner:      m.Out,
		CreatedAt:      time.Unix(int64(m.Date), 0).UTC(),
	}, true
}

func resolveEntityName(peerType string, id int64, e tg.Entities) string {
	switch peerType {
	case "user":
		if u, ok := e.Users[id]; ok {
			return formatUserName(u)
		}
	case "chat":
		if c, ok := e.Chats[id]; ok {
			return c.Title
		}
	case "channel":
		if c, ok := e.Channels[id]; ok {
			return c.Title
		}
	}
	return ""
}

func formatUserName(u *tg.User) string {
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if name != "" {
		return name
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return "Telegram User"
}

func randomInt64() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}
