import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, SecretInput, SettingsPage, useT } from "@go-tenon/ui"
import { generalSettings, mailSettings } from "../../../resources"

// 系统设置：通过框架的 SettingsPage 把常规设置和邮件服务（Resend）合并为多标签页，字段和校验来自 Go 声明。
export const Route = createFileRoute("/_app/settings/general")({
  staticData: { title: "nav.general_settings", order: 1, permission: "platform-settings:manage" },
  component: GeneralSettingsPage,
})

// Resend API Key 使用框架的 SecretInput（默认掩码，可切换显示）。
const APIKeyField: FieldInput = ({ field, label, value, onChange, error, disabled }) => (
  <SecretInput
    label={label}
    placeholder="re_..."
    required={field.required}
    error={error}
    disabled={disabled}
    value={(value as string) ?? ""}
    onChange={(e) => onChange?.(e.currentTarget.value)}
  />
)

function GeneralSettingsPage() {
  const t = useT()
  return (
    <SettingsPage
      title={t("nav.general_settings")}
      groups={[
        { model: generalSettings, description: t("settings.general_description") },
        { model: mailSettings, description: t("settings.mail_description"), inputs: { api_key: APIKeyField } },
      ]}
    />
  )
}
