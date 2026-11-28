package tgaccount

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/gotd/td/tgerr"
	"github.com/nats-io/nats.go"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/channel"
)

const (
	natsRPCPrefix   = "tgaccount.rpc."
	natsCtrlPrefix  = "tgaccount.ctrl."
	natsLoginPrefix = "tgaccount.login."

	ctrlReload     = "reload"
	ctrlRestart    = "restart"
	ctrlDisconnect = "disconnect"

	rpcCallTimeout  = 45 * time.Second
	mediaRPCTimeout = 2 * time.Minute
)

// errCtrlStopped 表示管理操作通过控制广播停用或断开了长连接。
var errCtrlStopped = errors.New("tgaccount: stopped by control signal")

type rpcError struct {
	Code          string `json:"code,omitempty"`
	Message       string `json:"message"`
	RetryAfterSec int    `json:"retry_after_sec,omitempty"`
	Permanent     bool   `json:"permanent,omitempty"`
}

type accountRPCRequest struct {
	Op         string              `json:"op"`
	Peer       SyncedPeer          `json:"peer,omitempty"`
	Text       string              `json:"text,omitempty"`
	Attachment *channel.Attachment `json:"attachment,omitempty"`
	Caption    string              `json:"caption,omitempty"`
	RandomID   int64               `json:"random_id,omitempty"`
	MediaRef   string              `json:"media_ref,omitempty"`
	MaxBytes   int64               `json:"max_bytes,omitempty"`
	Limit      int                 `json:"limit,omitempty"`
}

type accountRPCResponse struct {
	MessageID   int               `json:"message_id,omitempty"`
	Dialogs     []DialogDTO       `json:"dialogs,omitempty"`
	Folders     []FolderDTO       `json:"folders,omitempty"`
	History     []IncomingMessage `json:"history,omitempty"`
	MediaFileID string            `json:"media_file_id,omitempty"`
	Filename    string            `json:"filename,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Size        int64             `json:"size,omitempty"`
	Error       *rpcError         `json:"error,omitempty"`
}

type loginRPCRequest struct {
	Op       string `json:"op"`
	TenantID string `json:"tenant_id"`
	MemberID string `json:"member_id"`
	Code     string `json:"code,omitempty"`
	Password string `json:"password,omitempty"`
}

type loginRPCResponse struct {
	DTO   *LoginSessionDTO `json:"dto,omitempty"`
	Error *rpcError        `json:"error,omitempty"`
}

// encodeRPCError 将进程内的哨兵错误、限流等待与永久失败标记序列化为可跨 NATS 还原的结构体。
func encodeRPCError(err error) *rpcError {
	if err == nil {
		return nil
	}
	out := &rpcError{
		Message:   err.Error(),
		Permanent: queue.IsPermanent(err),
	}
	switch {
	case errors.Is(err, ErrNotConnected):
		out.Code = ErrNotConnected.Code()
	case errors.Is(err, ErrTemporarilyUnavailable):
		out.Code = ErrTemporarilyUnavailable.Code()
	case errors.Is(err, channel.ErrMediaUnavailable):
		out.Code = channel.ErrMediaUnavailable.Code()
	case errors.Is(err, channel.ErrMediaTooLarge):
		out.Code = channel.ErrMediaTooLarge.Code()
	case errors.Is(err, ErrPeerNotFound):
		out.Code = ErrPeerNotFound.Code()
	case errors.Is(err, ErrLoginNotFound):
		out.Code = ErrLoginNotFound.Code()
	case errors.Is(err, ErrLoginFailed):
		out.Code = ErrLoginFailed.Code()
	}
	if d := errorx.RetryAfterOf(err); d > 0 {
		out.RetryAfterSec = max(1, int((d+time.Second-1)/time.Second))
		out.Permanent = false
	} else if rpcErr, ok := tgerr.As(err); ok {
		switch rpcErr.Type {
		case "FLOOD_WAIT", "FLOOD_PREMIUM_WAIT", "SLOWMODE_WAIT":
			sec := rpcErr.Argument
			if sec <= 0 {
				sec = 5
			}
			out.RetryAfterSec = sec
			out.Permanent = false
		}
	}
	return out
}

// decodeRPCError 将 NATS RPC 返回的错误结构体还原为带错误码、限流等待与永久标记的 Go error。
func decodeRPCError(re *rpcError) error {
	if re == nil {
		return nil
	}
	cause := errors.New(re.Message)
	var err error
	switch re.Code {
	case ErrNotConnected.Code():
		err = ErrNotConnected.Wrap(cause)
	case ErrTemporarilyUnavailable.Code():
		err = ErrTemporarilyUnavailable.Wrap(cause)
	case channel.ErrMediaUnavailable.Code():
		err = channel.ErrMediaUnavailable.Wrap(cause)
	case channel.ErrMediaTooLarge.Code():
		err = channel.ErrMediaTooLarge.Wrap(cause)
	case ErrPeerNotFound.Code():
		err = ErrPeerNotFound.Wrap(cause)
	case ErrLoginNotFound.Code():
		err = ErrLoginNotFound.Wrap(cause)
	case ErrLoginFailed.Code():
		err = ErrLoginFailed.Wrap(cause)
	default:
		err = cause
	}
	if re.RetryAfterSec > 0 && !re.Permanent {
		return queue.RetryAfter(err, time.Duration(re.RetryAfterSec)*time.Second)
	}
	if re.Permanent && !queue.IsPermanent(err) {
		return queue.Permanent(err)
	}
	return err
}

func replyJSON(msg *nats.Msg, v any) {
	if msg.Reply == "" {
		return
	}
	raw, _ := json.Marshal(v)
	_ = msg.Respond(raw)
}

func (m *Manager) publishAccountCtrl(channelID, cmd string) {
	if m.nc == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	_ = m.nc.Publish(natsCtrlPrefix+channelID, []byte(cmd))
}

func (m *Manager) handleAccountCtrl(ctx context.Context, tenantID, channelID string, runner *accountRunner, restartRequested, stopRequested *atomic.Bool, cancelRun context.CancelFunc, data []byte) {
	switch string(data) {
	case ctrlReload:
		ch, err := channel.Find(ctx, m.db, tenantID, channelID)
		if err != nil {
			slog.WarnContext(ctx, "reload telegram account config failed", "channel_id", channelID, "error", err)
			return
		}
		if !ch.Enabled {
			stopRequested.Store(true)
			cancelRun()
			m.wake()
			return
		}
		cfg, err := settingsOf(ch)
		if err != nil {
			slog.WarnContext(ctx, "decode reloaded telegram account settings failed", "channel_id", channelID, "error", err)
			return
		}
		runner.setPeers(cfg.SyncedPeers)
		runner.setSync(cfg.resolvedSync())
	case ctrlRestart:
		restartRequested.Store(true)
		cancelRun()
	case ctrlDisconnect:
		stopRequested.Store(true)
		cancelRun()
		m.wake()
	}
}

func (m *Manager) handleAccountRPC(ctx context.Context, ch *channel.Channel, cli LiveClient, msg *nats.Msg) {
	var req accountRPCRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		replyJSON(msg, accountRPCResponse{Error: encodeRPCError(errors.Wrap(err, "decode tgaccount rpc request"))})
		return
	}
	timeout := rpcCallTimeout
	if req.Op == "fetch_media" || req.Op == "send_media" {
		timeout = mediaRPCTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var resp accountRPCResponse
	switch req.Op {
	case "send_text":
		msgID, err := cli.SendText(callCtx, req.Peer, req.Text, req.RandomID)
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.MessageID = msgID
		}
	case "send_media":
		if m.files == nil || req.Attachment == nil {
			resp.Error = encodeRPCError(errors.New("tgaccount: filesystem service and attachment are required"))
			break
		}
		fileUUID, err := uuid.Parse(req.Attachment.FileID)
		if err != nil {
			resp.Error = encodeRPCError(errors.Wrap(err, "parse attachment file id"))
			break
		}
		rc, _, err := m.files.Open(callCtx, fileUUID)
		if err != nil {
			resp.Error = encodeRPCError(errors.Wrap(err, "open attachment file"))
			break
		}
		msgID, err := cli.SendMedia(callCtx, req.Peer, *req.Attachment, rc, req.Caption, req.RandomID)
		_ = rc.Close()
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.MessageID = msgID
		}
	case "fetch_media":
		if m.files == nil {
			resp.Error = encodeRPCError(errors.New("tgaccount: filesystem service is required for cross-instance media fetch"))
			break
		}
		fetched, err := cli.DownloadMedia(callCtx, req.MediaRef, req.MaxBytes)
		if err != nil {
			resp.Error = encodeRPCError(err)
			break
		}
		size := fetched.Size
		if size <= 0 {
			size = filesystem.UnknownSize
		}
		filename := strings.TrimSpace(fetched.Filename)
		if filename == "" {
			filename = "media"
		}
		contentType := strings.TrimSpace(fetched.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		owner := identity.Subject{Type: identity.System, ID: string(channel.TelegramAccount) + ":" + ch.ID, Tenant: ch.TenantID}
		uploaded, upErr := m.files.Upload(callCtx, owner, filesystem.FileInput{
			Disk:        m.files.PrivateDisk(),
			Filename:    filename,
			ContentType: contentType,
			Size:        size,
		}, fetched.Body)
		_ = fetched.Body.Close()
		if upErr != nil {
			resp.Error = encodeRPCError(errors.Wrap(upErr, "stage downloaded telegram media"))
			break
		}
		if req.MaxBytes > 0 && uploaded.Size > req.MaxBytes {
			_ = m.files.Delete(context.WithoutCancel(callCtx), uploaded.ID)
			resp.Error = encodeRPCError(channel.ErrMediaTooLarge.New())
			break
		}
		resp.MediaFileID = uploaded.ID.String()
		resp.Filename = fetched.Filename
		resp.ContentType = fetched.ContentType
		resp.Size = uploaded.Size
	case "list_dialogs":
		dialogs, err := cli.ListDialogs(callCtx, req.Limit)
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.Dialogs = dialogs
		}
	case "list_folders":
		folders, err := cli.ListFolders(callCtx)
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.Folders = folders
		}
	case "fetch_history":
		history, err := cli.FetchHistory(callCtx, req.Peer, req.Limit)
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.History = history
		}
	default:
		resp.Error = encodeRPCError(errors.Newf("tgaccount: unknown rpc op %q", req.Op))
	}
	replyJSON(msg, resp)
}

func (m *Manager) requestAccountRPC(ctx context.Context, channelID string, req accountRPCRequest, timeout time.Duration) (accountRPCResponse, error) {
	if m.nc == nil {
		return accountRPCResponse{}, ErrTemporarilyUnavailable.New()
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return accountRPCResponse{}, errors.Wrap(err, "encode tgaccount rpc request")
	}
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	msg, err := m.nc.RequestWithContext(rpcCtx, natsRPCPrefix+channelID, payload)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) || errors.Is(err, context.DeadlineExceeded) {
			return accountRPCResponse{}, ErrTemporarilyUnavailable.Wrap(err)
		}
		return accountRPCResponse{}, errors.Wrap(err, "call tgaccount rpc")
	}
	var resp accountRPCResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return accountRPCResponse{}, errors.Wrap(err, "decode tgaccount rpc response")
	}
	if resp.Error != nil {
		return accountRPCResponse{}, decodeRPCError(resp.Error)
	}
	return resp, nil
}

func (m *Manager) rpcFetchMedia(ctx context.Context, ch *channel.Channel, ref string, maxBytes int64) (*channel.FetchedMedia, error) {
	if m.files == nil {
		return nil, errors.New("tgaccount: filesystem service is required for cross-instance media fetch")
	}
	resp, err := m.requestAccountRPC(ctx, ch.ID, accountRPCRequest{
		Op:       "fetch_media",
		MediaRef: ref,
		MaxBytes: maxBytes,
	}, mediaRPCTimeout)
	if err != nil {
		return nil, err
	}
	fid, err := uuid.Parse(resp.MediaFileID)
	if err != nil {
		return nil, errors.Wrap(err, "parse staged media file id")
	}
	rc, _, err := m.files.Open(ctx, fid)
	if err != nil {
		_ = m.files.Delete(context.WithoutCancel(ctx), fid)
		return nil, errors.Wrap(err, "open staged media file")
	}
	return &channel.FetchedMedia{
		Body: &cleanupReadCloser{
			ReadCloser: rc,
			onClose: func() {
				_ = m.files.Delete(context.WithoutCancel(ctx), fid)
			},
		},
		Filename:    resp.Filename,
		ContentType: resp.ContentType,
		Size:        resp.Size,
	}, nil
}

func (m *Manager) handleLoginRPC(entry *loginEntry, msg *nats.Msg) {
	var req loginRPCRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		replyJSON(msg, loginRPCResponse{Error: encodeRPCError(errors.Wrap(err, "decode tgaccount login rpc"))})
		return
	}
	if req.TenantID != entry.tenantID || req.MemberID != entry.memberID {
		replyJSON(msg, loginRPCResponse{Error: encodeRPCError(ErrLoginNotFound.New())})
		return
	}
	callCtx, cancel := context.WithTimeout(context.Background(), rpcCallTimeout)
	defer cancel()

	var resp loginRPCResponse
	switch req.Op {
	case "get":
		dto, err := m.buildLoginDTO(callCtx, entry)
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.DTO = dto
		}
	case "code":
		if _, err := entry.driver.SubmitCode(callCtx, req.Code); err != nil {
			resp.Error = encodeRPCError(err)
			break
		}
		dto, err := m.buildLoginDTO(callCtx, entry)
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.DTO = dto
		}
	case "password":
		if _, err := entry.driver.SubmitPassword(callCtx, req.Password); err != nil {
			resp.Error = encodeRPCError(err)
			break
		}
		dto, err := m.buildLoginDTO(callCtx, entry)
		if err != nil {
			resp.Error = encodeRPCError(err)
		} else {
			resp.DTO = dto
		}
	default:
		resp.Error = encodeRPCError(ErrLoginNotFound.New())
	}
	replyJSON(msg, resp)
}

func (m *Manager) forwardLoginRPC(ctx context.Context, sub identity.Subject, loginID string, req loginRPCRequest) (*LoginSessionDTO, error) {
	req.TenantID = sub.Tenant
	req.MemberID = sub.ID
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, "encode tgaccount login rpc")
	}
	rpcCtx, cancel := context.WithTimeout(ctx, rpcCallTimeout)
	defer cancel()
	msg, err := m.nc.RequestWithContext(rpcCtx, natsLoginPrefix+loginID, payload)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) {
			return nil, ErrLoginNotFound.New()
		}
		return nil, errors.Wrap(err, "call tgaccount login rpc")
	}
	var resp loginRPCResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, errors.Wrap(err, "decode tgaccount login rpc response")
	}
	if resp.Error != nil {
		return nil, decodeRPCError(resp.Error)
	}
	return resp.DTO, nil
}

type cleanupReadCloser struct {
	io.ReadCloser
	once    sync.Once
	onClose func()
}

func (c *cleanupReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}
