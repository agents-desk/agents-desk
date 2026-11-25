import { Avatar, Button, FileButton, Group, Stack, Text } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, notifyError, SecretInput, SettingsPage, useT } from "@go-tenon/ui"
import { useEffect, useState } from "react"
import { api, kit } from "../../../api"
import { generalSettings, mailSettings } from "../../../resources"

// 系统设置：通过框架的 SettingsPage 把常规设置和邮件服务（Resend）合并为多标签页，字段和校验来自 Go 声明。
export const Route = createFileRoute("/_app/settings/general")({
  staticData: { title: "nav.general_settings", icon: "settings", order: 5, permission: "platform-settings:manage" },
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

// LogoField 上传平台 Logo 为临时文件并回填 logo_file_id，保存表单后服务端正式确认并同步至公开站点信息。
const LogoField: FieldInput = ({ label, value, onChange, error, disabled }) => {
  const t = useT()
  const site = useQuery(kit.queries.site.get())
  const general = useQuery(kit.queries.general.settingsGet())
  const [previews, setPreviews] = useState<Record<string, string>>({})
  const [uploading, setUploading] = useState(false)

  const fileId = typeof value === "string" ? value : ""
  const preview = fileId
    ? (previews[fileId] ?? (fileId === general.data?.logo_file_id ? site.data?.logo_url : undefined))
    : undefined

  const choose = async (file: File | null) => {
    if (!file) return
    setUploading(true)
    try {
      const uploaded = await api.file.upload({ file })
      setPreviews((prev) => ({ ...prev, [uploaded.id]: uploaded.url }))
      onChange?.(uploaded.id)
    } catch (err) {
      notifyError(err, t("settings.logo_upload_failed"))
    } finally {
      setUploading(false)
    }
  }

  return (
    <Stack gap={6}>
      <Text size="sm" fw={500}>
        {label}
      </Text>
      <Group gap="md">
        <Avatar src={preview || null} size={56} radius="md" color="brand">
          {(site.data?.site_name ?? "A").slice(0, 1)}
        </Avatar>
        <Stack gap={6}>
          <Group gap="xs">
            <FileButton onChange={(f) => void choose(f)} accept="image/png,image/jpeg,image/webp" disabled={disabled}>
              {(props) => (
                <Button {...props} variant="default" size="xs" loading={uploading} disabled={disabled}>
                  {t("settings.logo_upload")}
                </Button>
              )}
            </FileButton>
            {fileId && !disabled && (
              <Button variant="subtle" color="gray" size="xs" onClick={() => onChange?.("")}>
                {t("settings.logo_remove")}
              </Button>
            )}
          </Group>
          <Text size="xs" c={error ? "red" : "dimmed"}>
            {error ?? t("settings.logo_hint")}
          </Text>
        </Stack>
      </Group>
    </Stack>
  )
}

function GeneralSettingsPage() {
  const t = useT()
  const queryClient = useQueryClient()
  const general = useQuery(kit.queries.general.settingsGet()).data
  const siteName = general?.site_name
  const logoFileId = general?.logo_file_id
  // 平台名称或 Logo 保存后刷新公开站点信息缓存，顶栏、登录页与浏览器标签图标立即同步更新。
  useEffect(() => {
    if (siteName !== undefined || logoFileId !== undefined) {
      void queryClient.invalidateQueries({ queryKey: kit.queries.site.get().queryKey })
    }
  }, [siteName, logoFileId, queryClient])
  return (
    <SettingsPage
      title={t("nav.general_settings")}
      groups={[
        { model: generalSettings, description: t("settings.general_description"), inputs: { logo_file_id: LogoField } },
        { model: mailSettings, description: t("settings.mail_description"), inputs: { api_key: APIKeyField } },
      ]}
    />
  )
}
