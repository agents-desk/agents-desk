package main

import (
	"context"
	"fmt"
	"io"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/platformadmin"
	"agents-desk/internal/tenant"
)

// seedPassword 是 seed 写入的所有账号的密码，只用于开发和演示。
const seedPassword = "password"

// seedCommand 返回 seed 命令：写入开发和演示用的数据，见 seed。
func seedCommand(db *bun.DB, adminLogins, memberLogins *login.Realm, addr tenant.Addresses) *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "写入开发和演示用的账号和工作区（密码都是 password），只在开发和演示环境中运行",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return seed(cmd.Context(), cmd.OutOrStdout(), db, adminLogins, memberLogins, addr)
		},
	}
}

// seed 通过业务动作写入开发和演示用的数据，已经存在的跳过，可以重复运行：
//   - 平台管理员 admin@example.com；
//   - 示例工作区 demo，主管理员 owner@example.com。
//
// 每一项写入或跳过时向 out 输出一行。
func seed(ctx context.Context, out io.Writer, db *bun.DB, adminLogins, memberLogins *login.Realm, addr tenant.Addresses) error {
	system := identity.Subject{Type: identity.System, ID: "seed"}

	// 平台管理员
	_, err := platformadmin.NewCreate(db, adminLogins).Handle(ctx, system, "admin", "admin@example.com", seedPassword)
	switch {
	case errors.Is(err, platformadmin.ErrEmailTaken):
		fmt.Fprintln(out, "平台管理员 admin@example.com 已存在，跳过")
	case err != nil:
		return err
	default:
		fmt.Fprintln(out, "已创建平台管理员 admin@example.com，密码", seedPassword)
	}

	// 示例工作区和它的主管理员
	_, err = tenant.NewCreate(db, memberLogins, addr).Handle(ctx, system, tenant.CreateParams{
		Name: "示例工作区", Slug: "demo",
		OwnerEmail: "owner@example.com", OwnerName: "主管理员", OwnerPassword: seedPassword,
	})
	switch {
	case errors.Is(err, tenant.ErrSlugTaken):
		fmt.Fprintln(out, "示例工作区 demo 已存在，跳过")
	case err != nil:
		return err
	default:
		fmt.Fprintln(out, "已创建示例工作区 demo，主管理员 owner@example.com，密码", seedPassword)
	}
	return nil
}
