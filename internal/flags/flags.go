// Package flags 声明平台的功能开关目录与工作区查询适配器，接口实现委托给 tenon/feature/featureapi。
package flags

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/feature/featureapi"
	"github.com/go-tenon/tenon/identity"
)

// 平台声明的功能开关目录：所有灰度开关集中在这里声明，发布后开关名保持固定。
var (
	// AITranslation 控制工作区会话的 AI 自动语言识别与互译能力，支持按工作区灰度或紧急关闭。
	AITranslation = feature.Flag{
		Name:        "conversation.ai_translation",
		Description: "会话 AI 自动语言识别与实时翻译",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// AutoAutomation 控制内置自动化规则引擎，支持按工作区灰度或紧急关闭。
	AutoAutomation = feature.Flag{
		Name:        "conversation.auto_automation",
		Description: "会话内置自动化规则引擎（回复时间、留资、关闭评价）",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// WidgetVisitorUploads 控制网页聊天插件与商务链接的访客文件上传开关，默认开启，支持按工作区或全员紧急关闭。
	WidgetVisitorUploads = feature.Flag{
		Name:        "widget.visitor_uploads",
		Description: "网页聊天插件访客附件上传",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// AIWorkspace 控制工作区端尚在建设中的 AI Agent 模块入口与占位视图，默认关闭。
	AIWorkspace = feature.Flag{
		Name:        "ai.workspace",
		Description: "工作区 AI 中心与助手预览入口",
		ByTenant:    true,
	}
	// BusinessLinks 控制成员个人网页商务沟通链接功能，支持按工作区灰度或关闭。
	BusinessLinks = feature.Flag{
		Name:        "business.links",
		Description: "个人网页商务沟通链接",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// BusinessTelegramAccount 控制成员 Telegram 个人号商务同步功能，支持按工作区灰度或关闭。
	BusinessTelegramAccount = feature.Flag{
		Name:        "business.telegram_account",
		Description: "成员 Telegram 个人号商务同步",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
)

// Catalog 是应用注册的全部功能开关。
var Catalog = feature.Catalog{
	AITranslation,
	AutoAutomation,
	WidgetVisitorUploads,
	AIWorkspace,
	BusinessLinks,
	BusinessTelegramAccount,
}

// NewStore 创建绑定到 db 的功能开关存储。
func NewStore(db bun.IDB) *feature.Store {
	return feature.NewStore(db, Catalog)
}

// Options 返回供 featureapi 使用的扩展配置：补充工作区名称与 slug，并记录操作日志。
func Options(db *bun.DB) featureapi.Options {
	return featureapi.Options{
		RecordActivity: true,
		LookupTenants: func(ctx context.Context, ids []string) (map[string]featureapi.TenantInfo, error) {
			out := make(map[string]featureapi.TenantInfo, len(ids))
			if len(ids) == 0 || db == nil {
				return out, nil
			}
			var rows []struct {
				ID   string `bun:"id"`
				Name string `bun:"name"`
				Slug string `bun:"slug"`
			}
			if err := db.NewSelect().Table("tenants").Column("id", "name", "slug").Where("id IN (?)", bun.List(ids)).Scan(ctx, &rows); err != nil {
				return nil, errors.Wrap(err, "load tenant info for feature flags")
			}
			for _, r := range rows {
				out[r.ID] = featureapi.TenantInfo{Name: r.Name, Slug: r.Slug}
			}
			return out, nil
		},
	}
}
