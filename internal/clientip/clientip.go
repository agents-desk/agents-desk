// Package clientip 在 HTTP 中间件链中提取经过可信代理清洗的客户端真实 IP 并写入 context，
// 供登录审计记录与业务层按 IP 限流读取。
package clientip

import (
	"context"
	"net/http"

	"github.com/go-tenon/tenon/httpx"
)

type ctxKey struct{}

// Middleware 把 httpx.KeyByIP 解析出的客户端地址写入请求 context。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httpx.KeyByIP(r)
		if ip != "" {
			r = r.WithContext(WithIP(r.Context(), ip))
		}
		next.ServeHTTP(w, r)
	})
}

// WithIP 返回携带客户端 IP 的 context。
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKey{}, ip)
}

// FromContext 从 context 读取当前请求的客户端 IP，不在 HTTP 请求中时返回空字符串。
func FromContext(ctx context.Context) string {
	ip, _ := ctx.Value(ctxKey{}).(string)
	return ip
}
