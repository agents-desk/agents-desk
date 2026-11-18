package tenant_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
)

// newTestAPI 返回只注册了租户接口的 API，token 是固定的：admin 有全部权限，viewer 只能查看。
func newTestAPI() http.Handler {
	api := rest.New(chi.NewRouter(), rest.Options{Accounts: identity.Accounts{platformadmin.Type: nil}})
	tokens := map[string]identity.Subject{
		"admin-token":  {Type: platformadmin.Type, ID: "admin", Permissions: []string{string(auth.TenantsRead), string(auth.TenantsManage)}},
		"viewer-token": {Type: platformadmin.Type, ID: "viewer", Permissions: []string{string(auth.TenantsRead)}},
	}
	validate := func(_ context.Context, token string) (identity.Subject, error) {
		if sub, ok := tokens[token]; ok {
			return sub, nil
		}
		return identity.Subject{}, identity.ErrUnauthenticated.New()
	}
	// 这些请求在调用 Handle 之前就会被拒绝，所以 db 和 logins 传 nil
	tenant.RegisterRoutes(api, rest.RequireToken(api, validate, string(auth.TenantsRead)),
		rest.RequireToken(api, validate, string(auth.TenantsManage)), nil, nil, addr)
	return api.Handler()
}

func TestCreateRejectsBadRequests(t *testing.T) {
	t.Parallel()
	const valid = `{"name":"Acme","slug":"acme","owner_email":"alice@acme.com"}`
	tests := []struct {
		name     string
		token    string
		body     string
		status   int
		code     string
		location string
	}{
		{"没有 token", "", valid, http.StatusUnauthorized, "auth.unauthenticated", ""},
		{"只能查看", "viewer-token", valid, http.StatusForbidden, "auth.forbidden", ""},
		{"slug 有大写字母", "admin-token", `{"name":"Acme","slug":"Acme","owner_email":"alice@acme.com"}`, http.StatusUnprocessableEntity, "", "body.slug"},
		{"slug 太短", "admin-token", `{"name":"Acme","slug":"ab","owner_email":"alice@acme.com"}`, http.StatusUnprocessableEntity, "", "body.slug"},
		{"邮箱格式不对", "admin-token", `{"name":"Acme","slug":"acme","owner_email":"alice"}`, http.StatusUnprocessableEntity, "", "body.owner_email"},
	}
	h := newTestAPI()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/tenants", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			var problem struct {
				Code   string `json:"code"`
				Errors []struct {
					Location string `json:"location"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if rr.Code != tc.status || problem.Code != tc.code {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if tc.location != "" && (len(problem.Errors) == 0 || problem.Errors[0].Location != tc.location) {
				t.Fatalf("errors=%s", rr.Body.String())
			}
		})
	}
}
