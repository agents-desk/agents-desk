package platformadmin

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// LoginInput 是登录的请求：邮箱和密码。
type LoginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" minLength:"1" maxLength:"128"`
	}
}

// SessionOutput 写入或清除平台管理员的会话 cookie。
type SessionOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
}

// Profile 是当前登录的平台管理员，以及他在平台的域上的权限：管理员端据此显示菜单和按钮。
type Profile struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Permissions []string `json:"permissions"`
}

// ProfileOutput 是当前管理员的响应。
type ProfileOutput struct {
	Body Profile
}

// Session 是平台管理员的登录、退出和当前账号。
type Session struct {
	db       *bun.DB
	logins   *login.Realm
	tokens   *apitoken.Store
	lifetime time.Duration // 浏览器保存会话 cookie 的时长，与登录 token 的最长有效期相同
}

// NewSession 返回平台管理员的会话处理器。
func NewSession(db *bun.DB, logins *login.Realm, tokens *apitoken.Store, lifetime time.Duration) *Session {
	return &Session{db: db, logins: logins, tokens: tokens, lifetime: lifetime}
}

// Login 校验邮箱和密码，签发登录 token 写进会话 cookie。密码错误返回 401，同一个邮箱错太多次返回 429，账号停用时返回 401。
func (s *Session) Login(ctx context.Context, in *LoginInput) (*SessionOutput, error) {
	out, err := s.logins.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
	if err != nil {
		return nil, err
	}
	token, err := s.tokens.Login(ctx, out.Subject, apitoken.Device{UserAgent: in.UserAgent})
	if err != nil {
		return nil, err
	}
	return &SessionOutput{SetCookie: SessionCookie.Set(token, s.lifetime)}, nil
}

// Logout 撤销这次登录的 token，并清除会话 cookie。
func (s *Session) Logout(ctx context.Context, _ *struct{}) (*SessionOutput, error) {
	sub, _ := identity.FromContext(ctx)
	if err := s.tokens.Revoke(ctx, sub, sub.Credential); err != nil {
		return nil, err
	}
	return &SessionOutput{SetCookie: SessionCookie.Clear()}, nil
}

// Me 返回当前登录的平台管理员：名字、登录邮箱和权限。
func (s *Session) Me(ctx context.Context, _ *struct{}) (*ProfileOutput, error) {
	sub, _ := identity.FromContext(ctx)
	a, err := find(ctx, s.db, sub.ID)
	if err != nil {
		return nil, err
	}
	ids, err := s.logins.Identifiers(ctx, a.Ref())
	if err != nil {
		return nil, err
	}
	var email string
	if i := slices.IndexFunc(ids, func(id login.Attached) bool { return id.Kind == login.KindEmail }); i >= 0 {
		email = ids[i].Value
	}
	return &ProfileOutput{Body: Profile{ID: a.ID, Name: a.Name, Email: email, Permissions: sub.Permissions}}, nil
}
