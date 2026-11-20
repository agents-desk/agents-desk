package main

import (
	"context"
	"fmt"
	"io"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/platformadmin"
	"agents-desk/internal/team"
	"agents-desk/internal/tenant"
)

// seedPassword 是 seed 写入的所有账号的密码，只用于开发和演示。
const seedPassword = "password"

// seedCommand 返回 seed 命令：写入开发和演示用的数据，见 seed。
func seedCommand(db *bun.DB, adminLogins, memberLogins *login.Realm, files *filesystem.Service, addr tenant.Addresses) *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "写入开发和演示用的账号和工作区（密码都是 password），只在开发和演示环境中运行",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return seed(cmd.Context(), cmd.OutOrStdout(), db, adminLogins, memberLogins, files, addr)
		},
	}
}

// seed 通过业务动作写入开发和演示用的数据，已经存在的跳过，可以重复运行：
//   - 平台管理员 admin@example.com；
//   - 示例工作区 demo，主管理员 owner@example.com；
//   - demo 中的管理员 manager@example.com、组长 lead@example.com、坐席 agent@example.com；
//   - demo 中的团队“一线客服”，组长 lead@，成员 agent@。
//
// 每一项写入或跳过时向 out 输出一行。
func seed(ctx context.Context, out io.Writer, db *bun.DB, adminLogins, memberLogins *login.Realm, files *filesystem.Service, addr tenant.Addresses) error {
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

	// 示例工作区的其他成员：管理员、组长和坐席，以系统的身份在 demo 中添加
	var demo string
	if err := db.NewRaw("SELECT id FROM tenants WHERE slug = 'demo'").Scan(ctx, &demo); err != nil {
		return errors.Wrap(err, "find demo tenant")
	}
	add := member.NewAdd(db, memberLogins, files)
	inDemo := identity.Subject{Type: identity.System, ID: "seed", Tenant: demo}
	for _, m := range []struct {
		email, name string
		role        auth.AssignableRole
	}{
		{"manager@example.com", "管理员", auth.AssignableRole(auth.RoleAdmin)},
		{"lead@example.com", "组长", auth.AssignableRole(auth.RoleLead)},
		{"agent@example.com", "坐席", auth.AssignableRole(auth.RoleAgent)},
	} {
		_, err := add.Handle(ctx, inDemo, member.AddParams{Email: m.email, Name: m.name, Password: seedPassword, Role: m.role})
		switch {
		case errors.Is(err, member.ErrAlreadyMember):
			fmt.Fprintln(out, "成员", m.email, "已在示例工作区中，跳过")
		case err != nil:
			return err
		default:
			fmt.Fprintln(out, "已在示例工作区中添加", m.name, m.email, "，密码", seedPassword)
		}
	}

	// 示例工作区的团队“一线客服”：组长 lead@，成员 agent@
	t, err := team.NewCreate(db).Handle(ctx, inDemo, team.Changes{Name: "一线客服", Description: "处理客户的第一次咨询"})
	switch {
	case errors.Is(err, team.ErrNameTaken):
		fmt.Fprintln(out, "团队 一线客服 已存在，跳过")
		return nil
	case err != nil:
		return err
	}
	put := team.NewPutMember(db, memberLogins, files)
	for _, m := range []struct {
		email string
		lead  bool
	}{{"lead@example.com", true}, {"agent@example.com", false}} {
		ref, err := memberLogins.Lookup(ctx, login.Email(m.email))
		if err != nil {
			return err
		}
		if _, err := put.Handle(ctx, inDemo, t.ID, ref.ID, team.MemberChange{Lead: m.lead}); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "已在示例工作区中创建团队 一线客服：组长 lead@example.com，成员 agent@example.com")
	return nil
}
