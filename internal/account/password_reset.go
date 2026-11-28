package account

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/login"
)

// ForgotPasswordInput 是请求发送密码重置验证码的 HTTP 请求。
type ForgotPasswordInput struct {
	Body struct {
		Email string `json:"email" format:"email" maxLength:"254"`
	}
}

// ForgotPasswordDTO 是发送密码重置验证码后的请求标识。
type ForgotPasswordDTO struct {
	RequestID string `json:"request_id"`
}

// ForgotPasswordOutput 是找回密码接口的 HTTP 响应。
type ForgotPasswordOutput struct {
	Body ForgotPasswordDTO
}

// ResetPasswordInput 是凭邮箱验证码重设密码的 HTTP 请求。
type ResetPasswordInput struct {
	Body struct {
		RequestID string `json:"request_id" minLength:"1" maxLength:"64"`
		Code      string `json:"code" minLength:"4" maxLength:"16"`
		Password  string `json:"password" minLength:"8" maxLength:"128"`
	}
}

// PasswordReset 处理平台管理员与成员共用的忘记密码发送验证码与重设密码。
type PasswordReset struct {
	logins *login.Realm
	tokens *apitoken.Store
}

// NewPasswordReset 返回密码找回动作。
func NewPasswordReset(logins *login.Realm, tokens *apitoken.Store) *PasswordReset {
	return &PasswordReset{logins: logins, tokens: tokens}
}

// Forgot 发送重置密码验证码邮件；未注册的邮箱由底层返回固定格式的 request_id，不暴露账号是否存在。
func (a *PasswordReset) Forgot(ctx context.Context, email string) (ForgotPasswordDTO, error) {
	reqID, err := a.logins.SendCode(ctx, login.Reset, login.Email(email))
	if err != nil {
		if errors.Is(err, login.ErrInvalidIdentifier) {
			return ForgotPasswordDTO{}, login.ErrInvalidIdentifier.New(errorx.Field("email"))
		}
		return ForgotPasswordDTO{}, err
	}
	return ForgotPasswordDTO{RequestID: reqID}, nil
}

// Reset 校验验证码并将密码替换为 password，完成后撤销该账号全部登录令牌。
func (a *PasswordReset) Reset(ctx context.Context, requestID, code, password string) error {
	out, err := a.logins.VerifyCode(ctx, requestID, code)
	if err != nil {
		return err
	}
	who, err := a.logins.ResetPassword(ctx, out.Proof, password)
	if err != nil {
		if errors.Is(err, login.ErrWeakPassword) {
			return login.ErrWeakPassword.New(errorx.Field("password"))
		}
		return err
	}
	_, err = a.tokens.RevokeSubject(ctx, who)
	return err
}

// ForgotHTTP 处理忘记密码发送验证码 HTTP 请求。
func (a *PasswordReset) ForgotHTTP(ctx context.Context, in *ForgotPasswordInput) (*ForgotPasswordOutput, error) {
	dto, err := a.Forgot(ctx, in.Body.Email)
	if err != nil {
		return nil, err
	}
	return &ForgotPasswordOutput{Body: dto}, nil
}

// ResetHTTP 处理凭邮箱验证码重设密码 HTTP 请求。
func (a *PasswordReset) ResetHTTP(ctx context.Context, in *ResetPasswordInput) (*struct{}, error) {
	return nil, a.Reset(ctx, in.Body.RequestID, in.Body.Code, in.Body.Password)
}
