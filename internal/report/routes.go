package report

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册工作区报表接口，全部要求 reports:read 权限。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, roles *authz.Store, files *filesystem.Service) {
	route := func(id, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: http.MethodGet, Path: path, Summary: summary, Tags: []string{"report"}}
		require(auth.ReportsRead)(&op)
		return op
	}
	rest.Register(api, route("report-overview", "/reports/overview", "报表概览"), NewOverview(db, roles).HTTP)
	rest.Register(api, route("report-conversations", "/reports/conversations", "会话报表"), NewConversations(db).HTTP)
	rest.Register(api, route("report-response-time", "/reports/response-time", "响应时间报表"), NewResponseTime(db).HTTP)
	rest.Register(api, route("report-satisfaction", "/reports/satisfaction", "满意度报表"), NewSatisfaction(db).HTTP)
	rest.Register(api, route("report-satisfaction-ratings", "/reports/satisfaction/ratings", "满意度评价明细"), NewListRatings(db).HTTP)
	rest.Register(api, route("report-tickets", "/reports/tickets", "工单报表"), NewTickets(db, roles, files).HTTP)
	rest.Register(api, route("report-business", "/reports/business", "商务沟通报表"), NewBusiness(db, roles, files).HTTP)
	rest.Register(api, route("report-members", "/reports/members", "成员绩效报表"), NewMembers(db, roles, files).HTTP)
	rest.Register(api, route("report-translation", "/reports/translation", "AI 与翻译报表"), NewTranslation(db).HTTP)
}
