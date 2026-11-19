import { resourceModel } from "@go-tenon/ui"
import { kit } from "./api"

// members 是当前工作区的成员：列表、添加、修改角色和移出都来自 Go 中的声明（internal/member/tenant_routes.go）。
export const members = resourceModel(kit.resources.member, kit)
