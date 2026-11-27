package blockip

import (
	"context"
	"net/http"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// ListOutput 是工作区 IP 黑名单列表响应。
type ListOutput struct {
	Body []BlockedIPDTO
}

// List 读取工作区的全部 IP 与网段黑名单。
type List struct {
	db *bun.DB
}

// NewList 返回读取 IP 黑名单列表的动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区的全部 IP 黑名单规则，按创建时间倒序排列。
func (a *List) Handle(ctx context.Context, sub identity.Subject) ([]BlockedIPDTO, error) {
	var rows []BlockedIP
	if err := a.db.NewSelect().Model(&rows).
		Where("tenant_id = ?", sub.Tenant).
		OrderExpr("created_at DESC, id DESC").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list blocked ips")
	}
	out := make([]BlockedIPDTO, len(rows))
	for i := range rows {
		out[i] = BlockedIPDTO{
			ID:        rows[i].ID,
			CIDR:      rows[i].CIDR,
			Reason:    rows[i].Reason,
			CreatedBy: rows[i].CreatedBy,
			CreatedAt: rows[i].CreatedAt,
		}
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/blocked-ips。
func (a *List) HTTP(ctx context.Context, _ *struct{}) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: items}, nil
}

// CreateBody 是新增工作区 IP 黑名单的请求体。
type CreateBody struct {
	CIDR   string `json:"cidr" maxLength:"64" doc:"要封禁的单 IP 或 CIDR 网段（如 1.2.3.4 或 1.2.3.0/24）"`
	Reason string `json:"reason,omitempty" maxLength:"500" doc:"封禁原因说明"`
}

// CreateInput 是新增 IP 黑名单的 HTTP 请求。
type CreateInput struct {
	Body CreateBody
}

// CreateOutput 是新增 IP 黑名单的响应。
type CreateOutput struct {
	Body BlockedIPDTO
}

// Create 在工作区中添加一条 IP 或 CIDR 网段黑名单。
type Create struct {
	db *bun.DB
}

// NewCreate 返回新增 IP 黑名单的动作。
func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 在 sub 所在工作区新增一条规范化后的 IP 黑名单规则并记录审计日志。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, b CreateBody) (*BlockedIPDTO, error) {
	cidr, err := normalizeCIDR(b.CIDR)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(b.Reason)
	var createdBy *string
	if sub.ID != "" {
		id := sub.ID
		createdBy = &id
	}
	row := &BlockedIP{
		ID:        newID(),
		TenantID:  sub.Tenant,
		CIDR:      cidr,
		Reason:    reason,
		CreatedBy: createdBy,
	}
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(row).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "blocked_ips_tenant_cidr_unique") {
				return ErrDuplicate.New(errorx.Field("cidr"))
			}
			return errors.Wrap(err, "insert blocked ip")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "blockip.created", TargetType: "blocked_ip", TargetID: row.ID,
			Properties: map[string]any{"cidr": cidr, "reason": reason},
		})
	})
	if err != nil {
		return nil, err
	}
	return &BlockedIPDTO{
		ID:        row.ID,
		CIDR:      row.CIDR,
		Reason:    row.Reason,
		CreatedBy: row.CreatedBy,
		CreatedAt: row.CreatedAt,
	}, nil
}

// HTTP 处理 POST /t/{slug}/api/blocked-ips。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*CreateOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &CreateOutput{Body: *dto}, nil
}

// IDInput 是按路径 ID 删除 IP 黑名单的请求。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Delete 从当前工作区移除一条 IP 黑名单规则。
type Delete struct {
	db *bun.DB
}

// NewDelete 返回删除 IP 黑名单的动作。
func NewDelete(db *bun.DB) *Delete { return &Delete{db: db} }

// Handle 从 sub 所在工作区移除黑名单规则 id 并记录操作日志，不存在或属于其他工作区时返回 ErrNotFound。
func (a *Delete) Handle(ctx context.Context, sub identity.Subject, id string) error {
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var row BlockedIP
		res, err := tx.NewDelete().Model(&row).
			Where("id = ? AND tenant_id = ?", id, sub.Tenant).
			Returning("*").Exec(ctx)
		if err != nil {
			return errors.Wrap(err, "delete blocked ip")
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return errors.Wrap(err, "delete blocked ip")
		}
		if affected == 0 {
			return ErrNotFound.New()
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "blockip.deleted", TargetType: "blocked_ip", TargetID: id,
			Properties: map[string]any{"cidr": row.CIDR},
		})
	})
}

// HTTP 处理 DELETE /t/{slug}/api/blocked-ips/{id}。
func (a *Delete) HTTP(ctx context.Context, in *IDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}

// RegisterRoutes 在工作区 API（/t/{slug}/api）中注册 IP 黑名单的查询、添加与移除接口。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	manage := require(auth.TenantSettingsManage)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		manage(&op)
		return op
	}
	create := route("blocked-ip-create", http.MethodPost, "/blocked-ips", "添加 IP 黑名单")
	create.DefaultStatus = http.StatusCreated
	del := route("blocked-ip-delete", http.MethodDelete, "/blocked-ips/{id}", "移除 IP 黑名单")
	del.DefaultStatus = http.StatusNoContent

	rest.Register(api, route("blocked-ip-list", http.MethodGet, "/blocked-ips", "IP 黑名单列表"), NewList(db).HTTP)
	rest.Register(api, create, NewCreate(db).HTTP)
	rest.Register(api, del, NewDelete(db).HTTP)
}
