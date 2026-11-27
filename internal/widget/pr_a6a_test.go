package widget

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/blockip"
	"agents-desk/internal/clientip"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
)

// TestMiddlewareIPBlockAndBlockedContactAuth 验证聊天插件入口按工作区黑名单拦截 IP，并在联系人被拉黑后拒绝新建会话和令牌复查。
func TestMiddlewareIPBlockAndBlockedContactAuth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "guard")
	beta := setup(t, env, "guard-beta")

	// 添加只属于 f（guard）工作区的 IP 黑名单，验证拦截与跨工作区放行
	if _, err := blockip.NewCreate(env.DB).Handle(ctx, f.agent, blockip.CreateBody{CIDR: "203.0.113.10", Reason: "abuser"}); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(clientip.Middleware)
	r.Route("/w/{channel}/api", func(sub chi.Router) {
		sub.Use(Middleware(env.DB))
		sub.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	reqBlocked := httptest.NewRequest(http.MethodGet, "/w/"+f.widget.ID+"/api/ping", nil)
	reqBlocked.RemoteAddr = "203.0.113.10:4231"
	recBlocked := httptest.NewRecorder()
	r.ServeHTTP(recBlocked, reqBlocked)
	if recBlocked.Code != http.StatusForbidden {
		t.Fatalf("黑名单 IP 应返回 403，实际 %d", recBlocked.Code)
	}

	reqOtherIP := httptest.NewRequest(http.MethodGet, "/w/"+f.widget.ID+"/api/ping", nil)
	reqOtherIP.RemoteAddr = "203.0.113.11:4231"
	recOtherIP := httptest.NewRecorder()
	r.ServeHTTP(recOtherIP, reqOtherIP)
	if recOtherIP.Code != http.StatusOK {
		t.Fatalf("未命中黑名单 IP 应放行 200，实际 %d", recOtherIP.Code)
	}

	reqOtherTenant := httptest.NewRequest(http.MethodGet, "/w/"+beta.widget.ID+"/api/ping", nil)
	reqOtherTenant.RemoteAddr = "203.0.113.10:4231"
	recOtherTenant := httptest.NewRecorder()
	r.ServeHTTP(recOtherTenant, reqOtherTenant)
	if recOtherTenant.Code != http.StatusOK {
		t.Fatalf("其他工作区不应受本工作区黑名单拦截，实际 %d", recOtherTenant.Code)
	}

	// 建立访客会话后拉黑该联系人：旧令牌 Resolve 失效，再次携带 VisitorID 建连返回 contact.ErrBlocked
	sess, sub := f.session(t, SessionBody{Name: "Bad Visitor"})
	if _, err := contact.NewBlock(env.DB, f.tokens).Handle(ctx, f.agent, sess.Contact.ID, contact.BlockBody{Reason: "spam"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.tokens.Validate(ctx, sess.Token); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("拉黑后会话令牌应失效，实际 %v", err)
	}
	if _, err := Resolve(env.DB)(f.in(t, f.widget.ID), sub); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("已拉黑联系人 Resolve 应返回 ErrUnauthenticated，实际 %v", err)
	}

	start := NewStartSession(env.DB, env.Crypt, f.tokens, conversation.Downstream{})
	if _, err := start.Handle(f.in(t, f.widget.ID), SessionBody{VisitorID: sess.VisitorID}, apitoken.Device{}); !errors.Is(err, contact.ErrBlocked) {
		t.Fatalf("已拉黑联系人重建会话应返回 contact.ErrBlocked，实际 %v", err)
	}
}
