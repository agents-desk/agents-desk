import { Avatar, Button, FileButton, Group, NumberInput, Stack, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { applyServerErrors, FormError, notifyError, notifySuccess, Page, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { api, tenantApi } from "../../../../../api"

// 常规设置：工作区的 Logo、名称和单窗口渠道的重开窗口。地址（slug）创建后不能修改。
export const Route = createFileRoute("/t/$slug/settings/_workspace/general")({
  staticData: { title: "nav.general", icon: "settings", order: 0, permission: "tenant-settings:manage" },
  loader: () => tenantApi.tenant.current(),
  component: General,
})

function General(): ReactNode {
  const tenant = Route.useLoaderData()
  const router = useRouter()
  const t = useT()
  const form = useForm({
    initialValues: {
      name: tenant.name,
      logo_file_id: tenant.logo_file_id ?? "",
      reopen_window_hours: tenant.reopen_window_hours,
    },
  })
  const [preview, setPreview] = useState(tenant.logo_url)
  const [uploading, setUploading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const chooseLogo = async (file: File | null) => {
    if (!file) return
    setUploading(true)
    try {
      const uploaded = await api.file.upload({ file })
      form.setFieldValue("logo_file_id", uploaded.id)
      form.clearFieldError("logo_file_id")
      setPreview(uploaded.url)
    } catch (err) {
      notifyError(err, t("general.logo_upload_failed"))
    } finally {
      setUploading(false)
    }
  }

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await tenantApi.tenant.rename({
        name: values.name,
        logo_file_id: values.logo_file_id,
        reopen_window_hours: Number(values.reopen_window_hours),
      })
      // 重新读取布局中的工作区，侧栏顶部的名称、Logo 和浏览器标签图标随之更新
      await router.invalidate()
      form.resetDirty()
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  return (
    <Page title={t("nav.general")}>
      <form noValidate onSubmit={submit}>
        <Stack gap="md">
          <FormError error={error} />
          <Stack gap={6}>
            <Text size="sm" fw={500}>
              {t("general.logo")}
            </Text>
            <Group gap="md">
              <Avatar src={preview || null} size={56} radius="md" color="brand">
                {form.values.name.slice(0, 1)}
              </Avatar>
              <Stack gap={6}>
                <Group gap="xs">
                  <FileButton onChange={(f) => void chooseLogo(f)} accept="image/png,image/jpeg,image/webp">
                    {(props) => (
                      <Button {...props} variant="default" size="xs" loading={uploading}>
                        {t("general.logo_upload")}
                      </Button>
                    )}
                  </FileButton>
                  {preview && (
                    <Button
                      variant="subtle"
                      color="gray"
                      size="xs"
                      onClick={() => {
                        form.setFieldValue("logo_file_id", "")
                        setPreview("")
                      }}
                    >
                      {t("general.logo_remove")}
                    </Button>
                  )}
                </Group>
                <Text size="xs" c={form.errors.logo_file_id ? "red" : "dimmed"}>
                  {form.errors.logo_file_id ?? t("general.logo_hint")}
                </Text>
              </Stack>
            </Group>
          </Stack>
          <TextInput label={t("general.name")} required maxLength={60} {...form.getInputProps("name")} />
          <TextInput label={t("general.address")} value={`/t/${tenant.slug}/`} readOnly description={t("general.address_hint")} />
          <NumberInput
            label={t("general.reopen_window")}
            description={t("general.reopen_window_hint")}
            min={0}
            max={720}
            allowDecimal={false}
            allowNegative={false}
            {...form.getInputProps("reopen_window_hours")}
          />
          <Group justify="flex-end">
            <Button type="submit" disabled={!form.isDirty()} loading={saving}>
              {t("common.save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Page>
  )
}
