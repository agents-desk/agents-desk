package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	"github.com/go-tenon/tenon/filesystem"

	"agents-desk/internal/channel"
)

// Connector 返回实现 channel.Connector 与 channel.MediaFetcher 的企业微信机器人连接器。
func (m *Manager) Connector() channel.Connector { return connector{mgr: m} }

type connector struct {
	mgr *Manager
}

var _ channel.MediaFetcher = connector{}

// Capabilities 返回企业微信智能机器人的能力：WebSocket 长连接传输，支持 1:1 单聊、群内 @ 机器人、群 @ 发言人与流式响应。
func (connector) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		Transport:         channel.TransportLongConnection,
		MessageTypes:      []string{"text", "image", "video", "audio", "file"},
		SupportsDirect:    true,
		SupportsGroup:     true,
		SupportsMention:   true,
		SupportsStreaming: true,
		ReadReceipts:      false,
	}
}

// Send 将坐席或系统的回复经企业微信长连接发出（本机未持有连接时通过 NATS 转发到持有连接的实例）。
func (c connector) Send(ctx context.Context, m channel.Outgoing) (string, bool, error) {
	targetTo := strings.TrimSpace(m.Target.ExternalTo)
	if targetTo == "" {
		targetTo = strings.TrimSpace(m.ExternalTo)
	}
	if targetTo == "" {
		return "", false, errors.New("wecom: empty recipient target")
	}
	targetKind := m.Target.Kind
	if targetKind == "" {
		targetKind = channel.TargetContact
	}
	outText := m.Text
	if targetKind == channel.TargetGroup && len(m.Mentions) > 0 {
		outText = formatGroupMentions(outText, m.Mentions)
	}

	req := sendRPCRequest{
		TargetKind:   targetKind,
		ExternalTo:   targetTo,
		Text:         outText,
		ReplyReqID:   extractFreshReplyReqID(m.ReplyContext),
		Conversation: m.ConversationID,
		ItemID:       m.ItemID,
	}
	if len(m.Attachments) > 0 {
		req.Attachments = make([]rpcAttachment, len(m.Attachments))
		for i, att := range m.Attachments {
			req.Attachments[i] = rpcAttachment{
				FileID:   att.FileID,
				Kind:     att.Kind,
				Filename: att.Filename,
				Size:     att.Size,
			}
		}
	}

	// 1. 优先复用本进程已在线的机器人长连接
	if r := c.mgr.localRunner(m.Channel.ID); r != nil && r.getConn() != nil {
		extID, err := c.mgr.executeSend(ctx, r, req)
		if err != nil {
			return "", false, err
		}
		return extID, true, nil
	}

	// 2. 本进程未持有连接时，经 NATS 转发给持有该机器人租约的实例
	if c.mgr.nc != nil {
		payload, err := json.Marshal(req)
		if err != nil {
			return "", false, errors.Wrap(err, "encode wecom send rpc")
		}
		rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		msg, err := c.mgr.nc.RequestWithContext(rpcCtx, natsSendPrefix+m.Channel.ID, payload)
		if err != nil {
			return "", false, ErrNotConnected.Wrap(err)
		}
		var resp sendRPCResponse
		if err := json.Unmarshal(msg.Data, &resp); err != nil {
			return "", false, errors.Wrap(err, "decode wecom send rpc response")
		}
		if resp.Error != "" {
			return "", false, errors.New(resp.Error)
		}
		return resp.ExternalID, true, nil
	}

	return "", false, ErrNotConnected.New()
}

// FetchMedia 实现 channel.MediaFetcher：使用受 SSRF 防护的 HTTP 客户端拉取企微媒体并在携带 aeskey 时解密。
func (c connector) FetchMedia(ctx context.Context, _ *channel.Channel, ref string, maxBytes int64) (*channel.FetchedMedia, error) {
	info, err := decodeMediaSourceRef(ref)
	if err != nil {
		return nil, err
	}
	limit := int64(MaxDownloadSize)
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	if err != nil {
		return nil, errors.Wrap(err, "build wecom media download request")
	}
	resp, err := c.mgr.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "download wecom media")
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		_ = resp.Body.Close()
		return nil, channel.ErrMediaUnavailable.New()
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, errors.Newf("wecom media download http %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		_ = resp.Body.Close()
		return nil, channel.ErrMediaTooLarge.New()
	}

	// 若未加密且没有超过大小上限，直接返回有界流；若携带 aeskey 则在有界读取后做 AES-256-CBC 解密
	if strings.TrimSpace(info.AESKey) == "" {
		size := resp.ContentLength
		if size <= 0 {
			size = filesystem.UnknownSize
		}
		return &channel.FetchedMedia{
			Body:        &boundedReadCloser{rc: resp.Body, remain: limit},
			Filename:    info.Filename,
			ContentType: resp.Header.Get("Content-Type"),
			Size:        size,
		}, nil
	}

	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.Wrap(err, "read encrypted wecom media")
	}
	if int64(len(data)) > limit {
		return nil, channel.ErrMediaTooLarge.New()
	}
	plain, err := decryptAES256CBC(data, info.AESKey)
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		ct = http.DetectContentType(plain)
	}
	return &channel.FetchedMedia{
		Body:        io.NopCloser(bytes.NewReader(plain)),
		Filename:    info.Filename,
		ContentType: ct,
		Size:        int64(len(plain)),
	}, nil
}

func extractFreshReplyReqID(replyCtx map[string]any) string {
	if len(replyCtx) == 0 {
		return ""
	}
	reqID, _ := replyCtx["req_id"].(string)
	reqID = strings.TrimSpace(reqID)
	if reqID == "" {
		return ""
	}
	if rawAt, _ := replyCtx["received_at"].(string); rawAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, rawAt); err == nil {
			if time.Since(ts) > ReplyWindow {
				return ""
			}
		}
	}
	return reqID
}

type boundedReadCloser struct {
	rc     io.ReadCloser
	remain int64
}

func (b *boundedReadCloser) Read(p []byte) (int, error) {
	if b.remain <= 0 {
		var one [1]byte
		n, err := b.rc.Read(one[:])
		if n > 0 {
			return 0, channel.ErrMediaTooLarge.New()
		}
		return 0, err
	}
	if int64(len(p)) > b.remain {
		p = p[:b.remain]
	}
	n, err := b.rc.Read(p)
	b.remain -= int64(n)
	return n, err
}

func (b *boundedReadCloser) Close() error { return b.rc.Close() }

func parseUUID(raw string) (uuid.UUID, error) { return uuid.Parse(raw) }
