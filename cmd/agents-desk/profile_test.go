package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/filesystem"

	"agents-desk/internal/apptest"
	"agents-desk/internal/llm"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
)

// pngBytes 返回一张 w×h 的 PNG 图片，pad 字节的填充让文件变大（PNG 解码器忽略文件末尾多出的内容）。
func pngBytes(t *testing.T, pad int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return append(buf.Bytes(), make([]byte, pad)...)
}

// upload 以 multipart/form-data 上传 data，声明的文件名是 name、类型是 contentType，返回文件 ID 和地址。
func upload(t *testing.T, h http.Handler, path, name, contentType string, data []byte, cookie *http.Cookie) (id, url string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("上传 %s：%d %s", name, rec.Code, rec.Body.String())
	}
	var f struct{ ID, URL string }
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	return f.ID, f.URL
}

// fieldOf 返回错误响应中第一个字段错误的位置，例如 body.current_password。
func fieldOf(body string) string {
	var p struct {
		Errors []struct{ Location string } `json:"errors"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Errors) == 0 {
		return ""
	}
	return p.Errors[0].Location
}

// TestProfile 在管理员端和成员两套 API 上检查个人资料：上传头像后保存、换头像时删除原来的文件、
// 拒绝不是图片的文件、太大的图片和别人的文件、去掉头像，以及修改密码。
func TestProfile(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
	}
	// 与 config/app.yaml 相同：全局的请求体上限容纳 20 MiB 的上传
	a.cfg.Filesystem.MaxUploadSize = 20 * filesystem.MiB
	a.cfg.HTTP.BodyLimit.MaxBytes = 21495808
	h := a.routes(nil)

	if _, err := platformadmin.NewCreate(env.DB, env.AdminLogins).Handle(ctx, apptest.Admin, "运维", "ops@example.com", "password"); err != nil {
		t.Fatal(err)
	}
	create := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{})
	for _, owner := range []string{"owner", "other"} {
		if _, err := create.Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: owner, Slug: owner + "-team", OwnerEmail: owner + "@example.com", OwnerName: owner, OwnerPassword: "password",
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, side := range []struct{ name, api, email string }{
		{"管理员端", "/admin/api", "ops@example.com"},
		{"成员", "/api", "owner@example.com"},
	} {
		t.Run(side.name, func(t *testing.T) {
			login := func(password string) response {
				return call(h, "POST", side.api+"/auth/login", `{"email":"`+side.email+`","password":"`+password+`"}`, nil)
			}
			session := login("password").cookie
			me := func() (name, avatar string) {
				r := call(h, "GET", side.api+"/me", "", session)
				var p struct {
					Name      string `json:"name"`
					AvatarURL string `json:"avatar_url"`
				}
				if err := json.Unmarshal([]byte(r.body), &p); err != nil || r.status != http.StatusOK {
					t.Fatalf("/me：%d %s", r.status, r.body)
				}
				return p.Name, p.AvatarURL
			}

			// 上传头像并保存，名字一起改
			first, firstURL := upload(t, h, side.api+"/files", "me.png", "image/png", pngBytes(t, 0), session)
			if r := call(h, "PUT", side.api+"/me", `{"name":"新名字","avatar_file_id":"`+first+`"}`, session); r.status != http.StatusOK {
				t.Fatalf("保存：%d %s", r.status, r.body)
			}
			if name, avatar := me(); name != "新名字" || avatar != firstURL || !strings.HasPrefix(avatar, "/storage/") {
				t.Fatalf("/me：%s %s", name, avatar)
			}
			if r := call(h, "GET", firstURL, "", nil); r.status != http.StatusOK {
				t.Fatalf("读取头像：%d", r.status)
			}

			// 换头像：原来的文件被删除
			second, secondURL := upload(t, h, side.api+"/files", "me2.png", "image/png", pngBytes(t, 0), session)
			if r := call(h, "PUT", side.api+"/me", `{"name":"新名字","avatar_file_id":"`+second+`"}`, session); r.status != http.StatusOK {
				t.Fatalf("换头像：%d %s", r.status, r.body)
			}
			var status string
			if err := env.DB.NewRaw("SELECT status FROM files WHERE id = ?", first).Scan(ctx, &status); err != nil || status != "deleted" {
				t.Fatalf("原来的头像：%q %v", status, err)
			}
			if _, avatar := me(); avatar != secondURL {
				t.Fatalf("换头像后：%s", avatar)
			}

			// 不能用作头像的文件：提示指向 avatar_file_id，头像不变
			fake, _ := upload(t, h, side.api+"/files", "fake.png", "image/png", []byte("not really a picture"), session)
			big, _ := upload(t, h, side.api+"/files", "big.png", "image/png", pngBytes(t, 2<<20), session)
			othersLogin := call(h, "POST", "/api/auth/login", `{"email":"other@example.com","password":"password"}`, nil)
			others, _ := upload(t, h, "/api/files", "other.png", "image/png", pngBytes(t, 0), othersLogin.cookie)
			for name, id := range map[string]string{"不是图片": fake, "超过 2 MB": big, "别人的文件": others} {
				r := call(h, "PUT", side.api+"/me", `{"name":"新名字","avatar_file_id":"`+id+`"}`, session)
				if r.status < 400 || r.status >= 500 || fieldOf(r.body) != "body.avatar_file_id" {
					t.Errorf("%s：%d %s", name, r.status, r.body)
				}
			}
			if _, avatar := me(); avatar != secondURL {
				t.Fatalf("被拒绝后头像变了：%s", avatar)
			}

			// 去掉头像
			if r := call(h, "PUT", side.api+"/me", `{"name":"新名字"}`, session); r.status != http.StatusOK {
				t.Fatalf("去掉头像：%d %s", r.status, r.body)
			}
			if _, avatar := me(); avatar != "" {
				t.Fatalf("去掉头像后：%s", avatar)
			}

			// 修改密码：当前密码不对、新密码太弱都指向对应的字段
			if r := call(h, "PUT", side.api+"/me/password", `{"current_password":"wrong","new_password":"new-password"}`, session); r.status != http.StatusUnprocessableEntity || r.code != "account.wrong_password" || fieldOf(r.body) != "body.current_password" {
				t.Fatalf("当前密码不对：%d %s", r.status, r.body)
			}
			if r := call(h, "PUT", side.api+"/me/password", `{"current_password":"password","new_password":"short"}`, session); r.status != http.StatusUnprocessableEntity || fieldOf(r.body) != "body.new_password" {
				t.Fatalf("新密码太弱：%d %s", r.status, r.body)
			}
			elsewhere := login("password").cookie
			if r := call(h, "PUT", side.api+"/me/password", `{"current_password":"password","new_password":"new-password"}`, session); r.status != http.StatusNoContent {
				t.Fatalf("修改密码：%d %s", r.status, r.body)
			}
			// 其他登录失效，本次登录保留；旧密码不能再登录
			if r := call(h, "GET", side.api+"/me", "", elsewhere); r.status != http.StatusUnauthorized {
				t.Errorf("其他登录：%d", r.status)
			}
			me()
			if r := login("password"); r.status != http.StatusUnauthorized {
				t.Errorf("旧密码：%d", r.status)
			}
			if r := login("new-password"); r.status != http.StatusNoContent {
				t.Errorf("新密码：%d %s", r.status, r.body)
			}
		})
	}
}
