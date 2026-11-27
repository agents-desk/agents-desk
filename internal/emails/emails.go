// Package emails 提供应用嵌入的双语邮件模板解析与异步发送队列任务。
package emails

import (
	"context"

	"github.com/go-tenon/tenon/i18n"
	"github.com/go-tenon/tenon/mail"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/lang"
	"agents-desk/locales"
)

// defaultBundle 在包初始化时加载服务端消息目录，供所有异步与同步发信复用。
var defaultBundle = i18n.MustNew(lang.ZhCN, locales.FS)

// SendTopic 是异步投递事务外发邮件（成员邀请、离线访客回复提醒等）的队列主题。
var SendTopic = queue.Topic[mail.Message]("mail.send")

// WithLocale 返回携带指定目标语言的邮件渲染上下文。
func WithLocale(ctx context.Context, locale string) context.Context {
	return i18n.WithLocale(ctx, defaultBundle, locale)
}

// EnsureLocale 在 ctx 尚未绑定请求语言时应用 fallback 语言，避免覆盖浏览器的 Accept-Language。
func EnsureLocale(ctx context.Context, fallback string) context.Context {
	if i18n.Locale(ctx) != "" || fallback == "" {
		return ctx
	}
	return i18n.WithLocale(ctx, defaultBundle, fallback)
}

// NewTemplates 解析嵌入的 HTML 与纯文本邮件模板。
func NewTemplates() (*mail.Templates, error) {
	return mail.NewTemplates(Files)
}

// RegisterJobs 在队列 worker 上登记邮件发送任务。
func RegisterJobs(w *queue.Worker, mailer mail.Mailer) {
	SendTopic.Handle(w, func(ctx context.Context, msg mail.Message) error {
		return mailer.Send(ctx, msg)
	})
}
