import { resourceModel } from "@go-tenon/ui"
import { kit } from "./api"

// members 是当前工作区的成员：列表、添加、修改角色和移出都来自 Go 中的声明（internal/member/tenant_routes.go）。
export const members = resourceModel(kit.resources.member, kit)

// teams 是当前工作区的团队收件箱：列表、新建、修改和删除来自 Go 中的声明（internal/team/routes.go）。
export const teams = resourceModel(kit.resources.team, kit)

// roles 是当前工作区的角色：列表、新建、修改和删除来自 Go 中的声明（internal/role/routes.go）。
export const roles = resourceModel(kit.resources.role, kit)

// widgets 是当前工作区的网页聊天插件：列表、新建和修改来自 Go 中的声明（internal/widget/routes.go）。
export const widgets = resourceModel(kit.resources.widget, kit)

// tags 是当前工作区的标签：列表、新建、修改和删除来自 Go 中的声明（internal/tag/routes.go）。
export const tags = resourceModel(kit.resources.tag, kit)
