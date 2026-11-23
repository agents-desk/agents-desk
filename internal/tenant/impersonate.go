package tenant

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// readOnlyScopes 是代为登录开启只读排查模式时授予凭据的只读权限集合。
var readOnlyScopes = []string{
	string(auth.MembersRead),
	string(auth.TeamsRead),
	string(auth.ChannelsRead),
	string(auth.ConversationsRead),
	string(auth.ConversationsReadAll),
	string(auth.ConversationsReadTeam),
	string(auth.ContactsRead),
	string(auth.ReportsRead),
}

// ImpersonateMemberDTO 是管理员端选择代为登录目标时展示的工作区成员。
type ImpersonateMemberDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Owner bool   `json:"owner" doc:"是否是这个工作区的主管理员"`
}

// ImpersonateMembersOutput 是工作区可代为登录成员列表的响应。
type ImpersonateMembersOutput struct {
	Body []ImpersonateMemberDTO
}

// ListMembers 列出工作区中可被代为登录的未停用成员（主管理员排在最前）。
type ListMembers struct {
	db     *bun.DB
	roles  *authz.Store
	logins *login.Realm
}

// NewListMembers 返回列出工作区成员的动作。
func NewListMembers(db *bun.DB, roles *authz.Store, logins *login.Realm) *ListMembers {
	return &ListMembers{db: db, roles: roles, logins: logins}
}

// Handle 读取工作区 id 中所有未停用的成员，按主管理员优先、姓名排序。工作区不存在时返回 ErrNotFound。
func (a *ListMembers) Handle(ctx context.Context, id string) ([]ImpersonateMemberDTO, error) {
	t, err := find(ctx, a.db, id, false)
	if err != nil {
		return nil, err
	}
	assignments, err := a.roles.Members(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	memberIDs := make([]string, 0, len(assignments))
	for _, item := range assignments {
		if item.Subject.Type == member.Type {
			memberIDs = append(memberIDs, item.Subject.ID)
		}
	}
	if len(memberIDs) == 0 {
		return []ImpersonateMemberDTO{}, nil
	}
	var rows []member.Member
	if err := a.db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(memberIDs)).Where("disabled_at IS NULL").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load tenant members for impersonation")
	}
	out := make([]ImpersonateMemberDTO, 0, len(rows))
	for _, m := range rows {
		email, err := member.Email(ctx, a.logins, m.Ref())
		if err != nil {
			return nil, err
		}
		out = append(out, ImpersonateMemberDTO{
			ID:    m.ID,
			Name:  m.Name,
			Email: email,
			Owner: m.ID == t.OwnerID,
		})
	}
	slices.SortFunc(out, func(x, y ImpersonateMemberDTO) int {
		if x.Owner != y.Owner {
			if x.Owner {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.ID, y.ID))
	})
	return out, nil
}

// HTTP 处理 GET /admin/api/tenants/{id}/members。
func (a *ListMembers) HTTP(ctx context.Context, in *IDInput) (*ImpersonateMembersOutput, error) {
	list, err := a.Handle(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &ImpersonateMembersOutput{Body: list}, nil
}

// ImpersonateRequest 是管理员代为登录工作区成员的请求参数。
type ImpersonateRequest struct {
	MemberID string `json:"member_id,omitempty" format:"uuid" doc:"要代为登录的成员 ID；留空时默认代为登录该工作区的主管理员"`
	Reason   string `json:"reason" minLength:"1" maxLength:"200" doc:"代为登录原因或工单号，写入操作日志"`
	ReadOnly bool   `json:"read_only,omitempty" doc:"开启后限制为只读排查模式，只能查看数据，不能修改或回复"`
}

// ImpersonateInput 是 POST /admin/api/tenants/{id}/impersonate 的 HTTP 请求。
type ImpersonateInput struct {
	ID        string `path:"id" format:"uuid"`
	UserAgent string `header:"User-Agent"`
	Body      ImpersonateRequest
}

// ImpersonateResult 是代为登录成功后的结果：令牌、跳转路径与目标信息。
type ImpersonateResult struct {
	Token      string
	URL        string
	MemberID   string
	MemberName string
	TenantSlug string
}

// ImpersonateOutput 写入独立的代登 cookie（__Host-impersonation），并返回工作区进入地址。
type ImpersonateOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      struct {
		URL        string `json:"url" doc:"工作区进入路径，前端打开即可"`
		MemberID   string `json:"member_id"`
		MemberName string `json:"member_name"`
	}
}

// Impersonate 由平台管理员以目标工作区成员的身份发起短时、单工作区锁定的代为登录会话。
type Impersonate struct {
	db           *bun.DB
	roles        *authz.Store
	memberTokens *apitoken.Store
}

// NewImpersonate 返回管理员代为登录工作区成员的动作。
func NewImpersonate(db *bun.DB, roles *authz.Store, memberTokens *apitoken.Store) *Impersonate {
	return &Impersonate{db: db, roles: roles, memberTokens: memberTokens}
}

// Handle 校验工作区与目标成员状态，签发锁定在当前工作区的 KindImpersonation 令牌并记录平台操作日志。
// 工作区不存在返回 ErrNotFound，已停用返回 ErrDisabled；成员不是该工作区成员返回 member.ErrNotFound，账号已停用返回 member.ErrDisabled。
func (a *Impersonate) Handle(ctx context.Context, actor identity.Subject, tenantID string, req ImpersonateRequest, device apitoken.Device) (ImpersonateResult, error) {
	t, err := find(ctx, a.db, tenantID, false)
	if err != nil {
		return ImpersonateResult{}, err
	}
	if t.DisabledAt != nil {
		return ImpersonateResult{}, ErrDisabled.New()
	}
	targetID := strings.TrimSpace(req.MemberID)
	if targetID == "" {
		targetID = t.OwnerID
	}
	targetRef := identity.Ref{Type: member.Type, ID: targetID}
	_, isMember, err := a.roles.Permissions(ctx, targetRef, t.ID)
	if err != nil {
		return ImpersonateResult{}, err
	}
	if !isMember {
		return ImpersonateResult{}, member.ErrNotFound.New()
	}
	m, err := member.Find(ctx, a.db, targetID)
	if err != nil {
		return ImpersonateResult{}, err
	}
	if m.DisabledAt != nil {
		return ImpersonateResult{}, member.ErrDisabled.New()
	}
	reason := strings.TrimSpace(req.Reason)
	var scopes []string
	if req.ReadOnly {
		scopes = readOnlyScopes
	}
	var token string
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		txTokens := apitoken.NewStore(tx, apitoken.Config{Lifetime: apitoken.DefaultImpersonationLifetime})
		var issueErr error
		token, issueErr = txTokens.Impersonate(ctx, actor, targetRef, apitoken.Impersonation{
			Tenant: t.ID,
			Scopes: scopes,
			Reason: reason,
			Device: device,
		})
		if issueErr != nil {
			return issueErr
		}
		return activity.Record(ctx, tx, actor, activity.Activity{
			Event:      "tenant.impersonated",
			TargetType: "tenant",
			TargetID:   t.ID,
			Properties: map[string]any{
				"name":        t.Name,
				"member_id":   m.ID,
				"member_name": m.Name,
				"reason":      reason,
				"read_only":   req.ReadOnly,
			},
		})
	})
	if err != nil {
		return ImpersonateResult{}, err
	}
	slog.InfoContext(ctx, "platform admin impersonated tenant member",
		"admin_id", actor.ID, "tenant_id", t.ID, "tenant_slug", t.Slug, "member_id", m.ID, "read_only", req.ReadOnly)
	return ImpersonateResult{
		Token:      token,
		URL:        "/t/" + t.Slug + "/",
		MemberID:   m.ID,
		MemberName: m.Name,
		TenantSlug: t.Slug,
	}, nil
}

// HTTP 处理 POST /admin/api/tenants/{id}/impersonate。
func (a *Impersonate) HTTP(ctx context.Context, in *ImpersonateInput) (*ImpersonateOutput, error) {
	actor, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, actor, in.ID, in.Body, apitoken.Device{UserAgent: in.UserAgent})
	if err != nil {
		return nil, err
	}
	out := &ImpersonateOutput{
		SetCookie: member.SessionCookie.ImpersonationCookie().Set(res.Token, apitoken.DefaultImpersonationLifetime),
	}
	out.Body.URL = res.URL
	out.Body.MemberID = res.MemberID
	out.Body.MemberName = res.MemberName
	return out, nil
}
