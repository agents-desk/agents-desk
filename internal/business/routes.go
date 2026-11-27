package business

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/settings"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/platformsettings"
)

// RegisterRoutes 在工作区 API（/t/{slug}/api）中注册商务沟通策略与管理员全员渠道治理接口。
func RegisterRoutes(
	api *rest.API,
	require func(...auth.Permission) func(*huma.Operation),
	db *bun.DB,
	roles *authz.Store,
	files *filesystem.Service,
	policy *settings.Group[Policy],
	features *feature.Store,
	tgSettings *settings.Group[platformsettings.Telegram],
	down conversation.Downstream,
	publicURL string,
	onTGChanged func(),
) {
	read := require(auth.ConversationsRead)
	manage := require(auth.BusinessChannelsManage)
	route := func(guard func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		guard(&op)
		return op
	}

	pa := NewPolicyActions(db, policy, features, tgSettings)
	gov := NewGovernance(db, roles, files, down, publicURL, onTGChanged)

	rest.Register(api, route(read, "business-policy-get", http.MethodGet, "/settings/business-policy", "读取工作区商务沟通策略"), pa.GetHTTP)
	rest.Register(api, route(manage, "business-policy-save", http.MethodPut, "/settings/business-policy", "保存工作区商务沟通策略"), pa.SaveHTTP)
	rest.Register(api, route(manage, "business-channel-list", http.MethodGet, "/business-channels", "工作区全员商务渠道治理列表"), gov.ListHTTP)
	rest.Register(api, route(manage, "business-channel-set-enabled", http.MethodPut, "/business-channels/{id}/enabled", "启用或停用成员商务渠道"), gov.SetEnabledHTTP)
	rest.Register(api, route(manage, "business-channel-add-collaborators", http.MethodPost, "/business-channels/{id}/collaborators", "应急添加商务渠道协作成员"), gov.AddCollaboratorsHTTP)
}
