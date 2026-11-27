package agent

import (
	"context"
	"database/sql"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// Channel 返回工作区 tenant 中生成 id 的频道：逐字产出、定稿和取消只送给订阅它的人。
// 它与会话频道分开，避免每个打开会话的成员都收到 token 流。
func Channel(tenant, id string) string { return "t:" + tenant + ":generation:" + id }

// RegisterRealtime 在工作区端的实时连接 rt 上登记生成的频道：能看到这个生成所在会话的成员才能订阅，
// 可见范围与会话接口同源（conversation.CanView）。
func RegisterRealtime(rt *realtime.Realtime, db bun.IDB) {
	rt.Channel("t:{tenant}:generation:{id}", realtime.Private(func(ctx context.Context, sub identity.Subject, p realtime.Params) (bool, error) {
		if sub.Type != member.Type || p["tenant"] != sub.Tenant || !sub.Can(string(auth.ConversationsRead)) {
			return false, nil
		}
		if _, err := uuid.Parse(p["id"]); err != nil {
			return false, nil
		}
		var conversationID string
		if err := db.NewSelect().Table("generations").Column("conversation_id").
			Where("id = ? AND tenant_id = ?", p["id"], sub.Tenant).Scan(ctx, &conversationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, errors.Wrap(err, "load generation for realtime")
		}
		return conversation.CanView(ctx, db, sub, conversationID)
	}))
}
