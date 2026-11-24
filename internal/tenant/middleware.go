package tenant

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
)

// requestKey 是请求的 context 中由 Middleware 查出的租户。
type requestKey struct{}

// unknown 是 slug 不存在时本次请求的租户。它不是任何租户的 ID（ID 是 UUIDv7，不会全为 0），成员在其中没有角色，
// 所以与“不是这个工作区的成员”得到同样的 404，不透露工作区是否存在。
const unknown = "00000000-0000-0000-0000-000000000000"

// Middleware 是 /t/{slug}/api 和 /t/{slug}/realtime 的中间件：按路径中的 slug 查出租户，放进请求的 context，
// 供 FromRequest、IDFrom、Resolve 使用。
// 这是请求中唯一一处由地址换成 ID 的地方；以后有了自定义域名，在这里先按 Host 查找。查询出错时响应 500。
func Middleware(db bun.IDB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := new(Tenant)
			err := db.NewSelect().Model(t).Where("slug = ?", chi.URLParam(r, "slug")).Scan(r.Context())
			switch {
			case errors.Is(err, sql.ErrNoRows):
				t = nil
			case err != nil:
				slog.ErrorContext(r.Context(), "load tenant by slug", "error", err)
				httpx.WriteProblem(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, t)))
		})
	}
}

// FromRequest 是 rest.Options.Tenant：返回 Middleware 查出的租户的 ID，slug 不存在时返回 unknown。
func FromRequest(ctx huma.Context) string { return IDFrom(ctx.Context()) }

// IDFrom 返回 Middleware 放进 ctx 的租户的 ID，slug 不存在时返回 unknown。实时连接的 realtime.Options.Tenant 用它。
func IDFrom(ctx context.Context) string {
	if t := fromContext(ctx); t != nil {
		return t.ID
	}
	return unknown
}

// Resolve 在成员的 Resolver next 之后检查租户的状态：next 确认他是这个工作区的成员（不是成员时 404），
// 工作区已停用时返回 ErrDisabled（403）。停用对已经登录的成员立即生效。
func Resolve(next identity.Resolver) identity.Resolver {
	return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
		sub, err := next(ctx, sub)
		if err != nil {
			return identity.Subject{}, err
		}
		if t := fromContext(ctx); t != nil && t.DisabledAt != nil {
			return identity.Subject{}, ErrDisabled.New()
		}
		return sub, nil
	}
}

// CheckEnabled 返回 realtime.Options.CheckTenant：按租户 ID 实时查询数据库，确认租户存在且未停用。
// 租户不存在时返回 ErrNotFound，已停用时返回 ErrDisabled，长连接在握手与凭据复查时据此立即断开。
func CheckEnabled(db bun.IDB) func(context.Context, string) error {
	return func(ctx context.Context, tenantID string) error {
		if tenantID == "" || tenantID == unknown {
			return ErrNotFound.New()
		}
		var row struct {
			DisabledAt *time.Time `bun:"disabled_at"`
		}
		err := db.NewSelect().Table("tenants").Column("disabled_at").Where("id = ?", tenantID).Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "check tenant status")
		}
		if row.DisabledAt != nil {
			return ErrDisabled.New()
		}
		return nil
	}
}

// fromContext 返回 Middleware 放进 ctx 的租户，没有时返回 nil。
func fromContext(ctx context.Context) *Tenant {
	t, _ := ctx.Value(requestKey{}).(*Tenant)
	return t
}
