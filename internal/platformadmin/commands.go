package platformadmin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"

	"agents-desk/internal/auth"
)

// Commands 声明平台管理员的命令：admin create 创建管理员，admin ops-token 签发仅含 ops:read 权限的只读排障令牌。
func Commands(db *bun.DB, roles *authz.Store, logins *login.Realm, tokens *apitoken.Store) *cobra.Command {
	group := &cobra.Command{Use: "admin", Short: "管理平台管理员账号"}
	create := &cobra.Command{
		Use:   "create",
		Short: "创建平台管理员，密码在终端中输入，或者从标准输入读取第一行",
		Args:  command.NoArgs,
		RunE:  NewCreate(db, roles, logins).Command,
	}
	create.Flags().String("email", "", "登录用的邮箱")
	create.Flags().String("name", "", "名字，默认是邮箱 @ 之前的部分")
	_ = create.MarkFlagRequired("email")

	var opsEmail, opsName, opsPassword string
	createOps := &cobra.Command{
		Use:     "create-ops",
		Short:   "创建仅拥有 ops:read 权限的只读排障账号（角色 ops-observer，不能调用管理后台写接口）",
		Example: "agents-desk admin create-ops --email ops-bot@helmdesk.local --password <随机密码>",
		Args:    command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			name := strings.TrimSpace(opsName)
			if name == "" {
				name, _, _ = strings.Cut(opsEmail, "@")
			}
			password := opsPassword
			if password == "" {
				var err error
				password, err = command.ReadSecret(cmd, "密码：", "再输入一次：")
				if err != nil {
					return err
				}
			}
			now := time.Now().UTC()
			admin := &Admin{ID: newID(), Name: name, CreatedAt: now, UpdatedAt: now}
			err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
				if _, err := tx.NewInsert().Model(admin).Exec(ctx); err != nil {
					return err
				}
				if err := logins.Attach(ctx, tx, admin.Ref(), login.Email(opsEmail), true); err != nil {
					return err
				}
				if err := logins.SetPassword(ctx, tx, admin.Ref(), password); err != nil {
					return err
				}
				return auth.Grant(ctx, roles.WithDB(tx), admin.Ref(), "", auth.OpsObserver)
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "已创建只读排障账号", admin.Name, admin.ID)
			return err
		},
	}
	createOps.Flags().StringVar(&opsEmail, "email", "", "登录用的邮箱")
	createOps.Flags().StringVar(&opsName, "name", "只读排障机器人", "显示名称")
	createOps.Flags().StringVar(&opsPassword, "password", "", "登录密码，留空时交互输入")
	_ = createOps.MarkFlagRequired("email")

	var email, tokenName string
	var ttl time.Duration
	opsToken := &cobra.Command{
		Use:     "ops-token",
		Short:   "为指定平台管理员或只读排障账号签发仅含 ops:read 权限的短期只读排障令牌（用于 /ops/mcp）",
		Example: "agents-desk admin ops-token --email ops-bot@helmdesk.local --ttl 24h",
		Args:    command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			var subjectID string
			err := db.NewSelect().TableExpr("auth_identifiers").
				Column("subject_id").
				Where("subject_type = ? AND tenant = '' AND kind = ? AND lower(value) = lower(?)",
					string(Type), login.KindEmail, strings.TrimSpace(email)).
				Limit(1).Scan(ctx, &subjectID)
			if err != nil {
				return fmt.Errorf("未找到邮箱为 %s 的平台账号: %w", email, err)
			}
			sub, err := Resolve(db, roles)(ctx, identity.Subject{Type: Type, ID: subjectID})
			if err != nil {
				return err
			}
			if !sub.Can(string(auth.OpsRead)) {
				return fmt.Errorf("账号 %s 缺少 %s 权限", email, auth.OpsRead)
			}
			plain, err := tokens.Create(ctx, sub, apitoken.Personal{
				Name:      tokenName,
				Scopes:    []string{string(auth.OpsRead)},
				ExpiresAt: time.Now().UTC().Add(ttl),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), plain)
			return err
		},
	}
	opsToken.Flags().StringVar(&email, "email", "", "平台账号的登录邮箱")
	opsToken.Flags().StringVar(&tokenName, "name", "ops-mcp-readonly", "令牌名称，便于在令牌列表辨认和撤销")
	opsToken.Flags().DurationVar(&ttl, "ttl", 24*time.Hour, "令牌有效期，默认 24h")
	_ = opsToken.MarkFlagRequired("email")

	group.AddCommand(create, createOps, opsToken)
	return group
}
