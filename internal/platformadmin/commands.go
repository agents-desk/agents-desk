package platformadmin

import (
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/login"
)

// Commands 声明平台管理员的命令：admin create。
func Commands(db *bun.DB, logins *login.Realm) *cobra.Command {
	group := &cobra.Command{Use: "admin", Short: "管理平台管理员账号"}
	create := &cobra.Command{
		Use:   "create",
		Short: "创建平台管理员，密码在终端中输入，或者从标准输入读取第一行",
		Args:  command.NoArgs,
		RunE:  NewCreate(db, logins).Command,
	}
	create.Flags().String("email", "", "登录用的邮箱")
	create.Flags().String("name", "", "名字，默认是邮箱 @ 之前的部分")
	_ = create.MarkFlagRequired("email")
	group.AddCommand(create)
	return group
}
