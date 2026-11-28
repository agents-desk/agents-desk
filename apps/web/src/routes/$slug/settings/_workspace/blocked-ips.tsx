import { Button, Group, SimpleGrid, Stack, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconPlus, IconTrash } from "@tabler/icons-react"
import {
  applyServerErrors,
  confirm,
  DataTable,
  FormError,
  FormSection,
  notifyError,
  notifySuccess,
  Page,
  RowActions,
  useLanguage,
  useT,
  useTimeZone,
} from "@go-tenon/ui"
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
        <FormSection title={t("blocked_ips.add")}>
          <form
            noValidate
            onSubmit={form.onSubmit((values) => {
              setFormError(undefined)
              create.mutate(values)
            })}
          >
            <Stack gap="sm">
              <FormError error={formError} />
              <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
                <TextInput
                  label={t("blocked_ips.cidr")}
                  description={t("blocked_ips.cidr_hint")}
                  inputWrapperOrder={["label", "input", "description", "error"]}
                  placeholder={t("blocked_ips.cidr_placeholder")}
                  required
                  {...form.getInputProps("cidr")}
                />
                <TextInput
                  label={t("blocked_ips.reason")}
                  placeholder={t("blocked_ips.reason_placeholder")}
                  maxLength={500}
                  {...form.getInputProps("reason")}
                />
              </SimpleGrid>
              <Group justify="flex-end">
                <Button type="submit" leftSection={<IconPlus size={14} />} loading={create.isPending}>
                  {t("blocked_ips.add")}
                </Button>
              </Group>
            </Stack>
          </form>
        </FormSection>

        <DataTable
          records={rows}
          loading={list.isPending}
          empty={t("blocked_ips.empty")}
          columns={[
            {
              accessor: "cidr",
              title: t("blocked_ips.col_cidr"),
              render: (row) => (
                <Text size="sm" fw={500} ff="monospace">
                  {row.cidr}
                </Text>
              ),
            },
            {
              accessor: "reason",
              title: t("blocked_ips.col_reason"),
              render: (row) => (
                <Text size="sm" c={row.reason ? undefined : "dimmed"}>
                  {row.reason || "—"}
                </Text>
              ),
            },
            {
              accessor: "created_at",
              title: t("blocked_ips.col_created_at"),
              render: (row) => (
                <Text size="xs" c="dimmed">
                  {activity(row.created_at, ui, t, zone)}
                </Text>
              ),
            },
            {
              accessor: "actions",
              title: "",
              textAlign: "right",
              render: (row) => (
                <RowActions
                  loading={remove.isPending && remove.variables === row.id}
                  actions={[
                    {
                      label: t("blocked_ips.delete"),
                      icon: <IconTrash size={14} />,
                      danger: true,
                      loading: remove.isPending && remove.variables === row.id,
                      onClick: () => void handleDelete(row.id, row.cidr),
                    },
                  ]}
                />
              ),
            },
          ]}
        />
      </Stack>
    </Page>
  )
}
