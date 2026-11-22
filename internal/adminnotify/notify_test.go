package adminnotify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/notification"

	"agents-desk/internal/adminnotify"
	"agents-desk/internal/apptest"
	"agents-desk/internal/platformadmin"
	"agents-desk/locales"
)

func TestAdminNotifications(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	admin, err := platformadmin.NewCreate(env.DB, env.AdminLogins).Handle(ctx, identity.Subject{Type: identity.System, ID: "cli"}, "运维", "ops@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	sub := identity.Subject{Type: platformadmin.Type, ID: admin.ID}
	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour})
	tok, err := tokens.Login(ctx, sub, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}

	// 发送两条通知给该管理员
	if err := database.RunInTx(ctx, env.DB, nil, func(ctx context.Context, tx database.Tx) error {
		for _, count := range []int{3, 5} {
			if _, err := adminnotify.Send(ctx, tx, nil, notification.Notification{
				Event: "tenants.exported",
				Data:  map[string]any{"processed": count, "href": "/tenants"},
			}, notification.RecipientOf(sub)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Use(httpx.Defaults(httpx.DefaultConfig(), httpx.Languages(i18n.MustNew("zh-CN", locales.FS)))...)
	api := rest.New(router, rest.Options{
		Accounts: identity.Accounts{platformadmin.Type: platformadmin.Resolve(env.DB, env.Roles)},
	})
	adminnotify.RegisterRoutes(api, rest.RequireToken(api, tokens.Validate), env.DB, nil)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	// 读取列表，包含两条未读通知和中文译文
	rr := do(http.MethodGet, "/notifications", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("列表：status=%d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Items []adminnotify.Item `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.Items[0].Read || !strings.Contains(res.Items[0].Title, "导出完成") || res.Items[0].Href != "/tenants" {
		t.Fatalf("通知列表不符合预期：%+v", res.Items)
	}

	// 标记第一条已读
	rr = do(http.MethodPost, "/notifications/read", `{"ids":["`+res.Items[0].ID+`"]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("标记已读：status=%d body=%s", rr.Code, rr.Body.String())
	}
	n, err := notification.UnreadCount(ctx, env.DB, notification.RecipientOf(sub))
	if err != nil || n != 1 {
		t.Fatalf("标记一条已读后剩余未读数应当为 1，实际 %d err=%v", n, err)
	}

	// 全部标记已读
	rr = do(http.MethodPost, "/notifications/read-all", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("全部已读：status=%d body=%s", rr.Code, rr.Body.String())
	}
	n, err = notification.UnreadCount(ctx, env.DB, notification.RecipientOf(sub))
	if err != nil || n != 0 {
		t.Fatalf("全部已读后剩余未读数应当为 0，实际 %d err=%v", n, err)
	}
}
