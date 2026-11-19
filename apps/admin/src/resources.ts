import { resourceModel } from "@go-tenon/ui"
import { kit } from "./api"

// tenants 是工作区的资源模型：列表、新建和修改的表单、停用和启用都来自 Go 中的声明（internal/tenant/routes.go）。
export const tenants = resourceModel(kit.resources.tenant, kit)
