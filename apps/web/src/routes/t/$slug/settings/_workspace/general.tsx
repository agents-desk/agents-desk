import { Button, Group, Stack, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { applyServerErrors, FormError, notifySuccess, Page, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../api"

// 常规设置：工作区的名称。地址（slug）创建后不能修改。
export const Route = createFileRoute("/t/$slug/settings/_workspace/general")({
  staticData: { title: "nav.general", icon: "settings", order: 0, permission: "tenant-settings:manage" },
  loader: () => tenantApi.tenant.current(),
  component: General,
})

function General(): ReactNode {
  const tenant = Route.useLoaderData()
  const router = useRouter()
  const t = useT()
  const form = useForm({ initialValues: { name: tenant.name } })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()
  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await tenantApi.tenant.rename(values)
      // 重新读取布局中的工作区，侧栏顶部的名称随之更新
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
          <TextInput label={t("general.name")} required maxLength={60} {...form.getInputProps("name")} />
          <TextInput label={t("general.address")} value={`/t/${tenant.slug}/`} readOnly description={t("general.address_hint")} />
          <Group justify="flex-end">
            <Button type="submit" loading={saving}>
              {t("common.save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Page>
  )
}
