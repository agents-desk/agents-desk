import { Button, Group, NumberInput, Stack, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { applyServerErrors, AvatarField, FormError, notifySuccess, Page, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { api, tenantApi } from "../../../../api"
import { useUnsavedGuard } from "../../../../components/unsaved-guard"

// 常规设置：工作区的 Logo、名称和单窗口渠道的重开窗口。地址（slug）创建后不能修改。
export const Route = createFileRoute("/$slug/settings/_workspace/general")({
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
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const dirty = form.isDirty() || preview !== tenant.logo_url
  const unsaved = useUnsavedGuard(dirty)

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await tenantApi.tenant.rename({
        name: values.name,
        logo_file_id: values.logo_file_id,
        reopen_window_hours: Number(values.reopen_window_hours),
      })
      unsaved.release()
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
    <Page title={t("nav.general")} width="narrow">
      <form noValidate onSubmit={submit}>
        <Stack gap="md">
          <FormError error={error} />
          <AvatarField
            label={t("general.logo")}
            name={form.values.name}
            previewUrl={preview}
            size={56}
            radius="md"
            onUpload={(file) => api.file.upload({ file })}
            onChange={({ id, url }) => {
              form.setFieldValue("logo_file_id", id)
              form.clearFieldError("logo_file_id")
              setPreview(url)
            }}
            hint={t("general.logo_hint")}
            error={form.errors.logo_file_id}
          />
          <TextInput label={t("general.name")} required maxLength={60} {...form.getInputProps("name")} />
          <TextInput label={t("general.address")} value={`/app/${tenant.slug}/`} readOnly description={t("general.address_hint")} />
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
            <Button type="submit" disabled={!dirty} loading={saving}>
              {t("common.save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Page>
  )
}
