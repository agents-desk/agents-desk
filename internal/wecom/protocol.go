package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"

	"agents-desk/internal/channel"
)

// 企业微信智能机器人 WebSocket 长连接协议命令常量。
const (
	CmdSubscribe      = "aibot_subscribe"
	CmdPing           = "ping"
	CmdMsgCallback    = "aibot_msg_callback"
	CmdEventCallback  = "aibot_event_callback"
	CmdRespondMsg     = "aibot_respond_msg"
	CmdRespondWelcome = "aibot_respond_welcome_msg"
	CmdSendMsg        = "aibot_send_msg"
	CmdUploadInit     = "aibot_upload_media_init"
	CmdUploadChunk    = "aibot_upload_media_chunk"
	CmdUploadFinish   = "aibot_upload_media_finish"
)

// Frame 是企业微信智能机器人长连接收发的统一 JSON 帧。
type Frame struct {
	Cmd     string          `json:"cmd,omitempty"`
	Headers FrameHeaders    `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	ErrCode int             `json:"errcode,omitempty"`
	ErrMsg  string          `json:"errmsg,omitempty"`
}

// FrameHeaders 是长连接帧的协议头，req_id 用于请求-回执配对与被触发消息的响应关联。
type FrameHeaders struct {
	ReqID string `json:"req_id"`
}

// MsgCallbackBody 是 aibot_msg_callback 推送的消息体。
type MsgCallbackBody struct {
	MsgID    string        `json:"msgid"`
	AIBotID  string        `json:"aibotid"`
	ChatID   string        `json:"chatid"`
	ChatName string        `json:"chatname"`
	ChatType string        `json:"chattype"` // "single" 或 "group"
	From     MsgSender     `json:"from"`
	MsgType  string        `json:"msgtype"` // "text"、"image"、"voice"、"file"、"video"、"mixed"
	Text     *TextPayload  `json:"text,omitempty"`
	Image    *MediaPayload `json:"image,omitempty"`
	Voice    *VoicePayload `json:"voice,omitempty"`
	File     *MediaPayload `json:"file,omitempty"`
	Video    *MediaPayload `json:"video,omitempty"`
	Mixed    *MixedPayload `json:"mixed,omitempty"`
	Quote    *QuotePayload `json:"quote,omitempty"`
}

// MsgSender 是企微消息的发送人信息。
type MsgSender struct {
	UserID string `json:"userid"`
	Name   string `json:"name,omitempty"`
}

// TextPayload 是纯文本消息体。
type TextPayload struct {
	Content string `json:"content"`
}

// MediaPayload 是图片、文件、视频媒体消息体。
type MediaPayload struct {
	URL      string `json:"url"`
	AESKey   string `json:"aeskey,omitempty"`
	FileName string `json:"filename,omitempty"`
	FileSize int64  `json:"filesize,omitempty"`
}

// VoicePayload 是语音消息体（企微智能机器人会直接附带转写文本 Content，部分场景带音频 URL）。
type VoicePayload struct {
	Content string `json:"content,omitempty"`
	URL     string `json:"url,omitempty"`
	AESKey  string `json:"aeskey,omitempty"`
}

// MixedPayload 是图文混排消息体。
type MixedPayload struct {
	MsgItem []MixedItem `json:"msg_item"`
}

// MixedItem 是图文混排中的单项。
type MixedItem struct {
	MsgType string        `json:"msgtype"`
	Text    *TextPayload  `json:"text,omitempty"`
	Image   *MediaPayload `json:"image,omitempty"`
}

// QuotePayload 是入站消息附带的引用消息信息。
type QuotePayload struct {
	MsgID   string       `json:"msgid,omitempty"`
	MsgType string       `json:"msgtype,omitempty"`
	Text    *TextPayload `json:"text,omitempty"`
}

// EventCallbackBody 是 aibot_event_callback 推送的事件体。
type EventCallbackBody struct {
	MsgID      string       `json:"msgid"`
	CreateTime int64        `json:"create_time"`
	AIBotID    string       `json:"aibotid"`
	ChatID     string       `json:"chatid"`
	ChatType   string       `json:"chattype"`
	From       MsgSender    `json:"from"`
	Event      EventPayload `json:"event"`
}

// EventPayload 描述事件类型，例如 enter_chat、disconnected_event。
type EventPayload struct {
	EventType string `json:"eventtype"`
}

// mediaSourceRef 序列化后保存在 timeline_attachments.source_ref 中，供 MediaFetcher 异步下载和解密。
type mediaSourceRef struct {
	URL      string `json:"url"`
	AESKey   string `json:"aeskey,omitempty"`
	Filename string `json:"filename,omitempty"`
}

func encodeMediaSourceRef(ref mediaSourceRef) string {
	raw, _ := json.Marshal(ref)
	return string(raw)
}

func decodeMediaSourceRef(raw string) (mediaSourceRef, error) {
	var ref mediaSourceRef
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		return mediaSourceRef{}, errors.Wrap(err, "decode wecom media source ref")
	}
	if strings.TrimSpace(ref.URL) == "" {
		return mediaSourceRef{}, channel.ErrMediaUnavailable.New()
	}
	return ref, nil
}

// formatGroupMentions 为企微群回复前置拼接 <@userid> 提及标记（正文中已包含时跳过重复拼接）。
func formatGroupMentions(text string, mentions []channel.ExternalMention) string {
	if len(mentions) == 0 {
		return text
	}
	var prefix []string
	for _, m := range mentions {
		uid := strings.TrimSpace(m.ExternalID)
		if uid == "" {
			continue
		}
		token := fmt.Sprintf("<@%s>", uid)
		if strings.Contains(text, token) {
			continue
		}
		prefix = append(prefix, token)
	}
	if len(prefix) == 0 {
		return text
	}
	if strings.TrimSpace(text) == "" {
		return strings.Join(prefix, " ")
	}
	return strings.Join(prefix, " ") + " " + text
}

// decryptAES256CBC 使用企业微信下发的 Base64 编码 32 字节 AESKey 对媒体密文执行 AES-256-CBC + PKCS#7 解密（IV 取密钥前 16 字节）。
func decryptAES256CBC(ciphertext []byte, aesKeyB64 string) ([]byte, error) {
	keyStr := strings.TrimSpace(aesKeyB64)
	if mod := len(keyStr) % 4; mod != 0 {
		keyStr += strings.Repeat("=", 4-mod)
	}
	key, err := base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(aesKeyB64), "="))
		if err != nil {
			return nil, errors.Wrap(err, "decode wecom aeskey")
		}
	}
	if len(key) != 32 {
		return nil, errors.Newf("wecom: invalid aeskey length %d", len(key))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("wecom: invalid encrypted media length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.Wrap(err, "init wecom aes cipher")
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)

	// 校验并移除 PKCS#7（兼容企微 32 字节块填充）
	pad := int(plain[len(plain)-1])
	if pad <= 0 || pad > 32 || pad > len(plain) {
		return nil, errors.New("wecom: invalid pkcs7 padding")
	}
	for i := len(plain) - pad; i < len(plain); i++ {
		if int(plain[i]) != pad {
			return nil, errors.New("wecom: corrupted pkcs7 padding")
		}
	}
	return plain[:len(plain)-pad], nil
}

// isWeComAuthError 判断企微订阅回执错误码是否属于不可恢复的凭据/权限错误。
func isWeComAuthError(errCode int) bool {
	switch errCode {
	case 40001, 40013, 40014, 41001, 42001, 48002, 60011, 84001:
		return true
	default:
		return false
	}
}
