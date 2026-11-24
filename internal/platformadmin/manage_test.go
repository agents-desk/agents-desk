package platformadmin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/platformadmin"
)

func TestManagePlatformAdmins(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()

	// 先创建一位初始管理员，再通过接口管理其他管理员
	first, err := platformadmin.NewCreate(env.DB, env.Roles, env.AdminLogins).Handle(ctx, system, "一号管理员", "first@example.com", "secret-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	tokens := apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour})
	tok, err := tokens.Login(ctx, identity.Subject{Type: platformadmin.Type, ID: first.ID}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}

	api := rest.New(chi.NewRouter(), rest.Options{
		Accounts:    identity.Accounts{platformadmin.Type: platformadmin.Resolve(env.DB, env.Roles)},
		Permissions: auth.Catalog,
	})
	platformadmin.RegisterManageRoutes(api, rest.RequireToken(api, tokens.Validate, string(auth.PlatformAdminsManage)), env.DB, env.Roles, env.AdminLogins)
	h := api.Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 新建第二个平台管理员
	rr := do(http.MethodPost, "/admins", `{"name":"二号管理员","email":"second@example.com","password":"secret-pass-456"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("新建管理员：status=%d body=%s", rr.Code, rr.Body.String())
	}
	var second platformadmin.AdminDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Name != "二号管理员" || second.Email != "second@example.com" || second.Status != platformadmin.Active || second.Self {
		t.Fatalf("新建结果：%+v", second)
	}

	// 列表按名称筛选
	rr = do(http.MethodGet, "/admins?name=二号", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "second@example.com") || strings.Contains(rr.Body.String(), "first@example.com") {
		t.Fatalf("列表筛选：status=%d body=%s", rr.Code, rr.Body.String())
	}

	// 修改姓名
	rr = do(http.MethodPut, "/admins/"+second.ID, `{"name":"二号改名"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "二号改名") {
		t.Fatalf("改名：status=%d body=%s", rr.Code, rr.Body.String())
	}

	// 不能停用自己
	rr = do(http.MethodPost, "/admins/"+first.ID+"/disable", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("停用自己应当返回 403：status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := platformadmin.NewDisable(env.DB, env.AdminLogins).Handle(ctx, identity.Subject{Type: platformadmin.Type, ID: first.ID}, first.ID); !errors.Is(err, platformadmin.ErrSelf) {
		t.Fatalf("停用自己错误码：%v", err)
	}

	// 停用二号管理员并撤销其令牌
	secondTok, err := tokens.Login(ctx, identity.Subject{Type: platformadmin.Type, ID: second.ID}, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	rr = do(http.MethodPost, "/admins/"+second.ID+"/disable", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"disabled"`) {
		t.Fatalf("停用：status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := tokens.Validate(ctx, secondTok); err == nil {
		t.Fatal("停用后二号管理员的令牌应当被撤销")
	}

	// 按状态筛选已停用
	rr = do(http.MethodGet, "/admins?status=disabled", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), second.ID) || strings.Contains(rr.Body.String(), first.ID) {
		t.Fatalf("状态筛选：status=%d body=%s", rr.Code, rr.Body.String())
	}

	// 重新启用
	rr = do(http.MethodPost, "/admins/"+second.ID+"/enable", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"active"`) {
		t.Fatalf("启用：status=%d body=%s", rr.Code, rr.Body.String())
	}
}
