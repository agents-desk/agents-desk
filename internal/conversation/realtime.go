package conversation

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
)

// Channel 返回工作区 tenant 中会话 id 的频道：打开着这个会话的成员加入它，彼此看到谁正在看；成员正在输入时由页面直接
// 发布 {type: "typing"}，客户正在输入时由服务端发布 Typing。频道名带上工作区，与成员的个人频道一样。
func Channel(tenant, id string) string { return "t:" + tenant + ":conversation:" + id }

// Typing 是会话频道中“正在输入”的事件。成员发布的事件由实时连接带上发布者，客户的由服务端发布，带上联系人。
type Typing struct {
	Type  string `json:"type"`  // 总是 typing
	Actor Actor  `json:"actor"` // 正在输入的人
}

// viewer 是会话频道中其他成员看到的在线成员的资料。
type viewer struct {
	Name string `json:"name"`
}

// RegisterRealtime 在工作区端的实时连接 rt 上登记会话的频道：能看到这个会话的成员可以加入（在线成员彼此可见），
// 并发布“正在输入”。看得到会话的规则与接口相同（visible）；每 30 秒复查凭据时重新授权，失去可见范围的成员被移出。
func RegisterRealtime(rt *realtime.Realtime, db bun.IDB) {
	rt.Channel("t:{tenant}:conversation:{id}", realtime.Presence(func(ctx context.Context, sub identity.Subject, p realtime.Params) (any, bool, error) {
		if sub.Type != member.Type || p["tenant"] != sub.Tenant || !sub.Can(string(auth.ConversationsRead)) {
			return nil, false, nil
		}
		if _, err := uuid.Parse(p["id"]); err != nil {
			return nil, false, nil
		}
		if _, err := findVisible(ctx, db, sub, p["id"], false); errors.Is(err, ErrNotFound) {
			return nil, false, nil
		} else if err != nil {
			return nil, false, err
		}
		m, err := member.Find(ctx, db, sub.ID)
		if err != nil {
			return nil, false, err
		}
		return viewer{Name: m.Name}, true, nil
	}).ClientPublish())
}
