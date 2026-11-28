import { Button, Group, Modal, PasswordInput, Stack, Text, TextInput } from "@mantine/core"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, useResourceMutations, useSchemaForm, useT } from "@go-tenon/ui"
import type { TenantDTO } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api, kit } from "./api"
import { tenants } from "./resources"

// ChangeOwnerModal 更换工作区的主管理员：填写新主管理员的邮箱；这个邮箱还没有成员账号时，再填写姓名和初始密码。
// 原主管理员的 owner 角色被撤销。tenant 为空时关闭。
export function ChangeOwnerModal({ tenant, onClose }: { tenant?: TenantDTO; onClose: () => void }) {
  const { invalidate } = useResourceMutations(tenants)
  const t = useT()
  const form = useSchemaForm({
    schema: kit.schemas.tenant.changeOwner.body,
    rules: kit,
    initialValues: { email: "", name: "", password: "" },
  })
  const [error, setError] = useState<ErrorInfo>()
  const [saving, setSaving] = useState(false)

  // 每次打开时清空上一次填写的内容
  useEffect(() => {
    if (tenant) {
      form.reset()
      setError(undefined)
    }
  }, [tenant])

  return (
    <Modal
      opened={!!tenant}
      onClose={onClose}
      closeOnClickOutside={false}
      title={t("change_owner.title", { name: tenant?.name ?? "" })}
    >
      <form
        noValidate
        onSubmit={form.onSubmit(async (values) => {
          if (!tenant) return
          setSaving(true)
          try {
            await api.tenant.changeOwner(tenant.id, values)
            await invalidate() // 列表和详情重新读取
            onClose()
          } catch (err) {
            setError(applyServerErrors(form, err))
          } finally {
            setSaving(false)
          }
        })}
      >
        <Stack>
          <Text size="sm" c="dimmed">
            {t("change_owner.current", { name: tenant?.owner_name, email: tenant?.owner_email })}
          </Text>
          <FormError error={error} form={form} />
          <TextInput label={t("change_owner.email")} required data-autofocus {...form.getInputProps("email")} />
          <Text size="sm" c="dimmed">
            {t("change_owner.existing_hint")}
          </Text>
          <TextInput label={t("change_owner.name")} {...form.getInputProps("name")} />
          <PasswordInput label={t("change_owner.password")} {...form.getInputProps("password")} />
          <Group justify="flex-end" gap="xs">
            <Button variant="default" disabled={saving} onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" loading={saving}>
              {t("change_owner.submit")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
