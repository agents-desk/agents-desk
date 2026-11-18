package platformadmin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"
	"golang.org/x/term"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
)

var (
	ErrEmailTaken       = errorx.Define(errorx.Conflict, "platform_admin.email_taken")
	ErrPasswordMismatch = errorx.Define(errorx.InvalidArgument, "platform_admin.password_mismatch")
)

// Create 创建平台管理员：platform_admins 中的一行、登录用的邮箱和密码，以及平台的域上的 platform-admin 角色。
// 第一个平台管理员只能这样创建，入口是命令 admin create。
type Create struct {
	db     *bun.DB
	logins *login.Realm
}

// NewCreate 返回创建平台管理员的动作，logins 是平台管理员的 login.Realm。
func NewCreate(db *bun.DB, logins *login.Realm) *Create { return &Create{db: db, logins: logins} }

// Handle 在一个事务中创建账号、写入邮箱和密码、分配 platform-admin 角色，并记录操作日志，任何一步失败时整体回滚。
// actor 是执行创建的主体，命令行中是系统。邮箱已被使用时返回 ErrEmailTaken，密码不符合要求时返回 login.ErrWeakPassword。
func (a *Create) Handle(ctx context.Context, actor identity.Subject, name, email, password string) (*Admin, error) {
	now := time.Now().UTC()
	admin := &Admin{ID: newID(), Name: name, CreatedAt: now, UpdatedAt: now}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(admin).Exec(ctx); err != nil {
			return errors.Wrap(err, "insert platform admin")
		}
		// 由运维人员在服务器上创建，邮箱直接标为已验证
		if err := a.logins.Attach(ctx, tx, admin.Ref(), login.Email(email), true); err != nil {
			if errors.Is(err, login.ErrTaken) {
				return ErrEmailTaken.New()
			}
			return err
		}
		if err := a.logins.SetPassword(ctx, tx, admin.Ref(), password); err != nil {
			return err
		}
		if err := authz.NewStore(tx, auth.Catalog).Assign(ctx, admin.Ref(), "", auth.PlatformAdminRoleID); err != nil {
			return err
		}
		return activity.Record(ctx, tx, actor, activity.Activity{
			Event: "platform_admin.created", TargetType: string(Type), TargetID: admin.ID,
		})
	})
	if err != nil {
		return nil, err
	}
	return admin, nil
}

// Command 处理 admin create --email 邮箱 [--name 名字]。密码不出现在命令行参数和 shell 历史中：
// 在终端中运行时提示输入两次；标准输入不是终端时（脚本、docker compose run -T）读取第一行。
func (a *Create) Command(cmd *cobra.Command, _ []string) error {
	email, _ := cmd.Flags().GetString("email")
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	password, err := readPassword(cmd.InOrStdin(), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	system := identity.Subject{Type: identity.System, ID: "cli"}
	admin, err := a.Handle(cmd.Context(), system, name, email, password)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "已创建平台管理员", admin.Name, admin.ID)
	return err
}

// readPassword 读取新密码：in 是终端时关闭回显，提示输入两次并比较；否则读取 in 的第一行。提示写到 prompt。
func readPassword(in io.Reader, prompt io.Writer) (string, error) {
	f, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.Wrap(err, "read password")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	read := func(label string) (string, error) {
		if _, err := fmt.Fprint(prompt, label); err != nil {
			return "", err
		}
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(prompt)
		return string(b), errors.Wrap(err, "read password")
	}
	first, err := read("密码：")
	if err != nil {
		return "", err
	}
	second, err := read("再输入一次：")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", ErrPasswordMismatch.New()
	}
	return first, nil
}
