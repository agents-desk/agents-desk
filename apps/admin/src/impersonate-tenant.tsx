import { Button, Group, Modal, Select, Stack, Switch, Text, TextInput } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, useSchemaForm, useT } from "@go-tenon/ui"
import type { TenantDTO } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api, kit } from "./api"

// ImpersonateTenantModal 管理员端代为登录工作区的弹窗：选择要代登的工作区成员（默认主管理员）、填写排查原因、
// 可选开启只读排查模式；提交后服务端写入独立的 __Host-impersonation cookie，并打开目标工作区。
export function ImpersonateTenantModal({ tenant, onClose }: { tenant?: TenantDTO; onClose: () => void }) {
  const t = useT()
  const membersQuery = useQuery({
    queryKey: ["tenant-impersonate-members", tenant?.id],
    queryFn: () => api.tenant.members(tenant!.id),
    enabled: !!tenant,
  })
  const form = useSchemaForm({
    schema: kit.schemas.tenant.impersonate.body,
    rules: kit,
    initialValues: { member_id: "", reason: "", read_only: false },
  })
  const [error, setError] = useState<ErrorInfo>()
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (tenant) {
      form.setValues({ member_id: tenant.owner_id, reason: "", read_only: false })
      setError(undefined)
    }
  }, [tenant])

  const memberOptions = (membersQuery.data ?? []).map((m) => ({
    value: m.id,
    label: m.owner ? `${m.name} (${m.email}) · ${t("impersonate.owner_tag")}` : `${m.name} (${m.email})`,
  }))

  return (
    <Modal opened={!!tenant} onClose={onClose} title={t("impersonate.title", { name: tenant?.name ?? "" })}>
      <form
        onSubmit={form.onSubmit(async (values) => {
          if (!tenant) return
          setSubmitting(true)
          setError(undefined)
          try {
            const res = await api.tenant.impersonate(tenant.id, {
              member_id: values.member_id || undefined,
              reason: values.reason,
              read_only: values.read_only,
            })
            onClose()
            const opened = window.open(res.url, "_blank")
            if (!opened) {
              window.location.assign(res.url)
            }
          } catch (err) {
            setError(applyServerErrors(form, err))
          } finally {
            setSubmitting(false)
          }
        })}
      >
        <Stack>
          <Text size="sm" c="dimmed">
            {t("impersonate.description")}
          </Text>
          <FormError error={error} />
          <Select
            label={t("impersonate.member")}
            data={memberOptions}
            searchable
            disabled={membersQuery.isLoading}
            {...form.getInputProps("member_id")}
          />
          <TextInput
            label={t("impersonate.reason")}
            placeholder={t("impersonate.reason_placeholder")}
            required
            {...form.getInputProps("reason")}
          />
          <Switch
            label={t("impersonate.read_only")}
            description={t("impersonate.read_only_hint")}
            {...form.getInputProps("read_only", { type: "checkbox" })}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" color="deep-orange" loading={submitting}>
              {t("impersonate.submit")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
