package widget

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"agents-desk/internal/channel"
)

// FrameAncestors 返回聊天插件 id 的聊天窗口页面的 CSP frame-ancestors 取值：允许嵌入的网站以空格分隔，没有设置时是 *，
// 所有网站都可以嵌入。聊天插件不存在、已停用、所在的工作区已停用或者 id 不是 UUID 时是 'none'，页面只能整页打开
// （App 的 WebView 就是整页打开）。
func FrameAncestors(ctx context.Context, db bun.IDB, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "'none'", nil
	}
	ch, err := findPublic(ctx, db, id)
	if errors.Is(err, channel.ErrNotFound) {
		return "'none'", nil
	}
	if err != nil {
		return "", err
	}
	s, err := settingsOf(ch)
	if err != nil {
		return "", err
	}
	if len(s.AllowedOrigins) == 0 {
		return "*", nil
	}
	return strings.Join(s.AllowedOrigins, " "), nil
}
