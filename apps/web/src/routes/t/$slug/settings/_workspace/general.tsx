import { Button, Group, Stack, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { createFileRoute, useRouter } from "@tanstack/react-router"
import { applyServerErrors, FormError, notifySuccess, Page } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../api"

// 常规设置：工作区的名称。地址（slug）创建后不能修改。
export const Route = createFileRoute("/t/$slug/settings/_workspace/general")({
  staticData: { title: "常规", icon: "settings", order: 0, permission: "tenant-settings:manage" },
  loader: () => tenantApi.tenant.current(),
  component: General,
})

function General(): ReactNode {
  const tenant = Route.useLoaderData()
  const router = useRouter()
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
      notifySuccess("已保存")
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })
  return (
    <Page title="常规">
      <form noValidate onSubmit={submit}>
        <Stack gap="md">
          <FormError error={error} />
          <TextInput label="名称" required maxLength={60} {...form.getInputProps("name")} />
          <TextInput label="地址" value={`/t/${tenant.slug}/`} readOnly description="创建后不能修改" />
          <Group justify="flex-end">
            <Button type="submit" loading={saving}>
              保存
            </Button>
          </Group>
        </Stack>
      </form>
    </Page>
  )
}
