// Package adminnotify 提供管理员端的站内通知收件箱（列表、标记已读、SSE 未读角标事件流）与发送辅助函数。
package adminnotify

import (
	"context"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/notification"
)

// Topic 返回收件人 r 的未读角标广播主题（仅含字母、数字、冒号、连字符与下划线）。
func Topic(r notification.Recipient) string {
	tenant := r.Tenant
	if tenant == "" {
		tenant = "platform"
	}
	return "inbox:" + tenant + ":" + r.Ref.String()
}

// Send 在业务事务 tx 中写入站内通知，并在提交后通过 hub 广播提醒收件人的 SSE 连接刷新未读数。
func Send(ctx context.Context, tx database.Tx, hub *broadcast.Hub, n notification.Notification, recipients ...notification.Recipient) ([]notification.Notification, error) {
	sent, err := notification.Send(ctx, tx, n, recipients...)
	if err != nil {
		return nil, err
	}
	if hub != nil {
		for _, item := range sent {
			topic := Topic(item.Recipient())
			id := item.ID
			tx.AfterCommit(func(ctx context.Context) error {
				return errors.Wrap(hub.PublishJSON(ctx, topic, id), "broadcast admin notification")
			})
		}
	}
	return sent, nil
}

// Item 是一条返回给前端 NotificationsMenu 的通知，字段与 @go-tenon/ui 的 NotificationItem 一致。
type Item struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
	Href      string    `json:"href,omitempty"`
}

// ListInput 是通知列表的查询参数。
type ListInput struct {
	Before string `query:"before" doc:"上一页最后一条通知的 ID，用于加载更早的通知"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" doc:"返回条数，默认 50"`
}

// ListOutput 是通知列表响应。
type ListOutput struct {
	Body struct {
		Items []Item `json:"items" nullable:"false"`
	}
}

// ReadInput 是批量标记已读的请求体。
type ReadInput struct {
	Body struct {
		IDs []uuid.UUID `json:"ids" minItems:"1" maxItems:"100"`
	}
}

// Badge 是 SSE 未读数事件的数据载荷。
type Badge struct {
	Unread int `json:"unread"`
}

// Handlers 提供管理员端站内通知的 HTTP 与 SSE 处理器。
type Handlers struct {
	db  *bun.DB
	hub *broadcast.Hub
}

// NewHandlers 创建站内通知处理器。
func NewHandlers(db *bun.DB, hub *broadcast.Hub) *Handlers {
	return &Handlers{db: db, hub: hub}
}

// List 处理 GET /notifications：按时间从新到旧返回当前登录管理员的通知。
func (h *Handlers) List(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	var before uuid.UUID
	if in.Before != "" {
		id, err := uuid.Parse(in.Before)
		if err != nil {
			return nil, errors.Wrap(err, "parse notification cursor")
		}
		before = id
	}
	rows, err := notification.List(ctx, h.db, notification.Query{
		Recipient: notification.RecipientOf(sub),
		Before:    before,
		Limit:     in.Limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, toItem(ctx, r))
	}
	out := &ListOutput{}
	out.Body.Items = items
	return out, nil
}

// Read 处理 POST /notifications/read：将指定通知标记为已读，并通知所有打开的标签页更新角标。
func (h *Handlers) Read(ctx context.Context, in *ReadInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	me := notification.RecipientOf(sub)
	if err := notification.MarkRead(ctx, h.db, me, in.Body.IDs...); err != nil {
		return nil, err
	}
	if h.hub != nil {
		_ = h.hub.Publish(ctx, Topic(me), nil)
	}
	return nil, nil
}

// ReadAll 处理 POST /notifications/read-all：将当前管理员的全部通知标记为已读并更新角标。
func (h *Handlers) ReadAll(ctx context.Context, _ *struct{}) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	me := notification.RecipientOf(sub)
	if err := notification.MarkAllRead(ctx, h.db, me); err != nil {
		return nil, err
	}
	if h.hub != nil {
		_ = h.hub.Publish(ctx, Topic(me), nil)
	}
	return nil, nil
}

// Inbox 处理 GET /notifications/inbox：建立 SSE 连接，推送当前未读数并在收到广播时实时更新。
func (h *Handlers) Inbox(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
	sub, _ := identity.FromContext(ctx)
	me := notification.RecipientOf(sub)
	return rest.SSE(func(s *httpx.Stream) error {
		if h.hub == nil {
			n, err := notification.UnreadCount(s.Context(), h.db, me)
			if err != nil {
				return err
			}
			_ = s.SendJSON(httpx.Event{Name: "badge"}, Badge{Unread: n})
			<-s.Done()
			return nil
		}
		events := h.hub.Subscribe(Topic(me)) // 先订阅再查询，避免漏掉期间的新通知
		defer events.Close()
		for {
			n, err := notification.UnreadCount(s.Context(), h.db, me)
			if err != nil {
				return err
			}
			if err := s.SendJSON(httpx.Event{Name: "badge"}, Badge{Unread: n}); err != nil {
				return nil
			}
			select {
			case <-s.Done():
				return nil
			case _, ok := <-events.C():
				if !ok {
					return nil
				}
			}
		}
	}), nil
}

// toItem 按当前语言把通知事件与数据翻译为展示条目。
func toItem(ctx context.Context, n notification.Notification) Item {
	titleKey := "notification." + n.Event + ".title"
	bodyKey := "notification." + n.Event + ".body"
	title := i18n.T(ctx, titleKey, n.Data)
	body := i18n.T(ctx, bodyKey, n.Data)
	if body == bodyKey {
		body = ""
	}
	href, _ := n.Data["href"].(string)
	if href != "" && !strings.HasPrefix(href, "/") {
		href = ""
	}
	return Item{
		ID:        n.ID.String(),
		Title:     title,
		Body:      body,
		Read:      n.Read(),
		CreatedAt: n.CreatedAt,
		Href:      href,
	}
}
