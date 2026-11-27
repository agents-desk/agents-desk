// Package blockip 管理工作区级别的访客 IP 与 CIDR 网段黑名单，并在网页聊天插件入口按客户端地址拦截请求。
package blockip

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
)

var (
	// ErrBlocked 是客户端 IP 位于当前工作区的黑名单中，拒绝接入网页聊天插件。
	ErrBlocked = errorx.Define(errorx.PermissionDenied, "blockip.blocked")
	// ErrInvalidCIDR 是填写的 IP 地址或 CIDR 网段格式无效。
	ErrInvalidCIDR = errorx.Define(errorx.InvalidArgument, "blockip.invalid_cidr")
	// ErrNotFound 是黑名单规则不存在或属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "blockip.not_found")
	// ErrDuplicate 是同一个 IP 地址或网段已在当前工作区黑名单中。
	ErrDuplicate = errorx.Define(errorx.Conflict, "blockip.duplicate")
)

var now = func() time.Time { return time.Now().UTC() }

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// BlockedIP 表示工作区黑名单中的一条 IP 或 CIDR 封禁规则。
type BlockedIP struct {
	bun.BaseModel `bun:"table:blocked_ips"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID  string  `bun:"tenant_id,notnull"`
	CIDR      string  `bun:"cidr,notnull"`
	Reason    string  `bun:"reason,notnull"`
	CreatedBy *string `bun:"created_by"`
}

// BlockedIPDTO 是接口返回的工作区黑名单规则。
type BlockedIPDTO struct {
	ID        string    `json:"id"`
	CIDR      string    `json:"cidr" doc:"规范化后的单 IP（/32 或 /128）或 CIDR 网段"`
	Reason    string    `json:"reason,omitempty" doc:"封禁原因说明"`
	CreatedBy *string   `json:"created_by,omitempty" doc:"添加规则的成员 ID"`
	CreatedAt time.Time `json:"created_at"`
}

// normalizeCIDR 将单 IP 或网段字符串规范化为带掩码的 CIDR 表示（例如 1.2.3.4 -> 1.2.3.4/32）。
func normalizeCIDR(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrInvalidCIDR.New(errorx.Field("cidr"))
	}
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return "", ErrInvalidCIDR.New(errorx.Field("cidr"))
		}
		return p.Masked().String(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", ErrInvalidCIDR.New(errorx.Field("cidr"))
	}
	return netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()).String(), nil
}

// IsBlocked 检查原始客户端地址 rawIP 是否命中工作区 tenantID 的任一黑名单网段。
func IsBlocked(ctx context.Context, db bun.IDB, tenantID, rawIP string) (bool, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(rawIP))
	if err != nil || db == nil || strings.TrimSpace(tenantID) == "" {
		return false, nil
	}
	var blocked bool
	if err := db.NewRaw(`SELECT EXISTS (SELECT 1 FROM blocked_ips WHERE tenant_id = ? AND cidr >>= ?::inet)`,
		tenantID, addr.Unmap().String()).Scan(ctx, &blocked); err != nil {
		return false, errors.Wrap(err, "check blocked ip")
	}
	return blocked, nil
}
