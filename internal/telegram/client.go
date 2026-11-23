package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/conversation"
)

const defaultAPIBaseURL = "https://api.telegram.org"

// Client 调用 Telegram Bot API：校验 Bot Token、设置/删除 Webhook、收发消息与下载文件。
type Client struct {
	http    *http.Client
	baseURL string // 默认 https://api.telegram.org，测试时可替换为 httptest 地址
}

// NewClient 返回 Telegram Bot API 客户端；httpClient 为空时使用 30 秒超时的默认客户端。
func NewClient(httpClient *http.Client, baseURL ...string) *Client {
	hc := httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
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
	return c.do(req)
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
	return c.do(req)
}

func (c *Client) do(req *http.Request) (json.RawMessage, int, string, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, "", errors.Wrap(err, "call telegram api")
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

// GetMe 校验 Bot Token 并返回 Bot 的用户名（不含 @）。Token 无效时返回 ErrInvalidBotToken。
func (c *Client) GetMe(ctx context.Context, botToken string) (string, error) {
	res, status, _, err := c.callJSON(ctx, botToken, "getMe", nil)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusNotFound {
			return "", ErrInvalidBotToken.New(errorx.Field("bot_token"))
		}
		return "", errors.Wrap(err, "telegram getMe")
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(res, &me); err != nil {
		return "", errors.Wrap(err, "decode telegram getMe")
	}
	return me.Username, nil
}

// SetWebhook 把 Bot 的 Webhook 设为 targetURL，带上 secretToken 鉴权头，仅订阅 message 事件。
func (c *Client) SetWebhook(ctx context.Context, botToken, targetURL, secretToken string) error {
	_, _, desc, err := c.callJSON(ctx, botToken, "setWebhook", map[string]any{
		"url":             targetURL,
		"secret_token":    secretToken,
		"allowed_updates": []string{"message"},
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

// SendMessage 向 chatID 发送纯文本消息，返回 Telegram 生成的消息编号。
func (c *Client) SendMessage(ctx context.Context, botToken, chatID, text string) (int64, error) {
	res, _, _, err := c.callJSON(ctx, botToken, "sendMessage", map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendMessage")
	}
	return decodeMessageID(res)
}

// SendPhoto 向 chatID 发送图片附件及可选说明文字，返回 Telegram 生成的消息编号。
func (c *Client) SendPhoto(ctx context.Context, botToken, chatID, filename string, content io.Reader, caption string) (int64, error) {
	fields := map[string]string{"chat_id": chatID}
	if caption != "" {
		fields["caption"] = caption
	}
	res, _, _, err := c.callMultipart(ctx, botToken, "sendPhoto", fields, "photo", fallbackFilename(filename, "photo.jpg"), content)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendPhoto")
	}
	return decodeMessageID(res)
}

// SendDocument 向 chatID 发送文件附件及可选说明文字，返回 Telegram 生成的消息编号。
func (c *Client) SendDocument(ctx context.Context, botToken, chatID, filename string, content io.Reader, caption string) (int64, error) {
	fields := map[string]string{"chat_id": chatID}
	if caption != "" {
		fields["caption"] = caption
	}
	res, _, _, err := c.callMultipart(ctx, botToken, "sendDocument", fields, "document", fallbackFilename(filename, "file"), content)
	if err != nil {
		return 0, errors.Wrap(err, "telegram sendDocument")
	}
	return decodeMessageID(res)
}

// DownloadFile 按 Telegram file_id 下载文件字节（不超过 conversation.MaxFileSize），返回字节、推断文件名和探测 MIME 类型。
func (c *Client) DownloadFile(ctx context.Context, botToken, fileID string) ([]byte, string, string, error) {
	res, _, _, err := c.callJSON(ctx, botToken, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return nil, "", "", errors.Wrap(err, "telegram getFile")
	}
	var info struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(res, &info); err != nil {
		return nil, "", "", errors.Wrap(err, "decode telegram getFile")
	}
	if info.FilePath == "" {
		return nil, "", "", errors.New("telegram getFile returned empty file_path")
	}
	maxBytes := int64(conversation.MaxFileSize)
	if info.FileSize > maxBytes {
		return nil, "", "", errors.Newf("telegram file size %d exceeds max %d", info.FileSize, maxBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/file/bot%s/%s", c.baseURL, botToken, strings.TrimLeft(info.FilePath, "/")), nil)
	if err != nil {
		return nil, "", "", errors.Wrap(err, "build telegram download request")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", "", errors.Wrap(err, "download telegram file")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", errors.Newf("telegram file download http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", "", errors.Wrap(err, "read telegram file body")
	}
	if int64(len(data)) > maxBytes {
		return nil, "", "", errors.Newf("telegram downloaded file exceeds max %d", maxBytes)
	}
	name := path.Base(info.FilePath)
	ct := http.DetectContentType(data)
	return data, name, ct, nil
}

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

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
