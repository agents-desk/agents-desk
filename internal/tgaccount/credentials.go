package tgaccount

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"
	"golang.org/x/net/proxy"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/outbound"
	"github.com/go-tenon/tenon/settings"
)

var (
	// ErrCredentialsIncomplete 表示配置 Telegram API 凭据时 API ID 与 API Hash 必须同时填写。
	ErrCredentialsIncomplete = errorx.Define(errorx.InvalidArgument, "tgaccount.credentials_incomplete")
	// ErrInvalidProxyURL 表示填写的 Telegram 出口代理地址格式无效（仅支持 socks5:// 或 socks5h://）。
	ErrInvalidProxyURL = errorx.Define(errorx.InvalidArgument, "tgaccount.invalid_proxy_url")
)

// Credentials 是工作区级 Telegram MTProto 应用凭据与独立出口代理设置（按租户隔离，避免跨工作区风控连坐）。
// 字段不带 omitempty，见 tenon/settings 文档；APIHash 与 ProxyURL 落库时自动加密保存。
type Credentials struct {
	APIID    int    `json:"api_id" minimum:"0" doc:"Telegram 应用 API ID（在 my.telegram.org 申请）；填 0 表示暂不启用个人号绑定"`
	APIHash  string `json:"api_hash" maxLength:"128" format:"password" settings:"secret" doc:"Telegram 应用 API Hash，落库时自动加密保存"`
	ProxyURL string `json:"proxy_url" maxLength:"256" format:"password" settings:"secret" doc:"可选：本工作区独立的 MTProto SOCKS5 代理地址，例如 socks5://user:pass@127.0.0.1:1080；留空直连"`
}

// CredentialsDefaults 是工作区 Telegram API 凭据的默认值。
var CredentialsDefaults = Credentials{}

// Configured 报告当前工作区是否已完整配置 Telegram MTProto 应用凭据。
func (c Credentials) Configured() bool {
	return c.APIID > 0 && strings.TrimSpace(c.APIHash) != ""
}

// NewCredentialsGroup 返回工作区级 Telegram MTProto 凭据存储组，敏感字段经 keys 透明加密存储。
func NewCredentialsGroup(db bun.IDB, keys *crypt.Crypt) *settings.Group[Credentials] {
	return settings.New(db, "tenant.telegram_account_api", CredentialsDefaults, settings.WithCrypt(keys))
}

// CredentialsBody 是读取与保存工作区 Telegram API 凭据的请求和响应体。
type CredentialsBody struct {
	Body Credentials
}

// GetCredentials 读取当前工作区的 Telegram 个人号 API 凭据设置。
type GetCredentials struct {
	group *settings.Group[Credentials]
}

// NewGetCredentials 返回读取工作区 Telegram API 凭据设置的动作。
func NewGetCredentials(group *settings.Group[Credentials]) *GetCredentials {
	return &GetCredentials{group: group}
}

// Handle 读取 sub 所在工作区的 Telegram MTProto 应用凭据。
func (a *GetCredentials) Handle(ctx context.Context, sub identity.Subject) (Credentials, error) {
	return a.group.Tenant(sub.Tenant).Get(ctx)
}

// HTTP 处理 GET /t/{slug}/api/settings/telegram-api。
func (a *GetCredentials) HTTP(ctx context.Context, _ *struct{}) (*CredentialsBody, error) {
	sub, _ := identity.FromContext(ctx)
	v, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &CredentialsBody{Body: v}, nil
}

// SaveCredentials 保存当前工作区的 Telegram 个人号 API 凭据设置，并在变更后自动重连该工作区的个人号长连接。
type SaveCredentials struct {
	db        *bun.DB
	group     *settings.Group[Credentials]
	onChanged func(tenantID string)
}

// NewSaveCredentials 返回保存工作区 Telegram API 凭据设置的动作。
func NewSaveCredentials(db *bun.DB, group *settings.Group[Credentials], onChanged func(tenantID string)) *SaveCredentials {
	return &SaveCredentials{db: db, group: group, onChanged: onChanged}
}

// Handle 校验并保存工作区 Telegram MTProto 凭据（API ID 与 API Hash 须同时填写或同时留空），有修改时写入审计日志并通知重连。
func (a *SaveCredentials) Handle(ctx context.Context, sub identity.Subject, next Credentials) (Credentials, error) {
	next.APIHash = strings.TrimSpace(next.APIHash)
	next.ProxyURL = strings.TrimSpace(next.ProxyURL)
	if (next.APIID > 0) != (next.APIHash != "") {
		field := "api_hash"
		if next.APIID <= 0 {
			field = "api_id"
		}
		return Credentials{}, ErrCredentialsIncomplete.New(errorx.Field(field))
	}
	if next.ProxyURL != "" && !validSOCKS5Proxy(next.ProxyURL) {
		return Credentials{}, ErrInvalidProxyURL.New(errorx.Field("proxy_url"))
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var changed []string
		err := a.group.WithDB(tx).Tenant(sub.Tenant).Update(ctx, func(cur *Credentials) error {
			if cur.APIID != next.APIID {
				changed = append(changed, "api_id")
			}
			if cur.APIHash != next.APIHash {
				changed = append(changed, "api_hash")
			}
			if cur.ProxyURL != next.ProxyURL {
				changed = append(changed, "proxy_url")
			}
			*cur = next
			return nil
		})
		if err != nil || len(changed) == 0 {
			return err
		}
		if a.onChanged != nil {
			tenantID := sub.Tenant
			tx.AfterCommit(func(ctx context.Context) error {
				slog.InfoContext(ctx, "tenant telegram api credentials updated, reconnecting accounts", "tenant_id", tenantID, "changed", changed)
				a.onChanged(tenantID)
				return nil
			})
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "tgaccount.credentials_updated", TargetType: "tenant", TargetID: sub.Tenant,
			Properties: map[string]any{"changed": changed},
		})
	})
	return next, err
}

// HTTP 处理 PUT /t/{slug}/api/settings/telegram-api。
func (a *SaveCredentials) HTTP(ctx context.Context, in *CredentialsBody) (*CredentialsBody, error) {
	sub, _ := identity.FromContext(ctx)
	saved, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &CredentialsBody{Body: saved}, nil
}

// ConnectionTestInput 是测试 Telegram MTProto 网络连通性的请求（可传入尚未保存的代理地址进行预检）。
type ConnectionTestInput struct {
	Body struct {
		ProxyURL string `json:"proxy_url,omitempty" maxLength:"256" doc:"可选：待测试的 SOCKS5 代理地址；留空使用本工作区已保存的代理设置"`
	}
}

// ConnectionTestResult 是测试 Telegram MTProto 网络连通性的结果。
type ConnectionTestResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// ConnectionTestOutput 是测试连通性的 HTTP 响应。
type ConnectionTestOutput struct {
	Body ConnectionTestResult
}

// TestConnection 检测当前服务器（直连或经工作区 SOCKS5 代理）到 Telegram MTProto DC 的网络连通性与延迟。
type TestConnection struct {
	group *settings.Group[Credentials]
}

// NewTestConnection 返回测试工作区 Telegram 连通性的动作。
func NewTestConnection(group *settings.Group[Credentials]) *TestConnection {
	return &TestConnection{group: group}
}

// Handle 尝试在 6 秒超时内建立到 Telegram DC2 的 TCP/SOCKS5 连接并返回延迟结果。
func (a *TestConnection) Handle(ctx context.Context, sub identity.Subject, proxyOverride string) (*ConnectionTestResult, error) {
	proxyURL := strings.TrimSpace(proxyOverride)
	if proxyURL == "" && a.group != nil {
		saved, err := a.group.Tenant(sub.Tenant).Get(ctx)
		if err != nil {
			return nil, err
		}
		proxyURL = strings.TrimSpace(saved.ProxyURL)
	}
	if proxyURL != "" && !validSOCKS5Proxy(proxyURL) {
		return nil, ErrInvalidProxyURL.New(errorx.Field("proxy_url"))
	}
	const telegramDC2 = "149.154.167.50:443"
	dialCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := DialTelegram(dialCtx, proxyURL, "tcp", telegramDC2)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		if errors.Is(err, outbound.ErrForbidden) {
			return nil, ErrInvalidProxyURL.New(errorx.Field("proxy_url"))
		}
		return &ConnectionTestResult{OK: false, LatencyMS: latency, Error: err.Error()}, nil
	}
	_ = conn.Close()
	return &ConnectionTestResult{OK: true, LatencyMS: latency}, nil
}

// HTTP 处理 POST /t/{slug}/api/settings/telegram-api/test。
func (a *TestConnection) HTTP(ctx context.Context, in *ConnectionTestInput) (*ConnectionTestOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, in.Body.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &ConnectionTestOutput{Body: *res}, nil
}

// DialTelegram 按可选 SOCKS5 代理配置经 SSRF 防护拨号器连接到目标地址，供工作区连通性测试与 MTProto 客户端复用。
func DialTelegram(ctx context.Context, proxyURL, network, addr string) (net.Conn, error) {
	base := outbound.Dialer(outbound.Options{Timeout: 10 * time.Second, AnyPort: true})
	if strings.TrimSpace(proxyURL) == "" {
		return base.DialContext(ctx, network, addr)
	}
	u, err := url.Parse(strings.TrimSpace(proxyURL))
	if err != nil {
		return nil, err
	}
	var auth *proxy.Auth
	if u.User != nil {
		pwd, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pwd}
	}
	d, err := proxy.SOCKS5("tcp", u.Host, auth, base)
	if err != nil {
		return nil, err
	}
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, network, addr)
	}
	return d.Dial(network, addr)
}

// validSOCKS5Proxy 校验 SOCKS5 代理地址格式。
func validSOCKS5Proxy(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "socks5" || scheme == "socks5h"
}
