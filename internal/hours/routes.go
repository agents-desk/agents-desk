package hours

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/httpx/rest"

	"agents-desk/internal/auth"
)

// RegisterRoutes 在工作区的 API（/t/{slug}/api）中注册工作时间的设置：成员都能读取（收件箱按回复时间给等待时长变色），
// 有 tenant-settings:manage 的成员分三部分保存。require 返回要求成员登录凭据和给定权限的访问策略。
func RegisterRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB) {
	route := func(access func(*huma.Operation), id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		access(&op)
		return op
	}
	manage := require(auth.TenantSettingsManage)
	save := NewSave(db)
	rest.Register(api, route(require(), "hours-get", http.MethodGet, "/settings/business-hours", "读取工作时间"), NewGet(db).HTTP)
	rest.Register(api, route(manage, "hours-schedule-save", http.MethodPut, "/settings/business-hours/schedule", "保存时区和每周的工作时间"), save.ScheduleHTTP)
	rest.Register(api, route(manage, "hours-special-dates-save", http.MethodPut, "/settings/business-hours/special-dates", "保存特殊日期"), save.SpecialDatesHTTP)
	rest.Register(api, route(manage, "hours-reply-time-save", http.MethodPut, "/settings/business-hours/reply-time", "保存回复时间"), save.ReplyTimeHTTP)
}
