package conversation

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/spf13/cobra"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// Commands 声明开发用的命令：dev message 以客户的身份往工作区的测试渠道发一条消息，
// 收件箱的前端不用等聊天插件就能看到新消息进来。只在开发和演示环境中使用。
func Commands(db *bun.DB, registry channel.Registry, down Downstream) *cobra.Command {
	group := &cobra.Command{Use: "dev", Short: "开发用的命令，只在开发和演示环境中使用"}
	message := &cobra.Command{
		Use:     "message <文字>",
		Short:   "以客户的身份往工作区的测试渠道发一条消息",
		Example: `  agents-desk dev message --tenant demo --from rahul --name "Rahul Sharma" "mera order abhi tak nahi aaya"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, _ := cmd.Flags().GetString("tenant")
			from, _ := cmd.Flags().GetString("from")
			name, _ := cmd.Flags().GetString("name")
			if name == "" {
				name = from
			}
			ctx := cmd.Context()
			var target struct {
				TenantID  string `bun:"tenant_id"`
				ChannelID string `bun:"channel_id"`
			}
			err := db.NewRaw(`SELECT t.id AS tenant_id, c.id AS channel_id FROM tenants t
				JOIN channels c ON c.tenant_id = t.id AND c.kind = ? AND c.enabled
				WHERE t.slug = ? ORDER BY c.created_at LIMIT 1`, channel.Test, slug).Scan(ctx, &target)
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Newf("工作区 %s 中没有启用的测试渠道，先运行 seed", slug)
			}
			if err != nil {
				return errors.Wrap(err, "find test channel")
			}
			sub := identity.Subject{Type: identity.System, ID: "dev", Tenant: target.TenantID}
			res, err := NewReceive(db, registry, down).Handle(ctx, sub, ReceiveParams{
				ChannelID: target.ChannelID, From: from, FromName: name, Text: strings.Join(args, " "),
			})
			if err != nil {
				return err
			}
			switch {
			case res.Created:
				fmt.Fprintf(cmd.OutOrStdout(), "已新建会话 %s（第 %d 项）\n", res.ConversationID, res.Item.Seq)
			case res.Reopened:
				fmt.Fprintf(cmd.OutOrStdout(), "已重新打开会话 %s（第 %d 项）\n", res.ConversationID, res.Item.Seq)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "已写入会话 %s 的第 %d 项\n", res.ConversationID, res.Item.Seq)
			}
			return nil
		},
	}
	message.Flags().String("tenant", "demo", "工作区的地址（slug）")
	message.Flags().String("from", "rahul", "客户在测试渠道中的编号，同一个编号是同一个客户")
	message.Flags().String("name", "", "客户的名字，新客户时使用，默认与 --from 相同")
	group.AddCommand(message)
	return group
}
