package platformadmin

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/account"
	"agents-desk/internal/clientip"
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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	AvatarFileID string   `json:"avatar_file_id,omitempty" doc:"头像的文件 ID，修改个人资料时原样传回表示不换头像"`
	AvatarURL    string   `json:"avatar_url" doc:"头像的地址；没有头像时为空"`
	Permissions  []string `json:"permissions"`
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
	files    *filesystem.Service
	lifetime time.Duration // 浏览器保存会话 cookie 的时长，与登录 token 的最长有效期相同
}

// NewSession 返回平台管理员的会话处理器。
func NewSession(db *bun.DB, logins *login.Realm, tokens *apitoken.Store, files *filesystem.Service, lifetime time.Duration) *Session {
	return &Session{db: db, logins: logins, tokens: tokens, files: files, lifetime: lifetime}
}

// Login 校验邮箱和密码，签发登录 token 写进会话 cookie。密码错误返回 401，同一个邮箱错太多次返回 429，账号停用时返回 401。
func (s *Session) Login(ctx context.Context, in *LoginInput) (*SessionOutput, error) {
	out, err := s.logins.Password(ctx, login.Email(in.Body.Email), in.Body.Password)
	if err != nil {
		return nil, err
	}
	token, err := s.tokens.Login(ctx, out.Subject, apitoken.Device{
		IP:        clientip.FromContext(ctx),
		UserAgent: in.UserAgent,
	})
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

// Me 返回当前登录的平台管理员：名字、登录邮箱、头像和权限。
func (s *Session) Me(ctx context.Context, _ *struct{}) (*ProfileOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := loadProfile(ctx, s.db, s.logins, s.files, sub)
	if err != nil {
		return nil, err
	}
	return &ProfileOutput{Body: *p}, nil
}

// loadProfile 读取 sub 的个人资料：名字、登录邮箱、头像的地址，以及本次请求中 sub 的权限。
func loadProfile(ctx context.Context, db bun.IDB, logins *login.Realm, files *filesystem.Service, sub identity.Subject) (*Profile, error) {
	a, err := find(ctx, db, sub.ID)
	if err != nil {
		return nil, err
	}
	ids, err := logins.Identifiers(ctx, a.Ref())
	if err != nil {
		return nil, err
	}
	var email string
	if i := slices.IndexFunc(ids, func(id login.Attached) bool { return id.Kind == login.KindEmail }); i >= 0 {
		email = ids[i].Value
	}
	avatar, err := account.AvatarURL(ctx, files, a.AvatarFileID)
	if err != nil {
		return nil, err
	}
	p := &Profile{ID: a.ID, Name: a.Name, Email: email, AvatarURL: avatar, Permissions: sub.Permissions}
	if a.AvatarFileID != nil {
		p.AvatarFileID = a.AvatarFileID.String()
	}
	return p, nil
}
