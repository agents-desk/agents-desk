package widget

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"

	"agents-desk/internal/channel"
)

// UserTokenCommand 声明开发用的命令 dev user-token：像产品后端一样，用工作区中聊天插件的身份验证密钥签名一个用户身份，
// 在演示页或 App 中以已登录用户的身份打开聊天窗口。只在开发和演示环境中使用。
func UserTokenCommand(db *bun.DB, c *crypt.Crypt) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "user-token",
		Short:   "用聊天插件的身份验证密钥签名一个用户身份（JWT），测试已登录的用户",
		Example: `  agents-desk dev user-token --tenant demo --sub u-42 --name "Rahul Sharma" --email rahul@example.com`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()
			slug, _ := flags.GetString("tenant")
			name, _ := flags.GetString("widget")
			sub, _ := flags.GetString("sub")
			userName, _ := flags.GetString("name")
			email, _ := flags.GetString("email")
			ttl, _ := flags.GetDuration("ttl")
			ctx := cmd.Context()
			ch := new(channel.Channel)
			q := db.NewSelect().Model(ch).
				Where("channel.kind = ? AND channel.tenant_id = (SELECT id FROM tenants WHERE slug = ?)", channel.Widget, slug).
				OrderExpr("channel.created_at").Limit(1)
			if name != "" {
				q = q.Where("channel.name = ?", name)
			}
			err := q.Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Newf("工作区 %s 中没有这个聊天插件，先运行 seed", slug)
			}
			if err != nil {
				return errors.Wrap(err, "find widget")
			}
			s, err := settingsOf(ch)
			if err != nil {
				return err
			}
			secret, err := c.Decrypt(s.IdentitySecret)
			if err != nil {
				return err
			}
			claims := userClaims{
				RegisteredClaims: jwt.RegisteredClaims{Subject: sub, ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl))},
				Name:             userName, Email: email,
			}
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
			if err != nil {
				return errors.Wrap(err, "sign user token")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "聊天插件 %s（%s），%s 后过期\n", ch.Name, ch.ID, ttl)
			fmt.Fprintln(cmd.OutOrStdout(), token)
			return nil
		},
	}
	cmd.Flags().String("tenant", "demo", "工作区的地址（slug）")
	cmd.Flags().String("widget", "", "聊天插件的名称，默认是工作区中最早建的那个")
	cmd.Flags().String("sub", "", "产品中的用户编号")
	cmd.Flags().String("name", "", "用户的名字")
	cmd.Flags().String("email", "", "用户的邮箱")
	cmd.Flags().Duration("ttl", 24*time.Hour, "有效期")
	_ = cmd.MarkFlagRequired("sub")
	return cmd
}
