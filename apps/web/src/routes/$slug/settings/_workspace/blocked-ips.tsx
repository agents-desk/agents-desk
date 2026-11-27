import { ActionIcon, Button, Group, Paper, Stack, Table, Text, TextInput, Tooltip } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconPlus, IconTrash } from "@tabler/icons-react"
import { applyServerErrors, confirm, FormError, notifyError, notifySuccess, Page, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../api"
import { activity } from "../../../../inbox/format"

// IP 黑名单设置页：管理工作区级的访客 IP 与 CIDR 封禁规则。
export const Route = createFileRoute("/$slug/settings/_workspace/blocked-ips")({
  staticData: { title: "nav.blocked_ips", icon: "shield", order: 4, permission: "tenant-settings:manage" },
  component: BlockedIPsPage,
})

function BlockedIPsPage(): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const list = useQuery(kit.queries.blocked.ipList())
  const [formError, setFormError] = useState<ReturnType<typeof applyServerErrors>>()

  const form = useForm({
    initialValues: {
      cidr: "",
      reason: "",
    },
  })

  const create = useMutation({
    mutationFn: (values: { cidr: string; reason: string }) =>
      tenantApi.blocked.ipCreate({ cidr: values.cidr.trim(), reason: values.reason.trim() || undefined }),
    onSuccess: () => {
      form.reset()
      setFormError(undefined)
      void queryClient.invalidateQueries({ queryKey: kit.queries.blocked.key() })
      notifySuccess(t("blocked_ips.added"))
    },
    onError: (err) => {
      setFormError(applyServerErrors(form, err))
    },
  })

  const remove = useMutation({
    mutationFn: (id: string) => tenantApi.blocked.ipDelete(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: kit.queries.blocked.key() })
      notifySuccess(t("blocked_ips.deleted"))
    },
    onError: (err) => notifyError(err),
  })

  const handleDelete = async (id: string, cidr: string) => {
    const ok = await confirm({
      title: t("blocked_ips.delete_confirm", { cidr }),
      message: t("blocked_ips.delete_message"),
      danger: true,
      confirmLabel: t("blocked_ips.delete"),
    })
    if (ok) remove.mutate(id)
  }

  const rows = list.data ?? []

  return (
    <Page title={t("nav.blocked_ips")} description={t("blocked_ips.description")}>
      <Stack gap="lg">
        <Paper withBorder p="md" radius="md">
          <form
            noValidate
            onSubmit={form.onSubmit((values) => {
              setFormError(undefined)
              create.mutate(values)
            })}
          >
            <Stack gap="sm">
              <FormError error={formError} />
              <Group align="flex-start" gap="sm" wrap="wrap">
                <TextInput
                  label={t("blocked_ips.cidr")}
                  description={t("blocked_ips.cidr_hint")}
                  placeholder={t("blocked_ips.cidr_placeholder")}
                  required
                  style={{ flex: 1, minWidth: 220 }}
                  {...form.getInputProps("cidr")}
                />
                <TextInput
                  label={t("blocked_ips.reason")}
                  placeholder={t("blocked_ips.reason_placeholder")}
                  maxLength={500}
                  style={{ flex: 1, minWidth: 220 }}
                  {...form.getInputProps("reason")}
                />
              </Group>
              <Group justify="flex-end">
                <Button type="submit" leftSection={<IconPlus size={14} />} loading={create.isPending}>
                  {t("blocked_ips.add")}
                </Button>
              </Group>
            </Stack>
          </form>
        </Paper>

        {rows.length === 0 && !list.isPending ? (
          <Paper withBorder p="lg" radius="md">
            <Text size="sm" c="dimmed" ta="center">
              {t("blocked_ips.empty")}
            </Text>
          </Paper>
        ) : (
          <Paper withBorder radius="md" style={{ overflow: "hidden" }}>
            <Table verticalSpacing="sm" horizontalSpacing="md">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("blocked_ips.col_cidr")}</Table.Th>
                  <Table.Th>{t("blocked_ips.col_reason")}</Table.Th>
                  <Table.Th>{t("blocked_ips.col_created_at")}</Table.Th>
                  <Table.Th w={60} />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rows.map((row) => (
                  <Table.Tr key={row.id}>
                    <Table.Td>
                      <Text size="sm" fw={500} ff="monospace">
                        {row.cidr}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c={row.reason ? undefined : "dimmed"}>
                        {row.reason || "—"}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="xs" c="dimmed">
                        {activity(row.created_at, ui, t, zone)}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Tooltip label={t("blocked_ips.delete")}>
                        <ActionIcon
                          variant="subtle"
                          color="red"
                          loading={remove.isPending}
                          aria-label={t("blocked_ips.delete")}
                          onClick={() => void handleDelete(row.id, row.cidr)}
                        >
                          <IconTrash size={15} />
                        </ActionIcon>
                      </Tooltip>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Paper>
        )}
      </Stack>
    </Page>
  )
}
