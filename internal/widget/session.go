package widget

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
)

// 渠道身份的外部编号：访客是 visitor: 加访客编号，用户是 user: 加产品的用户编号。
const (
	visitorPrefix = "visitor:"
	userPrefix    = "user:"
)

// SessionBody 是聊天窗口建立会话的请求体。
type SessionBody struct {
	VisitorID string `json:"visitor_id,omitempty" maxLength:"64" doc:"上次返回的访客编号，浏览器保存它；第一次打开或编号无效时不填，返回新的编号"`
	UserToken string `json:"user_token,omitempty" maxLength:"4096" doc:"已登录的用户：产品后端用身份验证密钥签名的 JWT（HS256），sub 是用户编号，必须有 exp，可以带 name、email"`
	Name      string `json:"name,omitempty" maxLength:"100" doc:"可选：访客或商务沟通对方自报的称呼"`
	Email     string `json:"email,omitempty" maxLength:"254" doc:"可选：访客或商务沟通对方自报的邮箱"`
	Language  string `json:"language,omitempty" maxLength:"35" doc:"浏览器或 App 上报的语言（BCP 47），例如 pt-BR、hi-Latn；仅在客户尚未识别语言时作为冷启动先验"`
	TimeZone  string `json:"time_zone,omitempty" maxLength:"64" doc:"浏览器上报的 IANA 时区名，例如 Asia/Shanghai"`
	Referrer  string `json:"referrer,omitempty" maxLength:"2048" doc:"嵌入聊天窗口的产品页面地址"`
}

// SessionInput 是建立会话的 HTTP 请求。
type SessionInput struct {
	UserAgent string `header:"User-Agent"`
	Body      SessionBody
}

// SessionDTO 是建立的会话：联系人的令牌和身份。
type SessionDTO struct {
	Token     string          `json:"token" doc:"联系人的令牌：之后的请求和实时连接用 Authorization: Bearer 带上它"`
	VisitorID string          `json:"visitor_id,omitempty" doc:"访客编号，浏览器保存它，下次建立会话时带上；用户会话为空（原有访客已合并消费）"`
	Contact   contact.Summary `json:"contact" doc:"客户自己：线索或用户"`
}

// SessionOutput 是建立会话的响应。
type SessionOutput struct {
	Body SessionDTO
}

// userClaims 是产品后端签名的用户身份。
type userClaims struct {
	jwt.RegisteredClaims
	Name  string `json:"name"`
	Email string `json:"email"`
}

// StartSession 是客户打开聊天窗口时建立会话：识别客户是谁，签发联系人的令牌。
type StartSession struct {
	db     *bun.DB
	crypt  *crypt.Crypt
	tokens *apitoken.Store
	down   conversation.Downstream
}

// NewStartSession 返回建立会话的动作；crypt 解开身份验证密钥，tokens 签发联系人的令牌，down 在线索合并到用户时通知成员刷新会话。
func NewStartSession(db *bun.DB, c *crypt.Crypt, tokens *apitoken.Store, down conversation.Downstream) *StartSession {
	return &StartSession{db: db, crypt: c, tokens: tokens, down: down}
}

// Handle 为本次请求的聊天插件（Middleware 查出的）建立会话：
//   - 带了 b.UserToken：校验签名、有效期和用户编号，客户是这个用户（没有时新建），用户的名字和邮箱按其中的更新；
//     如果同时带了之前访客的 b.VisitorID，且对应本聊天插件的一位线索，就在同一个事务中把该线索合并到用户（会话、时间线和资料迁到用户名下，
//     并消费掉旧的访客身份，防止退出登录后仍以旧访客编号冒用已登录用户）；无效的 UserToken 返回 ErrInvalidUserToken；
//   - 没有带：b.VisitorID 是这个聊天插件中已有的访客时，客户是这位线索；否则新建一位线索和新的访客编号。
//
// 然后为联系人签发令牌，令牌只能在聊天插件所在的工作区中使用。聊天插件不存在或已停用时返回 channel.ErrNotFound。
func (a *StartSession) Handle(ctx context.Context, b SessionBody, device apitoken.Device) (*SessionDTO, error) {
	ch, err := channelFrom(ctx)
	if err != nil {
		return nil, err
	}
	var claims *userClaims
	if b.UserToken != "" {
		if claims, err = a.verify(ch, b.UserToken); err != nil {
			return nil, err
		}
	}
	res := SessionDTO{}
	var person *contact.Contact
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if claims != nil {
			person, err = contact.ResolveUser(ctx, tx, ch.TenantID, ch.ID, userPrefix+claims.Subject, claims.Subject,
				contact.Profile{Name: claims.Name, Email: claims.Email, Language: b.Language, TimeZone: b.TimeZone, Referrer: b.Referrer})
			if err != nil {
				return err
			}
			if b.VisitorID != "" {
				var id contact.Identity
				err := tx.NewSelect().Model(&id).Where("channel_id = ? AND external_id = ?", ch.ID, visitorPrefix+b.VisitorID).Scan(ctx)
				if err == nil && id.ContactID != person.ID {
					sys := identity.Subject{Type: identity.System, ID: "widget:" + ch.ID, Tenant: ch.TenantID}
					if err := conversation.MergeActiveBetweenContacts(ctx, tx, a.down, sys, person.ID, id.ContactID); err != nil {
						return err
					}
					var convIDs []string
					person, convIDs, err = contact.MergeLead(ctx, tx, ch.TenantID, id.ContactID, person.ID, ch.ID, visitorPrefix+b.VisitorID)
					if err != nil {
						return err
					}
					if err := conversation.NotifyUpdated(ctx, tx, a.down, ch.TenantID, convIDs); err != nil {
						return err
					}
				} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return errors.Wrap(err, "load visitor for merge")
				}
			}
			return nil
		}
		profile := contact.Profile{Name: b.Name, Email: b.Email, Language: b.Language, TimeZone: b.TimeZone, Referrer: b.Referrer}
		if b.VisitorID != "" {
			var id contact.Identity
			err := tx.NewSelect().Model(&id).Where("channel_id = ? AND external_id = ?", ch.ID, visitorPrefix+b.VisitorID).Scan(ctx)
			if err == nil {
				person, _, err = contact.ResolveIdentity(ctx, tx, ch.TenantID, ch.ID, visitorPrefix+b.VisitorID, profile)
				if err != nil {
					return err
				}
				res.VisitorID = b.VisitorID
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return errors.Wrap(err, "load visitor")
			}
		}
		res.VisitorID = newVisitorID()
		person, _, err = contact.ResolveIdentity(ctx, tx, ch.TenantID, ch.ID, visitorPrefix+res.VisitorID, profile)
		return err
	})
	if err != nil {
		return nil, err
	}
	res.Contact = person.Summary()
	res.Token, err = a.tokens.Login(ctx, identity.Subject{Type: contact.Type, ID: person.ID, Tenant: ch.TenantID}, device)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// HTTP 处理 POST /w/{channel}/api/session。
func (a *StartSession) HTTP(ctx context.Context, in *SessionInput) (*SessionOutput, error) {
	dto, err := a.Handle(ctx, in.Body, apitoken.Device{UserAgent: in.UserAgent})
	if err != nil {
		return nil, err
	}
	return &SessionOutput{Body: *dto}, nil
}

// verify 用聊天插件 ch 的身份验证密钥校验产品后端签名的用户身份 token：只接受 HS256，必须有 exp 和 sub。
func (a *StartSession) verify(ch *channel.Channel, token string) (*userClaims, error) {
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	secret, err := a.crypt.Decrypt(s.IdentitySecret)
	if err != nil {
		return nil, err
	}
	claims := new(userClaims)
	_, err = jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || claims.Subject == "" || len(claims.Subject) > 200 {
		return nil, ErrInvalidUserToken.New()
	}
	return claims, nil
}
