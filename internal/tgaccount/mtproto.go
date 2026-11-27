package tgaccount

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/gotd/td/session"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"rsc.io/qr"

	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/platformsettings"
)

// MTProtoConfig 是建立 MTProto 连接所需的应用凭据与可选 SOCKS5 代理配置。
type MTProtoConfig struct {
	APIID    int
	APIHash  string
	ProxyURL string
}

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

// IncomingMedia 是从 MTProto 消息中提取的一个入站媒体附件元数据与下载引用。
type IncomingMedia struct {
	Kind        conversation.AttachmentKind
	Filename    string
	ContentType string
	Size        int64
	Width       int
	Height      int
	DurationMs  int
	IsVoice     bool
	SourceRef   string
	AutoFetch   bool
}

// IncomingMessage 是从 MTProto 实时更新或历史拉取中解析出的一条 Telegram 消息。
type IncomingMessage struct {
	ExternalChatID string
	MessageID      int
	SenderID       string // 发送者的渠道外部标识，如 tg:user:12345
	SenderName     string
	Text           string
	FromOwner      bool
	Mentioned      bool // 群聊中是否 @ 提及或回复了当前账号
	Media          []IncomingMedia
	CreatedAt      time.Time
}

// LoginDriver 封装一次交互式登录（扫码或手机号）的后台 MTProto 生命周期，支持在测试中替换模拟实现。
type LoginDriver interface {
	State() LoginState
	SubmitCode(ctx context.Context, code string) (LoginState, error)
	SubmitPassword(ctx context.Context, password string) (LoginState, error)
	Close()
}

// LiveClient 封装单个已连接 Telegram 个人号的 MTProto 操作，供拉取对话列表、文件夹、历史回填、媒体下载与发送消息使用。
type LiveClient interface {
	ListDialogs(ctx context.Context, limit int) ([]DialogDTO, error)
	ListFolders(ctx context.Context) ([]FolderDTO, error)
	FetchHistory(ctx context.Context, peer SyncedPeer, limit int) ([]IncomingMessage, error)
	DownloadMedia(ctx context.Context, ref string, maxBytes int64) (*channel.FetchedMedia, error)
	SendText(ctx context.Context, peer SyncedPeer, text string) (int, error)
	SendMedia(ctx context.Context, peer SyncedPeer, att channel.Attachment, data io.Reader, caption string) (int, error)
}

// Backend 抽象底层 MTProto 运行时，默认使用纯 Go 的 GotdBackend（github.com/gotd/td）。
type Backend interface {
	StartPhoneLogin(ctx context.Context, cfg MTProtoConfig, phone string) (LoginDriver, error)
	StartQRLogin(ctx context.Context, cfg MTProtoConfig) (LoginDriver, error)
	RunAccount(ctx context.Context, cfg MTProtoConfig, storage session.Storage,
		onMessage func(context.Context, IncomingMessage) error, onReady func(LiveClient)) error
	RunOneShot(ctx context.Context, cfg MTProtoConfig, storage session.Storage, fn func(context.Context, LiveClient) error) error
}

// GotdBackend 是基于纯 Go github.com/gotd/td 的默认 MTProto 实现。
type GotdBackend struct{}

// NewGotdBackend 返回纯 Go MTProto 后端。
func NewGotdBackend() Backend { return GotdBackend{} }

// gotdOptions 按 MTProtoConfig 构造 gotd.Options；配置了 SOCKS5 代理时通过 platformsettings.DialTelegram 拨号。
func gotdOptions(cfg MTProtoConfig, storage session.Storage, handler gotd.UpdateHandler, noUpdates bool) gotd.Options {
	opts := gotd.Options{
		SessionStorage: storage,
		UpdateHandler:  handler,
		NoUpdates:      noUpdates,
	}
	if proxyURL := strings.TrimSpace(cfg.ProxyURL); proxyURL != "" {
		opts.Resolver = dcs.Plain(dcs.PlainOptions{
			Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return platformsettings.DialTelegram(ctx, proxyURL, network, addr)
			},
		})
	}
	return opts
}

// StartPhoneLogin 启动后台 MTProto 客户端并向手机号 phone 发送登录验证码。
func (GotdBackend) StartPhoneLogin(ctx context.Context, cfg MTProtoConfig, phone string) (LoginDriver, error) {
	d := newGotdLoginDriver(cfg, LoginModePhone, phone)
	if err := d.waitInitial(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// StartQRLogin 启动后台 MTProto 客户端并生成首个扫码登录二维码。
func (GotdBackend) StartQRLogin(ctx context.Context, cfg MTProtoConfig) (LoginDriver, error) {
	d := newGotdLoginDriver(cfg, LoginModeQR, "")
	if err := d.waitInitial(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// RunAccount 为已授权的 Telegram 个人号建立长连接，监听 1:1 单聊与群聊新消息并保持 LiveClient 可用。
func (GotdBackend) RunAccount(ctx context.Context, cfg MTProtoConfig, storage session.Storage,
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

	client := gotd.NewClient(cfg.APIID, cfg.APIHash, gotdOptions(cfg, storage, dispatcher, false))
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

// RunOneShot 用已保存的会话临时连接 Telegram 执行一次操作（用于独立 worker 进程投递消息或拉取媒体时复用）。
func (GotdBackend) RunOneShot(ctx context.Context, cfg MTProtoConfig, storage session.Storage, fn func(context.Context, LiveClient) error) error {
	client := gotd.NewClient(cfg.APIID, cfg.APIHash, gotdOptions(cfg, storage, nil, true))
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

func newGotdLoginDriver(cfg MTProtoConfig, mode LoginMode, phone string) *gotdLoginDriver {
	runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	d := &gotdLoginDriver{
		cancel:     cancel,
		storage:    &memSessionStorage{},
		readyCh:    make(chan error, 1),
		codeCh:     make(chan codeSubmission, 1),
		passwordCh: make(chan passwordSubmission, 1),
	}
	go d.run(runCtx, cfg, mode, phone)
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

func (d *gotdLoginDriver) run(ctx context.Context, cfg MTProtoConfig, mode LoginMode, phone string) {
	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(dispatcher)
	client := gotd.NewClient(cfg.APIID, cfg.APIHash, gotdOptions(cfg, d.storage, dispatcher, false))
	err := client.Run(ctx, func(ctx context.Context) error {
		if mode == LoginModeQR {
			return d.runQR(ctx, client, cfg.APIID, cfg.APIHash, loggedIn)
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
	code, err := qr.Encode(token.URL(), qr.M)
	if err != nil {
		return "", errors.Wrap(err, "render telegram qr image")
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()), nil
}

// dialogEntry 是带联系人标记的内部对话项，供 ListDialogs 与 ListFolders 共享解析逻辑。
type dialogEntry struct {
	dto       DialogDTO
	isContact bool
}

// gotdLiveClient 实现 LiveClient，直接通过 MTProto tg.Client 读取对话、文件夹、拉取历史、下载媒体与发送文字/媒体消息。
type gotdLiveClient struct {
	api *tg.Client
}

// listDialogsWithMeta 按分页拉取最多 limit 条 1:1 单聊与超级群/普通群对话。
func (c *gotdLiveClient) listDialogsWithMeta(ctx context.Context, limit int) ([]dialogEntry, error) {
	if limit <= 0 {
		limit = 300
	}
	if limit > 300 {
		limit = 300
	}
	const pageSize = 100

	var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	var offsetDate, offsetID int
	seen := map[string]bool{}
	out := make([]dialogEntry, 0, min(limit, pageSize))

	for len(out) < limit {
		batch := min(pageSize, limit-len(out))
		res, err := c.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      batch,
		})
		if err != nil {
			return nil, errors.Wrap(err, "telegram get dialogs")
		}
		var dialogs []tg.DialogClass
		var messages []tg.MessageClass
		var users []tg.UserClass
		var chats []tg.ChatClass
		switch d := res.(type) {
		case *tg.MessagesDialogs:
			dialogs, messages, users, chats = d.Dialogs, d.Messages, d.Users, d.Chats
		case *tg.MessagesDialogsSlice:
			dialogs, messages, users, chats = d.Dialogs, d.Messages, d.Users, d.Chats
		default:
			return out, nil
		}
		if len(dialogs) == 0 {
			break
		}

		msgDates := map[int]int{}
		for _, mc := range messages {
			switch m := mc.(type) {
			case *tg.Message:
				msgDates[m.ID] = m.Date
			case *tg.MessageService:
				msgDates[m.ID] = m.Date
			}
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

		var lastPeer tg.InputPeerClass
		var lastMsgID, lastDate int
		for _, dc := range dialogs {
			dlg, ok := dc.(*tg.Dialog)
			if !ok {
				continue
			}
			lastMsgID = dlg.TopMessage
			if dt, ok := msgDates[dlg.TopMessage]; ok {
				lastDate = dt
			}
			switch p := dlg.Peer.(type) {
			case *tg.PeerUser:
				u, ok := userMap[p.UserID]
				if !ok {
					continue
				}
				lastPeer = &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
				if u.Self || u.Bot || u.Deleted {
					continue
				}
				key := formatPeerKey("user", u.ID)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, dialogEntry{
					dto: DialogDTO{SyncedPeer: SyncedPeer{
						ExternalChatID: key,
						PeerType:       "user",
						PeerID:         u.ID,
						AccessHash:     u.AccessHash,
						Title:          formatUserName(u),
						Username:       u.Username,
						IsGroup:        false,
					}},
					isContact: u.Contact,
				})
			case *tg.PeerChat:
				ch, ok := chatMap[p.ChatID]
				if !ok {
					continue
				}
				lastPeer = &tg.InputPeerChat{ChatID: ch.ID}
				if ch.Deactivated || ch.Left {
					continue
				}
				key := formatPeerKey("chat", ch.ID)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, dialogEntry{
					dto: DialogDTO{SyncedPeer: SyncedPeer{
						ExternalChatID: key,
						PeerType:       "chat",
						PeerID:         ch.ID,
						Title:          ch.Title,
						IsGroup:        true,
					}},
				})
			case *tg.PeerChannel:
				ch, ok := chanMap[p.ChannelID]
				if !ok {
					continue
				}
				lastPeer = &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
				// 仅同步群聊（Megagroup），忽略单向广播频道
				if !ch.Megagroup || ch.Left {
					continue
				}
				key := formatPeerKey("channel", ch.ID)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, dialogEntry{
					dto: DialogDTO{SyncedPeer: SyncedPeer{
						ExternalChatID: key,
						PeerType:       "channel",
						PeerID:         ch.ID,
						AccessHash:     ch.AccessHash,
						Title:          ch.Title,
						Username:       ch.Username,
						IsGroup:        true,
					}},
				})
			}
		}
		if len(dialogs) < batch || lastPeer == nil || (lastDate == offsetDate && lastMsgID == offsetID) {
			break
		}
		offsetPeer = lastPeer
		offsetID = lastMsgID
		offsetDate = lastDate
	}
	return out, nil
}

func (c *gotdLiveClient) ListDialogs(ctx context.Context, limit int) ([]DialogDTO, error) {
	entries, err := c.listDialogsWithMeta(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]DialogDTO, len(entries))
	for i := range entries {
		out[i] = entries[i].dto
	}
	return out, nil
}

// ListFolders 读取成员在 Telegram 客户端中创建的文件夹（Dialog Filter），并解析每个文件夹匹配的可同步单聊与群聊。
func (c *gotdLiveClient) ListFolders(ctx context.Context) ([]FolderDTO, error) {
	filtersRes, err := c.api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "telegram get dialog filters")
	}
	entries, err := c.listDialogsWithMeta(ctx, 300)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]SyncedPeer, len(entries))
	for _, e := range entries {
		byKey[e.dto.ExternalChatID] = e.dto.SyncedPeer
	}

	out := make([]FolderDTO, 0, len(filtersRes.Filters))
	for _, fc := range filtersRes.Filters {
		switch f := fc.(type) {
		case *tg.DialogFilter:
			title := strings.TrimSpace(f.Title.Text)
			if f.ID <= 0 || title == "" {
				continue
			}
			includeSet := inputPeerKeys(f.IncludePeers, f.PinnedPeers)
			excludeSet := inputPeerKeys(f.ExcludePeers)
			peers := make([]SyncedPeer, 0)
			added := map[string]bool{}
			for _, e := range entries {
				k := e.dto.ExternalChatID
				if excludeSet[k] || added[k] {
					continue
				}
				matched := includeSet[k] ||
					(!e.dto.IsGroup && e.isContact && f.Contacts) ||
					(!e.dto.IsGroup && !e.isContact && f.NonContacts) ||
					(e.dto.IsGroup && f.Groups)
				if matched {
					added[k] = true
					peers = append(peers, e.dto.SyncedPeer)
				}
			}
			out = append(out, FolderDTO{
				ID:        f.ID,
				Title:     title,
				PeerCount: len(peers),
				Peers:     peers,
			})
		case *tg.DialogFilterChatlist:
			title := strings.TrimSpace(f.Title.Text)
			if f.ID <= 0 || title == "" {
				continue
			}
			includeSet := inputPeerKeys(f.IncludePeers, f.PinnedPeers)
			peers := make([]SyncedPeer, 0, len(includeSet))
			for k := range includeSet {
				if p, ok := byKey[k]; ok {
					peers = append(peers, p)
				}
			}
			out = append(out, FolderDTO{
				ID:        f.ID,
				Title:     title,
				PeerCount: len(peers),
				Peers:     peers,
			})
		}
	}
	return out, nil
}

// inputPeerKeys 提取 InputPeerClass 切片中的外部会话键集合。
func inputPeerKeys(lists ...[]tg.InputPeerClass) map[string]bool {
	set := map[string]bool{}
	for _, list := range lists {
		for _, ip := range list {
			switch p := ip.(type) {
			case *tg.InputPeerUser:
				set[formatPeerKey("user", p.UserID)] = true
			case *tg.InputPeerChat:
				set[formatPeerKey("chat", p.ChatID)] = true
			case *tg.InputPeerChannel:
				set[formatPeerKey("channel", p.ChannelID)] = true
			}
		}
	}
	return set
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

// mediaRef 是保存在 IncomingAttachment.SourceRef 中的 MTProto 文件下载定位信息。
type mediaRef struct {
	Kind        string `json:"k"`
	ID          int64  `json:"id"`
	AccessHash  int64  `json:"ah"`
	FileRef     []byte `json:"fr,omitempty"`
	ThumbSize   string `json:"ts,omitempty"`
	Filename    string `json:"fn,omitempty"`
	ContentType string `json:"ct,omitempty"`
	Size        int64  `json:"sz,omitempty"`
}

func encodeMediaRef(r mediaRef) string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeMediaRef(raw string) (mediaRef, error) {
	var r mediaRef
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return r, channel.ErrMediaUnavailable.New()
	}
	if err := json.Unmarshal(b, &r); err != nil || r.ID == 0 {
		return r, channel.ErrMediaUnavailable.New()
	}
	return r, nil
}

// limitedWriter 限制单次媒体流写入的字节上限，超过 maxBytes 时立刻终止下载并返回 channel.ErrMediaTooLarge。
type limitedWriter struct {
	w        io.Writer
	written  int64
	maxBytes int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.maxBytes > 0 && l.written+int64(len(p)) > l.maxBytes {
		return 0, channel.ErrMediaTooLarge.New()
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	return n, err
}

// DownloadMedia 根据编码的 MTProto mediaRef 流式下载图片或文档附件正文。
func (c *gotdLiveClient) DownloadMedia(ctx context.Context, ref string, maxBytes int64) (*channel.FetchedMedia, error) {
	mr, err := decodeMediaRef(ref)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && mr.Size > maxBytes {
		return nil, channel.ErrMediaTooLarge.New()
	}
	var loc tg.InputFileLocationClass
	switch mr.Kind {
	case "photo":
		loc = &tg.InputPhotoFileLocation{
			ID:            mr.ID,
			AccessHash:    mr.AccessHash,
			FileReference: mr.FileRef,
			ThumbSize:     mr.ThumbSize,
		}
	case "doc":
		loc = &tg.InputDocumentFileLocation{
			ID:            mr.ID,
			AccessHash:    mr.AccessHash,
			FileReference: mr.FileRef,
			ThumbSize:     mr.ThumbSize,
		}
	default:
		return nil, channel.ErrMediaUnavailable.New()
	}

	pr, pw := io.Pipe()
	go func() {
		lw := &limitedWriter{w: pw, maxBytes: maxBytes}
		_, dlErr := downloader.NewDownloader().Download(c.api, loc).Stream(ctx, lw)
		if dlErr != nil {
			if tgerr.Is(dlErr, "FILE_REFERENCE_EXPIRED", "FILE_ID_INVALID", "LOCATION_INVALID") {
				dlErr = channel.ErrMediaUnavailable.Wrap(dlErr)
			}
			_ = pw.CloseWithError(dlErr)
			return
		}
		_ = pw.Close()
	}()

	size := mr.Size
	if size <= 0 {
		size = -1
	}
	return &channel.FetchedMedia{
		Body:        pr,
		Filename:    mr.Filename,
		ContentType: mr.ContentType,
		Size:        size,
	}, nil
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

// parseTGMessage 将 MTProto 消息对象解析为统一的 IncomingMessage，同时提取正文文本与图片/文档/音视频媒体元数据。
func parseTGMessage(mc tg.MessageClass, e tg.Entities) (IncomingMessage, bool) {
	m, ok := mc.(*tg.Message)
	if !ok {
		return IncomingMessage{}, false
	}
	text := strings.TrimSpace(m.Message)
	media := extractTGMedia(m.Media)
	if text == "" && len(media) == 0 {
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
		Mentioned:      m.Mentioned,
		Media:          media,
		CreatedAt:      time.Unix(int64(m.Date), 0).UTC(),
	}, true
}

// extractTGMedia 从 MTProto MessageMediaClass 中解析照片或文档附件的元数据与下载引用。
func extractTGMedia(mc tg.MessageMediaClass) []IncomingMedia {
	if mc == nil {
		return nil
	}
	switch m := mc.(type) {
	case *tg.MessageMediaPhoto:
		p, ok := m.Photo.(*tg.Photo)
		if !ok || p == nil {
			return nil
		}
		var bestType string
		var bestW, bestH int
		var bestSize int64
		for _, sz := range p.Sizes {
			switch s := sz.(type) {
			case *tg.PhotoSize:
				if s.W*s.H >= bestW*bestH {
					bestType, bestW, bestH, bestSize = s.Type, s.W, s.H, int64(s.Size)
				}
			case *tg.PhotoSizeProgressive:
				if s.W*s.H >= bestW*bestH {
					bestType, bestW, bestH = s.Type, s.W, s.H
					if len(s.Sizes) > 0 {
						bestSize = int64(s.Sizes[len(s.Sizes)-1])
					}
				}
			}
		}
		if bestType == "" {
			return nil
		}
		filename := fmt.Sprintf("photo_%d.jpg", p.ID)
		ref := encodeMediaRef(mediaRef{
			Kind:        "photo",
			ID:          p.ID,
			AccessHash:  p.AccessHash,
			FileRef:     p.FileReference,
			ThumbSize:   bestType,
			Filename:    filename,
			ContentType: "image/jpeg",
			Size:        bestSize,
		})
		return []IncomingMedia{{
			Kind:        conversation.AttachmentImage,
			Filename:    filename,
			ContentType: "image/jpeg",
			Size:        bestSize,
			Width:       bestW,
			Height:      bestH,
			SourceRef:   ref,
			AutoFetch:   true,
		}}
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok || doc == nil {
			return nil
		}
		mime := strings.TrimSpace(doc.MimeType)
		if mime == "" {
			mime = "application/octet-stream"
		}
		var filename string
		var width, height, durationMs int
		var isVoice bool
		kind := conversation.AttachmentFile
		if strings.HasPrefix(mime, "image/") {
			kind = conversation.AttachmentImage
		} else if strings.HasPrefix(mime, "video/") || m.Video {
			kind = conversation.AttachmentVideo
		} else if strings.HasPrefix(mime, "audio/") || m.Voice {
			kind = conversation.AttachmentAudio
			isVoice = m.Voice
		}
		for _, attr := range doc.Attributes {
			switch a := attr.(type) {
			case *tg.DocumentAttributeFilename:
				filename = strings.TrimSpace(a.FileName)
			case *tg.DocumentAttributeImageSize:
				width, height = a.W, a.H
			case *tg.DocumentAttributeVideo:
				kind = conversation.AttachmentVideo
				width, height = a.W, a.H
				durationMs = int(a.Duration * 1000)
			case *tg.DocumentAttributeAudio:
				kind = conversation.AttachmentAudio
				durationMs = a.Duration * 1000
				if a.Voice {
					isVoice = true
				}
			}
		}
		if filename == "" {
			filename = fmt.Sprintf("document_%d", doc.ID)
		}
		ref := encodeMediaRef(mediaRef{
			Kind:        "doc",
			ID:          doc.ID,
			AccessHash:  doc.AccessHash,
			FileRef:     doc.FileReference,
			Filename:    filename,
			ContentType: mime,
			Size:        doc.Size,
		})
		// 图片、语音与 5 MiB 以内小文件自动拉取；视频与大文件保持 remote 按需点击拉取
		autoFetch := kind == conversation.AttachmentImage ||
			(kind == conversation.AttachmentAudio && doc.Size <= int64(conversation.MaxAudioSize)) ||
			(kind == conversation.AttachmentFile && doc.Size > 0 && doc.Size <= 5*1024*1024)
		return []IncomingMedia{{
			Kind:        kind,
			Filename:    filename,
			ContentType: mime,
			Size:        doc.Size,
			Width:       width,
			Height:      height,
			DurationMs:  durationMs,
			IsVoice:     isVoice,
			SourceRef:   ref,
			AutoFetch:   autoFetch,
		}}
	default:
		return nil
	}
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
