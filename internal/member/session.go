package member

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/account"
	"agents-desk/internal/lang"
)

// LoginInput 是登录的请求：邮箱和密码。
type LoginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email    string `json:"email" format:"email" maxLength:"254"`
		Password string `json:"password" minLength:"1" maxLength:"128"`
	}
}

// SessionOutput 写入或清除成员的会话 cookie。
type SessionOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
}

// ImpersonationInfo 描述当前会话的代为登录状态。
type ImpersonationInfo struct {
	ActorID   string `json:"actor_id" doc:"发起代为登录的平台管理员 ID"`
	ActorName string `json:"actor_name" doc:"发起代为登录的平台管理员姓名"`
	ReadOnly  bool   `json:"read_only" doc:"当前代为登录会话是否限制为只读排查模式"`
}

// Profile 是当前登录的成员。他所属的工作区和在工作区中的权限分别由 tenant-mine、tenant-current 返回。
type Profile struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Title         string               `json:"title" doc:"对外展示的头衔或职位，用于商务沟通名片"`
	Bio           string               `json:"bio" doc:"对外展示的简短介绍，用于商务沟通名片"`
	Email         string               `json:"email"`
	AvatarFileID  string               `json:"avatar_file_id,omitempty" doc:"头像的文件 ID，修改个人资料时原样传回表示不换头像"`
	AvatarURL     string               `json:"avatar_url" doc:"头像的地址；没有头像时为空"`
	Notifications NotificationSettings `json:"notifications" doc:"实时提醒设置：浏览器通知与提示音开关"`
	Impersonation *ImpersonationInfo   `json:"impersonation,omitempty" doc:"代为登录状态；本人登录时为空"`
	Preferences
}

// ProfileOutput 是当前成员的响应。
type ProfileOutput struct {
	Body Profile
}

// Session 是成员的登录、退出和当前账号。
type Session struct {
	db       *bun.DB
	logins   *login.Realm
	tokens   *apitoken.Store
	files    *filesystem.Service
	lifetime time.Duration // 浏览器保存会话 cookie 的时长，与登录 token 的最长有效期相同
}

// NewSession 返回成员的会话处理器，logins 是成员的 login.Realm，tokens 签发成员的登录 token，files 读取头像。
func NewSession(db *bun.DB, logins *login.Realm, tokens *apitoken.Store, files *filesystem.Service, lifetime time.Duration) *Session {
	return &Session{db: db, logins: logins, tokens: tokens, files: files, lifetime: lifetime}
}

// Login 校验邮箱和密码，签发登录 token 写进会话 cookie。密码错误返回 401，同一个邮箱错太多次返回 429，
// 账号停用时返回 401（ErrAccountDisabled）。登录 token 不绑定工作区，同一次登录可以进入成员所属的每个工作区。
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

// Logout 撤销这次登录的 token，并清除对应的会话 cookie；凭据撤销后由 tokens 的 Credentials 联动通知长连接复查。
// 代为登录状态下仅清除独立的代登 cookie（__Host-impersonation），不影响浏览器中已有的普通成员会话。
func (s *Session) Logout(ctx context.Context, _ *struct{}) (*SessionOutput, error) {
	sub, _ := identity.FromContext(ctx)
	if err := s.tokens.Revoke(ctx, sub, sub.Credential); err != nil {
		return nil, err
	}
	if sub.Impersonated() {
		return &SessionOutput{SetCookie: SessionCookie.ImpersonationCookie().Clear()}, nil
	}
	return &SessionOutput{SetCookie: SessionCookie.Clear()}, nil
}

// Me 返回当前登录的成员：名字、登录邮箱、头像及代为登录信息。
func (s *Session) Me(ctx context.Context, _ *struct{}) (*ProfileOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := loadProfile(ctx, s.db, s.logins, s.files, sub)
	if err != nil {
		return nil, err
	}
	return &ProfileOutput{Body: *p}, nil
}

// loadProfile 读取 sub 的个人资料：名字、登录邮箱、头像地址与代为登录状态。
func loadProfile(ctx context.Context, db bun.IDB, logins *login.Realm, files *filesystem.Service, sub identity.Subject) (*Profile, error) {
	m, err := Find(ctx, db, sub.ID)
	if err != nil {
		return nil, err
	}
	email, err := Email(ctx, logins, m.Ref())
	if err != nil {
		return nil, err
	}
	avatar, err := account.AvatarURL(ctx, files, m.AvatarFileID)
	if err != nil {
		return nil, err
	}
	shortcut := m.SendShortcut
	if shortcut == "" {
		shortcut = SendEnter
	}
	uiLang := m.UILanguage
	if uiLang == "" {
		uiLang = UILanguage(lang.MatchUI(string(m.Language)))
	}
	p := &Profile{
		ID:            m.ID,
		Name:          m.Name,
		Title:         m.Title,
		Bio:           m.Bio,
		Email:         email,
		AvatarURL:     avatar,
		Notifications: m.NotificationPreferences.Settings(),
		Preferences: Preferences{
			UILanguage:         uiLang,
			Language:           m.Language,
			TimeZone:           m.TimeZone,
			SendShortcut:       shortcut,
			OpenNextAfterReply: m.OpenNextAfterReply,
		},
	}
	if m.AvatarFileID != nil {
		p.AvatarFileID = m.AvatarFileID.String()
	}
	if sub.Impersonated() {
		var actorName string
		err := db.NewSelect().Table("platform_admins").Column("name").Where("id = ?", sub.Impersonator.ID).Scan(ctx, &actorName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, errors.Wrap(err, "load impersonator name")
		}
		p.Impersonation = &ImpersonationInfo{
			ActorID:   sub.Impersonator.ID,
			ActorName: actorName,
			ReadOnly:  sub.ReadOnly || isReadOnlyScopes(sub.Scopes),
		}
	}
	return p, nil
}

// isReadOnlyScopes 报告凭据的 Scopes 是否属于只读排查模式。
func isReadOnlyScopes(scopes []string) bool {
	if len(scopes) == 0 {
		return false
	}
	return slices.Equal(scopes, []string{"*:read"}) || !slices.ContainsFunc(scopes, func(s string) bool {
		return s == "*" || s != "*:read" && !slices.Contains([]string{
			"members:read", "teams:read", "channels:read", "conversations:read", "conversations:read-all", "conversations:read-team", "contacts:read", "reports:read",
		}, s)
	})
}
