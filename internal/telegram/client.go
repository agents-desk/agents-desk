package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/outbound"

	"agents-desk/internal/channel"
)

const (
	defaultAPIBaseURL = "https://api.telegram.org"
	// MaxDownloadSize 是 Telegram 官方 Bot API getFile 允许下载的单文件最大体积（20 MiB）。
	MaxDownloadSize = 20 * filesystem.MiB
	// autoFetchFileThreshold 是普通文档和音频自动下载的体积阈值，超过它时保留远端引用等待点击按需拉取。
	autoFetchFileThreshold = 5 * filesystem.MiB
	// autoFetchAnimationThreshold 是动图自动下载的体积阈值，超过它时等待点击按需拉取。
	autoFetchAnimationThreshold = 2 * filesystem.MiB
)

// Client 调用 Telegram Bot API：校验 Bot Token、设置/删除 Webhook、收发消息与下载文件。
type Client struct {
	http    *http.Client
	baseURL string // 默认 https://api.telegram.org，测试时可替换为 httptest 地址
}

// NewClient 返回 Telegram Bot API 客户端；httpClient 为空时使用带 SSRF 防护的 30 秒超时客户端。
func NewClient(httpClient *http.Client, baseURL ...string) *Client {
	hc := httpClient
	if hc == nil {
		hc = outbound.New(outbound.Options{Timeout: 30 * time.Second})
	}
	base := defaultAPIBaseURL
	if len(baseURL) > 0 && strings.TrimSpace(baseURL[0]) != "" {
		base = strings.TrimRight(strings.TrimSpace(baseURL[0]), "/")
	}
	return &Client{http: hc, baseURL: base}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// callJSON 以 JSON 方式调用 Telegram Bot API 方法并解析统一响应壳。
func (c *Client) callJSON(ctx context.Context, botToken, method string, payload any) (json.RawMessage, int, string, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, "", errors.Wrap(err, "encode telegram request")
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/bot%s/%s", c.baseURL, botToken, method), body)
	if err != nil {
		return nil, 0, "", errors.Wrap(err, "build telegram request")
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, botToken)
}

// callMultipart 以 multipart/form-data 方式上传媒体文件给 Telegram Bot API。
func (c *Client) callMultipart(ctx context.Context, botToken, method string, fields map[string]string, fileField, filename string, content io.Reader) (json.RawMessage, int, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, 0, "", errors.Wrap(err, "write multipart field")
		}
	}
	part, err := mw.CreateFormFile(fileField, filename)
	if err != nil {
		return nil, 0, "", errors.Wrap(err, "create multipart file")
	}
	if _, err := io.Copy(part, content); err != nil {
		return nil, 0, "", errors.Wrap(err, "copy multipart file")
	}
	if err := mw.Close(); err != nil {
		return nil, 0, "", errors.Wrap(err, "close multipart writer")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/bot%s/%s", c.baseURL, botToken, method), &buf)
	if err != nil {
		return nil, 0, "", errors.Wrap(err, "build telegram multipart request")
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req, botToken)
}

func (c *Client) do(req *http.Request, botToken string) (json.RawMessage, int, string, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, "", errors.Wrap(redactToken(err, botToken), "call telegram api")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, resp.StatusCode, "", errors.Wrap(err, "read telegram response")
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, resp.StatusCode, string(raw), errors.Newf("telegram api http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if !out.OK {
		code := out.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return nil, code, out.Description, errors.Newf("telegram api error %d: %s", code, out.Description)
	}
	return out.Result, resp.StatusCode, "", nil
}

// BotInfo 是 Telegram getMe 返回的机器人身份信息。
type BotInfo struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// SendOptions 是发送 Telegram 消息或媒体时的可选协议参数（引用回复与格式模式）。
type SendOptions struct {
	ReplyToMessageID int64
	ParseMode        string
}

// GetMe 校验 Bot Token 并返回 Bot 的数字 ID 与用户名（不含 @）。Token 无效时返回 ErrInvalidBotToken。
func (c *Client) GetMe(ctx context.Context, botToken string) (BotInfo, error) {
	res, status, _, err := c.callJSON(ctx, botToken, "getMe", nil)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusNotFound {
			return BotInfo{}, ErrInvalidBotToken.New(errorx.Field("bot_token"))
		}
		return BotInfo{}, errors.Wrap(err, "telegram getMe")
	}
	var me BotInfo
	if err := json.Unmarshal(res, &me); err != nil {
		return BotInfo{}, errors.Wrap(err, "decode telegram getMe")
	}
	return me, nil
}

// SetWebhook 把 Bot 的 Webhook 设为 targetURL，带上 secretToken 鉴权头，订阅 message 与 my_chat_member 事件。
func (c *Client) SetWebhook(ctx context.Context, botToken, targetURL, secretToken string) error {
	_, _, desc, err := c.callJSON(ctx, botToken, "setWebhook", map[string]any{
		"url":             targetURL,
		"secret_token":    secretToken,
		"allowed_updates": []string{"message", "my_chat_member"},
	})
	if err != nil {
		return ErrWebhookFailed.New(errorx.Public("reason", firstNonEmpty(desc, err.Error())))
	}
	return nil
}

// DeleteWebhook 移除 Bot 的 Webhook（停用直连渠道时调用）。
func (c *Client) DeleteWebhook(ctx context.Context, botToken string) error {
	_, _, desc, err := c.callJSON(ctx, botToken, "deleteWebhook", map[string]any{})
	if err != nil {
		return ErrWebhookFailed.New(errorx.Public("reason", firstNonEmpty(desc, err.Error())))
	}
	return nil
}

// SendMessage 向 chatID 发送文本消息（支持可选引用回复与 HTML 格式），返回 Telegram 生成的消息编号。
func (c *Client) SendMessage(ctx context.Context, botToken, chatID, text string, opts ...SendOptions) (int64, error) {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if len(opts) > 0 {
		opt := opts[0]
		if opt.ParseMode != "" {
			payload["parse_mode"] = opt.ParseMode
		}
		if opt.ReplyToMessageID > 0 {
			payload["reply_parameters"] = map[string]any{
				"message_id":                  opt.ReplyToMessageID,
				"allow_sending_without_reply": true,
			}
		}
	}
	res, _, _, err := c.callJSON(ctx, botToken, "sendMessage", payload)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendMessage")
	}
	return decodeMessageID(res)
}

func applyMultipartSendOptions(fields map[string]string, opts []SendOptions) {
	if len(opts) == 0 {
		return
	}
	opt := opts[0]
	if opt.ParseMode != "" {
		fields["parse_mode"] = opt.ParseMode
	}
	if opt.ReplyToMessageID > 0 {
		raw, _ := json.Marshal(map[string]any{
			"message_id":                  opt.ReplyToMessageID,
			"allow_sending_without_reply": true,
		})
		fields["reply_parameters"] = string(raw)
	}
}

// SendPhoto 向 chatID 发送图片附件及可选说明文字，返回 Telegram 生成的消息编号。
func (c *Client) SendPhoto(ctx context.Context, botToken, chatID, filename string, content io.Reader, caption string, opts ...SendOptions) (int64, error) {
	fields := map[string]string{"chat_id": chatID}
	if caption != "" {
		fields["caption"] = caption
	}
	applyMultipartSendOptions(fields, opts)
	res, _, _, err := c.callMultipart(ctx, botToken, "sendPhoto", fields, "photo", fallbackFilename(filename, "photo.jpg"), content)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendPhoto")
	}
	return decodeMessageID(res)
}

// SendDocument 向 chatID 发送文件附件及可选说明文字，返回 Telegram 生成的消息编号。
func (c *Client) SendDocument(ctx context.Context, botToken, chatID, filename string, content io.Reader, caption string, opts ...SendOptions) (int64, error) {
	fields := map[string]string{"chat_id": chatID}
	if caption != "" {
		fields["caption"] = caption
	}
	applyMultipartSendOptions(fields, opts)
	res, _, _, err := c.callMultipart(ctx, botToken, "sendDocument", fields, "document", fallbackFilename(filename, "file"), content)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendDocument")
	}
	return decodeMessageID(res)
}

// SendVideo 向 chatID 发送视频附件及可选说明文字和元数据，返回 Telegram 生成的消息编号。
func (c *Client) SendVideo(ctx context.Context, botToken, chatID, filename string, content io.Reader, caption string, durationSec, width, height int, opts ...SendOptions) (int64, error) {
	fields := map[string]string{"chat_id": chatID, "supports_streaming": "true"}
	if caption != "" {
		fields["caption"] = caption
	}
	if durationSec > 0 {
		fields["duration"] = fmt.Sprintf("%d", durationSec)
	}
	if width > 0 {
		fields["width"] = fmt.Sprintf("%d", width)
	}
	if height > 0 {
		fields["height"] = fmt.Sprintf("%d", height)
	}
	applyMultipartSendOptions(fields, opts)
	res, _, _, err := c.callMultipart(ctx, botToken, "sendVideo", fields, "video", fallbackFilename(filename, "video.mp4"), content)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendVideo")
	}
	return decodeMessageID(res)
}

// SendVoice 向 chatID 发送 OGG Opus 语音消息，返回 Telegram 生成的消息编号。
func (c *Client) SendVoice(ctx context.Context, botToken, chatID, filename string, content io.Reader, caption string, durationSec int, opts ...SendOptions) (int64, error) {
	fields := map[string]string{"chat_id": chatID}
	if caption != "" {
		fields["caption"] = caption
	}
	if durationSec > 0 {
		fields["duration"] = fmt.Sprintf("%d", durationSec)
	}
	applyMultipartSendOptions(fields, opts)
	res, _, _, err := c.callMultipart(ctx, botToken, "sendVoice", fields, "voice", fallbackFilename(filename, "voice.ogg"), content)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendVoice")
	}
	return decodeMessageID(res)
}

// SendAudio 向 chatID 发送音频文件及可选说明文字，返回 Telegram 生成的消息编号。
func (c *Client) SendAudio(ctx context.Context, botToken, chatID, filename string, content io.Reader, caption string, durationSec int, opts ...SendOptions) (int64, error) {
	fields := map[string]string{"chat_id": chatID}
	if caption != "" {
		fields["caption"] = caption
	}
	if durationSec > 0 {
		fields["duration"] = fmt.Sprintf("%d", durationSec)
	}
	applyMultipartSendOptions(fields, opts)
	res, _, _, err := c.callMultipart(ctx, botToken, "sendAudio", fields, "audio", fallbackFilename(filename, "audio.mp3"), content)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendAudio")
	}
	return decodeMessageID(res)
}

// OpenFile 按 Telegram file_id 打开文件流而不把正文全部读入内存，超出 maxBytes 或 Telegram 20 MiB 上限时返回 channel.ErrMediaTooLarge。
func (c *Client) OpenFile(ctx context.Context, botToken, fileID string, maxBytes int64) (*channel.FetchedMedia, error) {
	res, code, desc, err := c.callJSON(ctx, botToken, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		lower := strings.ToLower(desc)
		if strings.Contains(lower, "too big") {
			return nil, channel.ErrMediaTooLarge.New()
		}
		if code == http.StatusBadRequest && (strings.Contains(lower, "file_id") || strings.Contains(lower, "not found")) {
			return nil, channel.ErrMediaUnavailable.New()
		}
		return nil, errors.Wrap(err, "telegram getFile")
	}
	var info struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(res, &info); err != nil {
		return nil, errors.Wrap(err, "decode telegram getFile")
	}
	if info.FilePath == "" {
		return nil, channel.ErrMediaUnavailable.New()
	}
	limit := int64(MaxDownloadSize)
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}
	if info.FileSize > limit {
		return nil, channel.ErrMediaTooLarge.New()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/file/bot%s/%s", c.baseURL, botToken, strings.TrimLeft(info.FilePath, "/")), nil)
	if err != nil {
		return nil, errors.Wrap(err, "build telegram download request")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.Wrap(redactToken(err, botToken), "download telegram file")
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, channel.ErrMediaUnavailable.New()
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, errors.Newf("telegram file download http %d", resp.StatusCode)
	}
	size := resp.ContentLength
	if size <= 0 && info.FileSize > 0 {
		size = info.FileSize
	}
	if size > limit {
		_ = resp.Body.Close()
		return nil, channel.ErrMediaTooLarge.New()
	}
	if size <= 0 {
		size = filesystem.UnknownSize
	}
	return &channel.FetchedMedia{
		Body:        &boundedReadCloser{rc: resp.Body, remain: limit},
		Filename:    path.Base(info.FilePath),
		ContentType: resp.Header.Get("Content-Type"),
		Size:        size,
	}, nil
}

// boundedReadCloser 在流式读取过程中保护内存与磁盘，实际正文超过上限时立即返回 channel.ErrMediaTooLarge。
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

func decodeMessageID(res json.RawMessage) (int64, error) {
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(res, &msg); err != nil {
		return 0, errors.Wrap(err, "decode telegram message_id")
	}
	return msg.MessageID, nil
}

func fallbackFilename(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}

// redactToken 去除网络错误（尤其是 *url.Error 的 URL 字段）中的明文 Bot Token，防止凭据落入日志或送达失败记录。
func redactToken(err error, botToken string) error {
	if err == nil || botToken == "" {
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) && strings.Contains(ue.URL, botToken) {
		cloned := *ue
		cloned.URL = strings.ReplaceAll(cloned.URL, botToken, "[REDACTED]")
		return &cloned
	}
	if strings.Contains(err.Error(), botToken) {
		return errors.New(strings.ReplaceAll(err.Error(), botToken, "[REDACTED]"))
	}
	return err
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
