package translation

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册翻译：回复前的翻译预览、补译看到的消息，以及工作区的翻译设置（语气、术语表）。
// incoming 是识别和翻译收到的消息的处理器，回复预览在还不知道对方的语言时用它先识别。require 返回要求成员登录凭据和给定权限的访问策略。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, translator *Translator, incoming *Incoming, s *settings.Group[Settings]) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	manage := require(auth.TenantSettingsManage)
	actions := NewSettingsActions(db, s, translator)
	rest.Register(api, route(require(auth.ConversationsRead), "translation-preview", http.MethodPost, "/conversations/{id}/translations", "回复的译文和回译"),
		NewPreview(db, translator, incoming).HTTP)
	rest.Register(api, route(require(auth.ConversationsRead), "translation-fill", http.MethodPost, "/translations/fill", "补译看到的消息"),
		NewFillMissing(db, translator).HTTP)
	rest.Register(api, route(require(), "translation-settings", http.MethodGet, "/settings/translation", "读取翻译设置"), actions.GetHTTP)
	rest.Register(api, route(manage, "translation-tone-save", http.MethodPut, "/settings/translation/tone", "保存翻译的语气"), actions.ToneHTTP)
	rest.Register(api, route(manage, "translation-glossary-save", http.MethodPut, "/settings/translation/glossary", "保存术语表"), actions.GlossaryHTTP)
}
