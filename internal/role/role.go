// Package role 是工作区的角色：管理员创建的一组权限，包括会话的可见范围和可以做的操作。每位成员在一个工作区中有一个角色。
// 角色保存在 authz 中，域是工作区的 ID；主管理员是代码中预置的全局角色，见 auth.Owner，不在这里管理。
package role

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/errorx"

	"agents-desk/internal/auth"
)

var (
	// ErrNotFound 是角色不存在，或者不是这个工作区自己的角色（例如主管理员、别的工作区的角色）。
	ErrNotFound = errorx.Define(errorx.NotFound, "role.not_found")
	// ErrNameTaken 是同一个工作区中已经有同名的角色。
	ErrNameTaken = errorx.Define(errorx.Conflict, "role.name_taken")
	// ErrInUse 是还有成员使用这个角色，不能删除。
	ErrInUse = errorx.Define(errorx.Conflict, "role.in_use")
)

// Params 是角色的内容：名称、说明、会话的可见范围和勾选的操作权限。
type Params struct {
	Name              string                  `json:"name" minLength:"1" maxLength:"30"`
	Description       string                  `json:"description,omitempty" maxLength:"200"`
	ConversationScope auth.ConversationScope  `json:"conversation_scope" doc:"能看到哪些会话"`
	Permissions       []auth.TenantPermission `json:"permissions,omitempty" maxItems:"20" doc:"可以做的操作，不勾选时为空"`
}

// RoleDTO 是接口中的角色，带有使用它的人数。
type RoleDTO struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	ConversationScope auth.ConversationScope  `json:"conversation_scope" doc:"能看到哪些会话"`
	Permissions       []auth.TenantPermission `json:"permissions" doc:"可以做的操作"`
	MemberCount       int                     `json:"member_count" doc:"使用这个角色的人数"`
}

// RoleOutput 是返回一个角色的响应。
type RoleOutput struct {
	Body RoleDTO
}

// IDInput 是指定一个角色的请求：路径中的角色 ID。
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}

// Defaults 是新建工作区时创建的初始角色：管理员看到全部会话、可以做全部操作；坐席看到所在团队收件箱的会话。
// 它们是普通的角色，工作区可以修改和删除。
var Defaults = []Params{
	{Name: "管理员", Description: "管理成员、团队收件箱、渠道和工作区的设置，可以看到全部会话", ConversationScope: auth.ScopeAll, Permissions: auth.TenantPermissions.Values()},
	{Name: "坐席", Description: "处理所在团队收件箱的和指派给自己的会话", ConversationScope: auth.ScopeTeam, Permissions: []auth.TenantPermission{}},
}

// CreateDefaults 在事务 tx 中为工作区 tenant 创建初始角色 Defaults，新建工作区时调用。
func CreateDefaults(ctx context.Context, tx bun.IDB, tenant string) error {
	roles := authz.NewStore(tx, auth.Catalog)
	for _, p := range Defaults {
		if _, err := roles.CreateRole(ctx, toRole(tenant, p)); err != nil {
			return err
		}
	}
	return nil
}

// Lock 在事务 tx 中锁住工作区 tenant 的角色 id，返回这个角色。角色不存在、或者不是这个工作区自己的角色时返回 ErrNotFound，
// 带上 opts，例如指向请求中的字段。分配角色和删除角色都先锁住它：删除时数到的人数之后不会再有人分配到这个角色。
// 框架的 authz 提供这种并发安全之后（开发计划 F5），改用框架的做法，不再直接查询 authz_roles。
func Lock(ctx context.Context, tx bun.IDB, tenant, id string, opts ...errorx.Option) (authz.Role, error) {
	var found string
	err := tx.NewRaw("SELECT id FROM authz_roles WHERE id = ? AND tenant = ? FOR UPDATE", id, tenant).Scan(ctx, &found)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.Role{}, ErrNotFound.New(opts...)
	}
	if err != nil {
		return authz.Role{}, errors.Wrap(err, "lock role")
	}
	return authz.NewStore(tx, auth.Catalog).Role(ctx, id, tenant)
}

// toRole 把工作区 tenant 的角色内容 p 转换为 authz 中的角色：权限是基本权限、可见范围对应的权限和勾选的操作权限。
func toRole(tenant string, p Params) authz.Role {
	return authz.Role{Tenant: tenant, Name: p.Name, Description: p.Description, Permissions: auth.RolePermissions(p.ConversationScope, p.Permissions)}
}

// toDTO 把 authz 中的角色 r 转换为接口中的角色，count 是使用它的人数。
func toDTO(r authz.Role, count int) RoleDTO {
	scope, ops := auth.SplitPermissions(r.Permissions)
	return RoleDTO{ID: r.ID, Name: r.Name, Description: r.Description, ConversationScope: scope, Permissions: ops, MemberCount: count}
}

// memberCounts 返回工作区 tenant 中每个角色的人数，按角色 ID 查找。
func memberCounts(ctx context.Context, roles *authz.Store, tenant string) (map[string]int, error) {
	members, err := roles.Members(ctx, tenant)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, m := range members {
		for _, r := range m.Roles {
			counts[r.ID]++
		}
	}
	return counts, nil
}

// nameTaken 把 authz 的同名错误转换为 ErrNameTaken，指向字段 name；其他错误原样返回。
func nameTaken(err error, name string) error {
	if errors.Is(err, authz.ErrRoleExists) {
		return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", name))
	}
	return err
}
