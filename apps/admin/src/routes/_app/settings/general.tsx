import { Button, Group, Paper, Stack, Text, TextInput, Title } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { AvatarField, type FieldInput, notifyError, notifySuccess, Page, SecretInput, SettingsForm, useT } from "@go-tenon/ui"
import { type FormEvent, useEffect, useState } from "react"
import { api, kit } from "../../../api"
import { generalSettings, mailSettings } from "../../../resources"

// 系统设置：通过框架的 SettingsPage 把常规设置和邮件服务（Resend）合并为多标签页，字段和校验来自 Go 声明。
export const Route = createFileRoute("/_app/settings/general")({
  staticData: { title: "nav.general_settings", icon: "settings", order: 6, permission: "platform-settings:manage" },
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
const LogoField: FieldInput = ({ label, value, onChange, error }) => {
  const t = useT()
  const site = useQuery(kit.queries.site.get())
  const general = useQuery(kit.queries.general.settingsGet())
  const [previews, setPreviews] = useState<Record<string, string>>({})

  const fileId = typeof value === "string" ? value : ""
  const preview = fileId
    ? (previews[fileId] ?? (fileId === general.data?.logo_file_id ? site.data?.logo_url : undefined))
    : undefined

  return (
    <AvatarField
      label={label}
      name={site.data?.site_name ?? "A"}
      previewUrl={preview}
      size={56}
      radius="md"
      onUpload={(file) => api.file.upload({ file })}
      onChange={({ id, url }) => {
        if (id && url) {
          setPreviews((prev) => ({ ...prev, [id]: url }))
        }
        onChange?.(id)
      }}
      hint={t("settings.logo_hint")}
      error={error}
    />
  )
}

function MailTestCard() {
  const t = useT()
  const [to, setTo] = useState("")
  const send = useMutation({
    mutationFn: (recipient: string) => api.mail.settingsTest({ to: recipient || undefined }),
    onSuccess: (res) => notifySuccess(t("settings.mail_test_sent", { to: res.to })),
    onError: (err) => notifyError(err),
  })
  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    send.mutate(to.trim())
  }
  return (
    <Paper withBorder p="md" radius="md">
      <form onSubmit={onSubmit} noValidate>
        <Stack gap="sm">
          <div>
            <Title order={5}>{t("settings.mail_test_title")}</Title>
            <Text size="xs" c="dimmed" mt={4}>
              {t("settings.mail_test_description")}
            </Text>
          </div>
          <Group align="flex-end" gap="sm" wrap="wrap">
            <TextInput
              type="email"
              label={t("settings.mail_test_to")}
              placeholder={t("settings.mail_test_to_placeholder")}
              value={to}
              onChange={(e) => setTo(e.currentTarget.value)}
              style={{ flex: 1, minWidth: 240 }}
            />
            <Button type="submit" variant="default" loading={send.isPending}>
              {t("settings.mail_test_submit")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Paper>
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
    <Page
      title={t("nav.general_settings")}
      width="narrow"
      tabs={[
        {
          value: generalSettings.name,
          label: generalSettings.title,
          content: (
            <SettingsForm
              model={generalSettings}
              title={null}
              description={t("settings.general_description")}
              inputs={{ logo_file_id: LogoField }}
            />
          ),
        },
        {
          value: mailSettings.name,
          label: mailSettings.title,
          content: (
            <Stack gap="lg">
              <SettingsForm
                model={mailSettings}
                title={null}
                description={t("settings.mail_description")}
                inputs={{ api_key: APIKeyField }}
              />
              <MailTestCard />
            </Stack>
          ),
        },
      ]}
    />
  )
}
