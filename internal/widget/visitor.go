package widget

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime"

	"agents-desk/internal/blockip"
	"agents-desk/internal/channel"
	"agents-desk/internal/clientip"
	"agents-desk/internal/contact"
)

// requestKey 是请求的 context 中由 Middleware 查出的聊天插件。
type requestKey struct{}

// unknown 是聊天插件不存在时本次请求的租户：不是任何工作区的 ID，联系人的令牌在其中都无效。
const unknown = "00000000-0000-0000-0000-000000000000"

// Middleware 是 /w/{channel}/api 和 /w/{channel}/realtime 的中间件：按路径中的渠道 ID 查出客户在用的聊天插件
// （启用、工作区没有停用），放进请求的 context，供 TenantFromRequest、TenantFrom、Resolve 和接口使用。
// 查不到时放入 nil，接口返回 channel.ErrNotFound。查询出错时响应 500。
func Middleware(db bun.IDB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ch *channel.Channel
			if id, err := uuid.Parse(chi.URLParam(r, "channel")); err == nil {
				ch, err = findPublic(r.Context(), db, id.String())
				if err != nil && !errors.Is(err, channel.ErrNotFound) {
					slog.ErrorContext(r.Context(), "load widget by id", "error", err)
					httpx.WriteProblem(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
					return
				}
				if ch != nil {
					blocked, err := blockip.IsBlocked(r.Context(), db, ch.TenantID, clientip.FromContext(r.Context()))
					if err != nil {
						slog.ErrorContext(r.Context(), "check widget blocked ip", "error", err)
						httpx.WriteProblem(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
						return
					}
					if blocked {
						httpx.WriteProblem(w, http.StatusForbidden, "visitor ip is blocked")
						return
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, ch)))
		})
	}
}

// ChannelFrom 返回 Middleware 放进 ctx 的聊天插件；不存在、停用或者所在的工作区停用时返回 channel.ErrNotFound。
func ChannelFrom(ctx context.Context) (*channel.Channel, error) { return channelFrom(ctx) }

// channelFrom 返回 Middleware 放进 ctx 的聊天插件；不存在、停用或者所在的工作区停用时返回 channel.ErrNotFound。
func channelFrom(ctx context.Context) (*channel.Channel, error) {
	ch, _ := ctx.Value(requestKey{}).(*channel.Channel)
	if ch == nil {
		return nil, channel.ErrNotFound.New()
	}
	return ch, nil
}

// TenantFromRequest 是 rest.Options.Tenant：聊天插件所在的工作区。
func TenantFromRequest(ctx huma.Context) string { return TenantFrom(ctx.Context()) }

// TenantFrom 返回 Middleware 查出的聊天插件所在的工作区，查不到时返回 unknown。实时连接的 realtime.Options.Tenant 用它。
func TenantFrom(ctx context.Context) string {
	if ch, err := channelFrom(ctx); err == nil {
		return ch.TenantID
	}
	return unknown
}

// Resolve 是联系人的 Resolver：联系人要在本次请求的聊天插件中有身份（StartSession 建立的），否则令牌无效（401），
// 聊天窗口重新建立会话。聊天插件查不到时返回 channel.ErrNotFound。
// 长连接复查（identity.Rechecking）时没有 HTTP 路径参数，实时确认该联系人在当前租户下仍有启用中的聊天插件身份。
func Resolve(db bun.IDB) identity.Resolver {
	return func(ctx context.Context, sub identity.Subject) (identity.Subject, error) {
		if ch, err := channelFrom(ctx); err == nil {
			ok, err := hasIdentity(ctx, db, sub, ch.ID)
			if err != nil {
				return identity.Subject{}, err
			}
			if !ok {
				return identity.Subject{}, identity.ErrUnauthenticated.New()
			}
			return sub, nil
		}
		if !identity.Rechecking(ctx) {
			return identity.Subject{}, channel.ErrNotFound.New()
		}
		if sub.Type != contact.Type {
			return identity.Subject{}, identity.ErrUnauthenticated.New()
		}
		ok, err := db.NewSelect().TableExpr("contact_identities AS ci").
			Join("JOIN contacts AS c ON c.id = ci.contact_id AND c.tenant_id = ci.tenant_id").
			Join("JOIN channels AS ch ON ch.id = ci.channel_id AND ch.tenant_id = ci.tenant_id").
			Where("ci.tenant_id = ? AND ci.contact_id = ? AND c.blocked_at IS NULL AND ch.kind IN (?) AND ch.enabled = TRUE",
				sub.Tenant, sub.ID, bun.In([]channel.Kind{channel.Widget, channel.BusinessLink})).
			Exists(ctx)
		if err != nil {
			return identity.Subject{}, errors.Wrap(err, "recheck contact widget identity")
		}
		if !ok {
			return identity.Subject{}, identity.ErrUnauthenticated.New()
		}
		return sub, nil
	}
}

// hasIdentity 报告联系人 sub 在 sub 所在工作区的渠道 channelID 中有没有身份。
func hasIdentity(ctx context.Context, db bun.IDB, sub identity.Subject, channelID string) (bool, error) {
	if sub.Type != contact.Type {
		return false, nil
	}
	ok, err := db.NewSelect().TableExpr("contact_identities AS ci").
		Join("JOIN contacts AS c ON c.id = ci.contact_id AND c.tenant_id = ci.tenant_id").
		Where("ci.tenant_id = ? AND ci.channel_id = ? AND ci.contact_id = ? AND c.blocked_at IS NULL",
			sub.Tenant, channelID, sub.ID).Exists(ctx)
	return ok, errors.Wrap(err, "check contact identity")
}

// Channel 返回联系人 contactID 在聊天插件 channelID 中的实时频道：成员的回复经它推送给聊天窗口。
func Channel(channelID, contactID string) string { return "w:" + channelID + ":contact:" + contactID }

// RegisterRealtime 在聊天插件的实时连接 rt 上登记联系人的频道：只有在这个启用中的聊天插件中有身份的联系人自己能订阅。
func RegisterRealtime(rt *realtime.Realtime, db bun.IDB) {
	rt.Channel("w:{channel}:contact:{contact}", realtime.Private(func(ctx context.Context, sub identity.Subject, p realtime.Params) (bool, error) {
		if p["contact"] != sub.ID {
			return false, nil
		}
		if _, err := uuid.Parse(p["channel"]); err != nil {
			return false, nil
		}
		if ch, err := channelFrom(ctx); err == nil && ch.ID != p["channel"] {
			return false, nil
		}
		ch, err := findPublic(ctx, db, p["channel"])
		if errors.Is(err, channel.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if ch.TenantID != sub.Tenant {
			return false, nil
		}
		return hasIdentity(ctx, db, sub, p["channel"])
	}))
}
