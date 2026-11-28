package member

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/page"
)

// AccountStatus 是平台级成员账号的启用状态。
type AccountStatus string

const (
	AccountActive   AccountStatus = "active"   // 正常使用
	AccountDisabled AccountStatus = "disabled" // 已全局停用
)

// AccountStatuses 是全部成员账号状态枚举，显示文字在 member_account.status.<状态>。
var AccountStatuses = enum.New("member_account.status", enum.Item(AccountActive), enum.Item(AccountDisabled))

// Schema 让 OpenAPI 中的 AccountStatus 只接受目录取值。
func (AccountStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, AccountStatuses)
}

// AdminMemberDTO 是管理员端展示的全局成员账号信息。
type AdminMemberDTO struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Email       string        `json:"email"`
	TenantCount int           `json:"tenant_count" doc:"所属工作区数量"`
	Status      AccountStatus `json:"status"`
	DisabledAt  *time.Time    `json:"disabled_at,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
}

// AdminIDInput 是管理员端指定单个成员的路径参数。
type AdminIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// AdminMemberOutput 是管理员端返回单个成员的响应。
type AdminMemberOutput struct {
	Body AdminMemberDTO
}

// AdminListInput 是管理员端全局成员列表的查询条件。
type AdminListInput struct {
	page.OffsetParams
	Q      string        `query:"q" maxLength:"60" doc:"按姓名或邮箱模糊搜索"`
	Status AccountStatus `query:"status" doc:"按启用状态筛选"`
}

// AdminListOutput 是一页管理员端全局成员列表。
type AdminListOutput struct {
	Body page.Offset[AdminMemberDTO]
}

// AdminList 列出全平台的成员账号。
type AdminList struct {
	db     *bun.DB
	logins *login.Realm
}

// NewAdminList 返回管理员端全局成员列表动作。
func NewAdminList(db *bun.DB, logins *login.Realm) *AdminList {
	return &AdminList{db: db, logins: logins}
}

// Handle 按关键词与账号状态分页返回全平台成员，统计每位成员所属的工作区数量。
func (a *AdminList) Handle(ctx context.Context, in *AdminListInput) (*page.Offset[AdminMemberDTO], error) {
	if in == nil {
		in = &AdminListInput{}
	}
	var rows []Member
	q := a.db.NewSelect().Model(&rows).
		OrderExpr("created_at DESC, id DESC").
		Limit(in.Limit()).Offset(in.Offset())
	if in.Q != "" {
		like := "%" + database.EscapeLike(in.Q) + "%"
		q = q.Where(`(name ILIKE ? ESCAPE '\' OR EXISTS (
			SELECT 1 FROM auth_identifiers ai
			WHERE ai.subject_type = ? AND ai.kind = ? AND ai.subject_id = member.id::text AND ai.value ILIKE ? ESCAPE '\'
		))`, like, Type, login.KindEmail, like)
	}
	switch in.Status {
	case AccountActive:
		q = q.Where("disabled_at IS NULL")
	case AccountDisabled:
		q = q.Where("disabled_at IS NOT NULL")
	}
	total, err := q.ScanAndCount(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list global members")
	}
	items, err := toAdminDTOs(ctx, a.db, a.logins, rows)
	if err != nil {
		return nil, err
	}
	p := page.NewOffset(items, in.OffsetParams, int64(total))
	return &p, nil
}

// HTTP 处理 GET /admin/api/members。
func (a *AdminList) HTTP(ctx context.Context, in *AdminListInput) (*AdminListOutput, error) {
	res, err := a.Handle(ctx, in)
	if err != nil {
		return nil, err
	}
	return &AdminListOutput{Body: *res}, nil
}

// AdminGet 读取管理员端单个成员详情。
type AdminGet struct {
	db     *bun.DB
	logins *login.Realm
}

// NewAdminGet 返回管理员端读取单个成员的动作。
func NewAdminGet(db *bun.DB, logins *login.Realm) *AdminGet {
	return &AdminGet{db: db, logins: logins}
}

// Handle 读取全局成员 id 的详情。
func (a *AdminGet) Handle(ctx context.Context, id string) (*AdminMemberDTO, error) {
	m, err := Find(ctx, a.db, id)
	if err != nil {
		return nil, err
	}
	items, err := toAdminDTOs(ctx, a.db, a.logins, []Member{*m})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

// HTTP 处理 GET /admin/api/members/{id}。
func (a *AdminGet) HTTP(ctx context.Context, in *AdminIDInput) (*AdminMemberOutput, error) {
	dto, err := a.Handle(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return &AdminMemberOutput{Body: *dto}, nil
}

// SetAccountDisabled 全局停用或启用成员账号，停用时清除全部登录令牌并通知长连接复查。
type SetAccountDisabled struct {
	db       *bun.DB
	logins   *login.Realm
	creds    *broadcast.Credentials
	disabled bool
}

// NewAdminDisable 返回全局停用成员账号的动作。
func NewAdminDisable(db *bun.DB, logins *login.Realm, creds ...*broadcast.Credentials) *SetAccountDisabled {
	var c *broadcast.Credentials
	if len(creds) > 0 {
		c = creds[0]
	}
	return &SetAccountDisabled{db: db, logins: logins, creds: c, disabled: true}
}

// NewAdminEnable 返回全局启用成员账号的动作。
func NewAdminEnable(db *bun.DB, logins *login.Realm) *SetAccountDisabled {
	return &SetAccountDisabled{db: db, logins: logins, disabled: false}
}

// Handle 在事务中停用或启用全局成员 id，并记录平台域操作日志。
func (a *SetAccountDisabled) Handle(ctx context.Context, subject identity.Subject, id string) (*Member, error) {
	var updated *Member
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		m := new(Member)
		err := tx.NewSelect().Model(m).Where("id = ?", id).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "lock member for status update")
		}
		if (m.DisabledAt != nil) == a.disabled {
			updated = m
			return nil
		}
		at := time.Now().UTC()
		m.DisabledAt = nil
		m.UpdatedAt = at
		evName := "member.enabled"
		if a.disabled {
			m.DisabledAt = &at
			evName = "member.disabled"
		}
		if _, err := tx.NewUpdate().Model(m).Column("disabled_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update member disabled status")
		}
		if a.disabled {
			if err := apitoken.Erase(ctx, tx, m.Ref()); err != nil {
				return err
			}
			if a.creds != nil {
				ref := m.Ref()
				tx.AfterCommit(func(ctx context.Context) error {
					if err := a.creds.RecheckSubject(ctx, ref); err != nil {
						slog.WarnContext(ctx, "recheck disabled member failed", "member_id", ref.ID, "error", err)
					}
					return nil
				})
			}
		}
		updated = m
		return activity.Record(ctx, tx, subject, activity.Activity{
			Event:      evName,
			TargetType: string(Type),
			TargetID:   m.ID,
			Properties: map[string]any{"name": m.Name},
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// HTTP 处理 POST /admin/api/members/{id}/disable 与 POST /admin/api/members/{id}/enable。
func (a *SetAccountDisabled) HTTP(ctx context.Context, in *AdminIDInput) (*AdminMemberOutput, error) {
	sub, _ := identity.FromContext(ctx)
	m, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	items, err := toAdminDTOs(ctx, a.db, a.logins, []Member{*m})
	if err != nil {
		return nil, err
	}
	return &AdminMemberOutput{Body: items[0]}, nil
}

// toAdminDTOs 批量补充成员邮箱与所属工作区计数，构造管理员端成员 DTO 列表。
func toAdminDTOs(ctx context.Context, db bun.IDB, logins *login.Realm, rows []Member) ([]AdminMemberDTO, error) {
	out := make([]AdminMemberDTO, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	emails, err := Emails(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	// 当通过测试或特殊配置缺少邮箱记录时回退使用 login.Realm 查询单个账号
	if logins != nil {
		for _, id := range ids {
			if emails[id] != "" {
				continue
			}
			if addr, err := Email(ctx, logins, identity.Ref{Type: Type, ID: id}); err == nil && addr != "" {
				emails[id] = addr
			}
		}
	}
	var counts []struct {
		SubjectID string `bun:"subject_id"`
		Cnt       int    `bun:"cnt"`
	}
	if err := db.NewSelect().Table("authz_assignments").
		Column("subject_id").
		ColumnExpr("COUNT(DISTINCT tenant) AS cnt").
		Where("subject_type = ? AND tenant <> '' AND subject_id IN (?)", Type, bun.In(ids)).
		GroupExpr("subject_id").
		Scan(ctx, &counts); err != nil {
		return nil, errors.Wrap(err, "count member tenants")
	}
	tenantCounts := make(map[string]int, len(counts))
	for _, c := range counts {
		tenantCounts[c.SubjectID] = c.Cnt
	}
	for i := range rows {
		m := &rows[i]
		status := AccountActive
		if m.DisabledAt != nil {
			status = AccountDisabled
		}
		out[i] = AdminMemberDTO{
			ID:          m.ID,
			Name:        m.Name,
			Email:       emails[m.ID],
			TenantCount: tenantCounts[m.ID],
			Status:      status,
			DisabledAt:  m.DisabledAt,
			CreatedAt:   m.CreatedAt,
		}
	}
	return out, nil
}

// RegisterAdminRoutes 在管理员端 API（/admin/api）注册全局成员管理接口与资源声明。
func RegisterAdminRoutes(api *rest.API, requireManage func(*huma.Operation), db *bun.DB, logins *login.Realm, creds ...*broadcast.Credentials) {
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		requireManage(&op)
		return op
	}
	rest.Register(api, route("admin-member-list", http.MethodGet, "/members", "全局成员列表"), NewAdminList(db, logins).HTTP)
	rest.Register(api, route("admin-member-get", http.MethodGet, "/members/{id}", "读取全局成员"), NewAdminGet(db, logins).HTTP)
	rest.Register(api, route("admin-member-disable", http.MethodPost, "/members/{id}/disable", "停用成员账号"), NewAdminDisable(db, logins, creds...).HTTP)
	rest.Register(api, route("admin-member-enable", http.MethodPost, "/members/{id}/enable", "启用成员账号"), NewAdminEnable(db, logins).HTTP)

	api.Resource(rest.Resource{
		Name:    "member",
		List:    "admin-member-list",
		Get:     "admin-member-get",
		Columns: []string{"name", "email", "tenant_count", "status", "created_at"},
		Filters: []string{"q", "status"},
		Actions: []rest.Action{
			{Name: "disable", Operation: "admin-member-disable", Confirm: true, Danger: true, When: map[string][]string{"status": {string(AccountActive)}}},
			{Name: "enable", Operation: "admin-member-enable", Confirm: true, When: map[string][]string{"status": {string(AccountDisabled)}}},
		},
	})
}
