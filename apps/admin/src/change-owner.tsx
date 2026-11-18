import { Button, Group, Modal, PasswordInput, Stack, Text, TextInput } from "@mantine/core"
import { useQueryClient } from "@tanstack/react-query"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, useSchemaForm } from "@go-tenon/ui"
import type { TenantDTO } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api } from "./auth"
import { kit, tenants } from "./resources"

// ChangeOwnerModal 更换工作区的主管理员：填写新主管理员的邮箱；这个邮箱还没有成员账号时，再填写姓名和初始密码。
// 原主管理员的 owner 角色被撤销。tenant 为空时关闭。
export function ChangeOwnerModal({ tenant, onClose }: { tenant?: TenantDTO; onClose: () => void }) {
  const queryClient = useQueryClient()
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
    <Modal opened={!!tenant} onClose={onClose} title={`更换“${tenant?.name ?? ""}”的主管理员`}>
      <form
        onSubmit={form.onSubmit(async (values) => {
          if (!tenant) return
          setSaving(true)
          try {
            await api.tenant.changeOwner(tenant.id, values)
            await queryClient.invalidateQueries({ queryKey: [...tenants.queryKey] }) // 列表和详情
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
            现在的主管理员是 {tenant?.owner_name}（{tenant?.owner_email}）。更换后，他在这个工作区中的主管理员角色被撤销。
          </Text>
          <FormError error={error} />
          <TextInput label="新主管理员的邮箱" required {...form.getInputProps("email")} />
          <Text size="sm" c="dimmed">
            这个邮箱已经有成员账号时，不需要填写下面两项。
          </Text>
          <TextInput label="姓名" {...form.getInputProps("name")} />
          <PasswordInput label="初始密码" {...form.getInputProps("password")} />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              取消
            </Button>
            <Button type="submit" loading={saving}>
              更换
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
