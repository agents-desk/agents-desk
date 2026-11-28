import { Button, Group, Modal, PasswordInput, Stack, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus, IconTrash } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import {
  AnchorLink,
  applyServerErrors,
  confirm,
  DataTable,
  FormError,
  notifyError,
  notifySuccess,
  Page,
  PageActions,
  preloadQueries,
  ResourceSelect,
  RowActions,
  StatusDot,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { ExternalGroupsPanel, TelegramsPanel, UpcomingChannelsBanner } from "../../../../../features/channels/shared-panels"
import { wecomStatusColor } from "../../../../../features/wecom/wecom-studio"
import { teams } from "../../../../../resources"

// 内部服务渠道管理：企业微信智能机器人、内部服务 Telegram 机器人与内部外部群路由。
export const Route = createFileRoute("/$slug/settings/_service/internal-channels/")({
  loader: ({ context }) =>
    preloadQueries(context.app.queryClient, [
      { queryKey: ["resource", "wecom", "list"], queryFn: () => tenantApi.wecom.list() },
      { queryKey: ["resource", "telegram", "list", "internal"], queryFn: () => tenantApi.telegram.list({ audience: "internal" }) },
      { queryKey: ["external-groups", "list", "internal", "all"], queryFn: () => tenantApi.external.groupList({ audience: "internal" }) },
    ]),
  component: InternalChannelsPage,
})

function InternalChannelsPage(): ReactNode {
  const { slug } = Route.useParams()
  const t = useT()
  const wecomQuery = useQuery({
    queryKey: ["resource", "wecom", "list"],
    queryFn: () => tenantApi.wecom.list(),
  })
  const telegramsQuery = useQuery({
    queryKey: ["resource", "telegram", "list", "internal"],
    queryFn: () => tenantApi.telegram.list({ audience: "internal" }),
  })
  const groupsQuery = useQuery({
    queryKey: ["external-groups", "list", "internal", "all"],
    queryFn: () => tenantApi.external.groupList({ audience: "internal" }),
  })
  const pendingGroups = (groupsQuery.data ?? []).filter((g) => g.status === "pending_setup").length

  return (
    <Page
      title={t("nav.internal_channels")}
      description={t("internal_channels.description")}
      tabs={[
        {
          value: "wecom",
          label: t("nav.wecom_bots"),
          count: wecomQuery.data?.items.length || undefined,
          content: (
            <Stack gap="md">
              <WeComTab slug={slug} />
              <UpcomingChannelsBanner audience="internal" />
            </Stack>
          ),
        },
        {
          value: "telegram",
          label: t("nav.telegrams"),
          count: telegramsQuery.data?.items.length || undefined,
          content: <TelegramsPanel slug={slug} audience="internal" />,
        },
        {
          value: "groups",
          label: t("nav.internal_groups"),
          count: pendingGroups || (groupsQuery.data?.length || undefined),
          content: <ExternalGroupsPanel audience="internal" />,
        },
      ]}
    />
  )
}

/** WeComTab 管理工作区的企业微信智能机器人（WebSocket 长连接模式）列表与新建弹窗。 */
function WeComTab({ slug }: { slug: string }): ReactNode {
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<ReturnType<typeof applyServerErrors>>()

  const list = useQuery({
    queryKey: ["resource", "wecom", "list"],
    queryFn: () => tenantApi.wecom.list(),
    refetchInterval: 15_000,
  })

  const form = useForm<{
    name: string
    bot_id: string
    secret: string
    team_id: string | null
  }>({
    initialValues: { name: "", bot_id: "", secret: "", team_id: null },
  })

  const create = useMutation({
    mutationFn: (v: typeof form.values) =>
      tenantApi.wecom.create({
        name: v.name.trim(),
        bot_id: v.bot_id.trim(),
        secret: v.secret.trim(),
        team_id: v.team_id || undefined,
      }),
    onSuccess: (created) => {
      setCreating(false)
      setCreateError(undefined)
      form.reset()
      void queryClient.invalidateQueries({ queryKey: ["resource", "wecom"] })
      void navigate({ to: "/$slug/settings/internal-channels/wecom/$id", params: { slug, id: created.id } })
    },
    onError: (err) => setCreateError(applyServerErrors(form, err)),
  })

  const remove = useMutation({
    mutationFn: (id: string) => tenantApi.channel.delete(id),
    onSuccess: (res) => {
      void queryClient.invalidateQueries({ queryKey: ["resource", "wecom"] })
      void queryClient.invalidateQueries({ queryKey: kit.queries.channel.key() })
      notifySuccess(t(res.mode === "archived" ? "channels.archived_done" : "channels.deleted_done"))
    },
    onError: (err) => notifyError(err),
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
          {t("wecom.new")}
        </Button>
      </PageActions>
      <Text size="sm" c="dimmed">
        {t("wecom.description")}
      </Text>

      <DataTable
        records={list.data?.items ?? []}
        loading={list.isPending}
        empty={t("wecom.empty")}
        columns={[
          {
            accessor: "name",
            title: t("wecom.name"),
            render: (row) => (
              <AnchorLink to="/$slug/settings/internal-channels/wecom/$id" params={{ slug, id: row.id }} size="sm" fw={500}>
                {row.name}
              </AnchorLink>
            ),
          },
          {
            accessor: "bot_id",
            title: t("wecom.bot_id"),
            render: (row) => (
              <Text size="sm" c="dimmed">
                {row.bot_id}
              </Text>
            ),
          },
          {
            accessor: "team_name",
            title: t("common.team_inbox"),
            render: (row) => row.team_name || <Text c="dimmed" size="sm">{t("wecom.team_unassigned")}</Text>,
          },
          {
            accessor: "status",
            title: t("common.status"),
            render: (row) => (
              <StatusDot color={row.enabled ? wecomStatusColor(row.status) : "gray"}>
                {row.enabled ? kit.label("WecomStatus", row.status) : t("common.disabled")}
              </StatusDot>
            ),
          },
          {
            accessor: "actions",
            title: "",
            textAlign: "right",
            render: (row) => (
              <RowActions
                editLabel={t("wecom.configure")}
                onEdit={() => void navigate({ to: "/$slug/settings/internal-channels/wecom/$id", params: { slug, id: row.id } })}
                actions={[
                  {
                    label: t("wecom.delete"),
                    icon: <IconTrash size={14} />,
                    danger: true,
                    onClick: () =>
                      void confirm({
                        title: t("wecom.delete_confirm", { name: row.name }),
                        message: t("wecom.delete_message"),
                        danger: true,
                        confirmLabel: t("wecom.delete"),
                      }).then((ok) => {
                        if (ok) remove.mutate(row.id)
                      }),
                  },
                ]}
              />
            ),
          },
        ]}
      />

      <Modal opened={creating} onClose={() => setCreating(false)} closeOnClickOutside={false} title={t("wecom.create_title")}>
        <form noValidate onSubmit={form.onSubmit((v) => create.mutate(v))}>
          <Stack gap="md">
            <FormError error={createError} />
            <TextInput label={t("wecom.name")} required maxLength={60} data-autofocus {...form.getInputProps("name")} />
            <TextInput label={t("wecom.bot_id")} description={t("wecom.bot_id_hint")} required maxLength={128} {...form.getInputProps("bot_id")} />
            <PasswordInput
              label={t("wecom.secret")}
              description={t("wecom.secret_hint")}
              required
              autoComplete="off"
              {...form.getInputProps("secret")}
            />
            <ResourceSelect
              model={teams}
              label={t("wecom.team_hint")}
              placeholder={t("wecom.team_unassigned")}
              filterRow={(row) => row.status === "active"}
              getOption={(row) => ({ value: String(row.id), label: String(row.name ?? "") })}
              value={form.values.team_id}
              onChange={(v) => form.setFieldValue("team_id", v)}
              error={form.errors.team_id}
              clearable
            />
            <Group justify="flex-end" gap="xs">
              <Button variant="default" onClick={() => setCreating(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={create.isPending}>
                {t("wecom.create_submit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  )
}
