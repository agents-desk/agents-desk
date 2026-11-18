import { resourceModel } from "@go-tenon/ui"
import { createKit } from "@agents-desk/client/admin/kit"
import { api, session } from "./auth"

// kit 是生成的客户端和元数据：查询选项、校验规则、验证提示、枚举的显示文字和资源。
// scope 让查询的缓存按登录的账号区分。
export const kit = createKit(api, { scope: session.scope, language: "zh-CN" })

// tenants 是工作区的资源模型：列表、新建和修改的表单、停用和启用都来自 Go 中的声明（internal/tenant/routes.go）。
export const tenants = resourceModel(kit.resources.tenant, kit)
