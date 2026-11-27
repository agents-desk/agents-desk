import { Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import {
  AnchorLink,
  applyServerErrors,
  DataTable,
  FormError,
  Page,
  PageActions,
  preloadQueries,
  ResourceSelect,
  StatusDot,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../api"
import { RowActions } from "../../../../../components/row-actions"
import { ExternalGroupsPanel, TelegramsPanel, UpcomingChannelsBanner } from "../../../../../features/channels/shared-panels"
import { teams } from "../../../../../resources"

// 对外客服渠道管理：网页聊天插件、对外客服 Telegram 机器人与客户外部群路由。
export const Route = createFileRoute("/$slug/settings/_service/channels/")({
  loader: ({ context }) =>
    preloadQueries(context.app.queryClient, [
      { queryKey: ["resource", "widget", "list"], queryFn: () => tenantApi.widget.list() },
      { queryKey: ["resource", "telegram", "list", "customer"], queryFn: () => tenantApi.telegram.list({ audience: "customer" }) },
      { queryKey: ["external-groups", "list", "customer", "all"], queryFn: () => tenantApi.external.groupList({ audience: "customer" }) },
    ]),
  component: ServiceChannelsPage,
})

function ServiceChannelsPage(): ReactNode {
  const { slug } = Route.useParams()
  const t = useT()
  const widgetsQuery = useQuery({
    queryKey: ["resource", "widget", "list"],
    queryFn: () => tenantApi.widget.list(),
  })
  const telegramsQuery = useQuery({
    queryKey: ["resource", "telegram", "list", "customer"],
    queryFn: () => tenantApi.telegram.list({ audience: "customer" }),
  })
  const groupsQuery = useQuery({
    queryKey: ["external-groups", "list", "customer", "all"],
    queryFn: () => tenantApi.external.groupList({ audience: "customer" }),
  })
  const pendingGroups = (groupsQuery.data ?? []).filter((g) => g.status === "pending_setup").length

  return (
    <Page
      title={t("nav.service_channels")}
      description={t("service_channels.description")}
      tabs={[
        {
          value: "widgets",
          label: t("nav.widgets"),
          count: widgetsQuery.data?.items.length || undefined,
          content: (
            <Stack gap="md">
              <WidgetsTab slug={slug} />
              <UpcomingChannelsBanner audience="customer" />
            </Stack>
          ),
        },
        {
          value: "telegram",
          label: t("nav.telegrams"),
          count: telegramsQuery.data?.items.length || undefined,
          content: <TelegramsPanel slug={slug} audience="customer" />,
        },
        {
          value: "groups",
          label: t("nav.external_groups"),
          count: pendingGroups || (groupsQuery.data?.length || undefined),
          content: <ExternalGroupsPanel audience="customer" />,
        },
      ]}
    />
  )
}

/** WidgetsTab 管理工作区的网页聊天插件列表与新建弹窗。 */
function WidgetsTab({ slug }: { slug: string }): ReactNode {
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<ReturnType<typeof applyServerErrors>>()

  const list = useQuery({
    queryKey: ["resource", "widget", "list"],
    queryFn: () => tenantApi.widget.list(),
  })

  const form = useForm<{ name: string; team_id: string | null }>({
    initialValues: { name: "", team_id: null },
  })

  const create = useMutation({
    mutationFn: (v: { name: string; team_id: string | null }) =>
      tenantApi.widget.create({
        name: v.name.trim(),
        team_id: v.team_id || undefined,
      }),
    onSuccess: (created) => {
      setCreating(false)
      setCreateError(undefined)
      form.reset()
      void queryClient.invalidateQueries({ queryKey: ["resource", "widget"] })
      void navigate({ to: "/$slug/settings/channels/widgets/$id", params: { slug, id: created.id } })
    },
    onError: (err) => {
      setCreateError(applyServerErrors(form, err))
    },
  })

  const openCreate = () => {
    setCreateError(undefined)
    form.reset()
    setCreating(true)
  }

  return (
    <Stack gap="sm">
      <PageActions>
        <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
          {t("widgets.new")}
        </Button>
      </PageActions>
      <Text size="sm" c="dimmed">
        {t("widgets.description")}
      </Text>

      <DataTable
        records={list.data?.items ?? []}
        loading={list.isPending}
        empty={t("widgets.empty")}
        columns={[
          {
            accessor: "name",
            title: t("widget_studio.name"),
            render: (row) => (
              <AnchorLink to="/$slug/settings/channels/widgets/$id" params={{ slug, id: row.id }} size="sm" fw={500}>
                {row.name}
              </AnchorLink>
            ),
          },
          {
            accessor: "team_name",
            title: t("common.team_inbox"),
            render: (row) => row.team_name || <Text c="dimmed" size="sm">{t("widgets.team_unassigned")}</Text>,
          },
          {
            accessor: "enabled",
            title: t("common.status"),
            render: (row) => <StatusDot color={row.enabled ? "green" : "gray"}>{t(row.enabled ? "common.enabled" : "common.disabled")}</StatusDot>,
          },
          {
            accessor: "actions",
            title: "",
            textAlign: "right",
            render: (row) => (
              <RowActions
                editLabel={t("widgets.configure")}
                onEdit={() => void navigate({ to: "/$slug/settings/channels/widgets/$id", params: { slug, id: row.id } })}
              />
            ),
          },
        ]}
      />

      <Modal opened={creating} onClose={() => setCreating(false)} title={t("widgets.create_title")}>
        <form onSubmit={form.onSubmit((v) => create.mutate(v))}>
          <Stack gap="md">
            <FormError error={createError} />
            <TextInput
              label={t("widget_studio.name")}
              description={t("widget_studio.name_hint")}
              required
              maxLength={60}
              data-autofocus
              {...form.getInputProps("name")}
            />
            <ResourceSelect
              model={teams}
              label={t("widgets.team_hint")}
              placeholder={t("widgets.team_unassigned")}
              filterRow={(row) => row.status === "active"}
              getOption={(row) => ({ value: String(row.id), label: String(row.name ?? "") })}
              value={form.values.team_id}
              onChange={(v) => form.setFieldValue("team_id", v)}
              error={form.errors.team_id}
              clearable
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setCreating(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={create.isPending}>
                {t("widgets.create_submit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  )
}
