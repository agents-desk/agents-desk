import { resourceModel, settingsModel } from "@go-tenon/ui"
import { kit } from "./api"

// tenants 是工作区的资源模型：列表、新建和修改的表单、停用和启用都来自 Go 中的声明（internal/tenant/routes.go）。
export const tenants = resourceModel(kit.resources.tenant, kit)

// admins 是平台管理员的资源模型：列表、新建、修改和停用/启用都来自 Go 中的声明（internal/platformadmin/routes.go）。
export const admins = resourceModel(kit.resources.admin, kit)

// generalSettings、mailSettings 与 telegramApiSettings 是平台设置模型，供 SettingsPage 渲染多标签表单。
export const generalSettings = settingsModel(kit.settings.general, kit)
export const mailSettings = settingsModel(kit.settings.mail, kit)
export const telegramApiSettings = settingsModel(kit.settings["telegram-api"], kit)
