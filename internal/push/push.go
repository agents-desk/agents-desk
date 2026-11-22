// Package push 是推送给工作区成员的实时事件。每个成员在每个工作区中有一个个人频道，页面订阅它，
// 收到事件后经接口重新读取对应的数据。事件只带 ID 和序号，内容仍由接口按成员的权限和语言返回。
package push

import (
	"context"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime"

	"agents-desk/internal/member"
)

// 事件的类型。
const (
	Conversation = "conversation" // 会话有变化：时间线有新内容，或者负责人、团队收件箱、状态、优先级改了
	Read         = "read"         // 成员在别的页面标记了已读
)

// Event 是推送给成员的一个事件。
type Event struct {
	Type    string `json:"type"`               // Conversation 或 Read
	ID      string `json:"id"`                 // 会话的 ID
	Seq     int64  `json:"seq,omitempty"`      // 会话时间线最新一项的序号
	ItemSeq int64  `json:"item_seq,omitempty"` // 派生内容更新的那一项的序号（例如历史消息补译），页面据此把刷新窗口覆盖到它
}

// Channel 返回成员 memberID 在工作区 tenant 中的个人频道。工作区的 ID 写在频道名中：成员账号是全局的，
// 同一个成员在不同工作区中的事件分开推送。
func Channel(tenant, memberID string) string { return "t:" + tenant + ":member:" + memberID }

// Register 在 rt 上登记成员的个人频道：只有成员自己能订阅，并且频道所在的工作区就是连接所在的工作区。
func Register(rt *realtime.Realtime) {
	rt.Channel("t:{tenant}:member:{member}", realtime.Private(func(_ context.Context, sub identity.Subject, p realtime.Params) (bool, error) {
		return sub.Type == member.Type && p["tenant"] == sub.Tenant && p["member"] == sub.ID, nil
	}))
}

// Send 把事件 ev 推送到工作区 tenant 中成员 members 的个人频道，每人一条。某一条发布失败时继续发其余的，
// 返回全部错误。推送最多送达一次：页面重新连上之后自己重新读取数据。
func Send(ctx context.Context, hub *broadcast.Hub, tenant string, members []string, ev Event) error {
	var errs []error
	for _, m := range members {
		if err := hub.PublishJSON(ctx, Channel(tenant, m), ev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
