import type { ExtgroupGroupDTO, ExtgroupStatus } from "@agents-desk/client/tenant"
import { Badge, Button, Group, Modal, PasswordInput, SegmentedControl, Stack, Switch, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { AnchorLink, applyServerErrors, DataTable, FormError, notifyError, notifySuccess, Page, PageActions, preloadQueries, ResourceSelect, StatusDot, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { RowActions } from "../../../../../components/row-actions"
import { wecomStatusColor } from "../../../../../features/wecom/wecom-studio"
import { ChannelIcon } from "../../../../../inbox/channel-badge"
import { activity } from "../../../../../inbox/format"
import { LanguageSelect } from "../../../../../LanguageSelect"
import { teams } from "../../../../../resources"

// 服务渠道与外部群管理：分标签展示网页聊天插件、Telegram 机器人、企业微信机器人与已接入的外部群路由。
export const Route = createFileRoute("/$slug/settings/_service/channels/")({
  loader: ({ context }) =>
    preloadQueries(context.app.queryClient, [
      { queryKey: ["resource", "widget", "list"], queryFn: () => tenantApi.widget.list() },
      { queryKey: ["resource", "telegram", "list"], queryFn: () => tenantApi.telegram.list() },
      { queryKey: ["resource", "wecom", "list"], queryFn: () => tenantApi.wecom.list() },
      { queryKey: ["external-groups", "list", "all"], queryFn: () => tenantApi.external.groupList() },
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
    queryKey: ["resource", "telegram", "list"],
    queryFn: () => tenantApi.telegram.list(),
  })
  const wecomQuery = useQuery({
    queryKey: ["resource", "wecom", "list"],
    queryFn: () => tenantApi.wecom.list(),
  })
  const groupsQuery = useQuery({
    queryKey: ["external-groups", "list", "all"],
    queryFn: () => tenantApi.external.groupList(),
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
          content: <WidgetsTab slug={slug} />,
        },
        {
          value: "telegram",
          label: t("nav.telegrams"),
          count: telegramsQuery.data?.items.length || undefined,
          content: <TelegramsTab slug={slug} />,
        },
        {
          value: "wecom",
          label: t("nav.wecom_bots"),
          count: wecomQuery.data?.items.length || undefined,
          content: <WeComTab slug={slug} />,
        },
        {
          value: "groups",
          label: t("nav.external_groups"),
          count: pendingGroups || (groupsQuery.data?.length || undefined),
          content: <ExternalGroupsTab />,
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

/** TelegramsTab 管理工作区的 Telegram 机器人渠道列表与新建弹窗。 */
function TelegramsTab({ slug }: { slug: string }): ReactNode {
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<ReturnType<typeof applyServerErrors>>()

  const list = useQuery({
    queryKey: ["resource", "telegram", "list"],
    queryFn: () => tenantApi.telegram.list(),
  })

  const form = useForm<{
    name: string
    audience: "customer" | "internal"
    bot_token: string
    team_id: string | null
    gateway_managed: boolean
    enable_groups: boolean
  }>({
    initialValues: { name: "", audience: "customer", bot_token: "", team_id: null, gateway_managed: false, enable_groups: true },
  })

  const create = useMutation({
    mutationFn: (v: typeof form.values) =>
      tenantApi.telegram.create({
        name: v.name.trim(),
        audience: v.audience,
        bot_token: v.bot_token.trim(),
        team_id: v.team_id || undefined,
        gateway_managed: v.gateway_managed,
        enable_groups: v.enable_groups,
      }),
    onSuccess: (created) => {
      setCreating(false)
      setCreateError(undefined)
      form.reset()
      void queryClient.invalidateQueries({ queryKey: ["resource", "telegram"] })
      void navigate({ to: "/$slug/settings/channels/telegrams/$id", params: { slug, id: created.id } })
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
          {t("telegrams.new")}
        </Button>
      </PageActions>
      <Text size="sm" c="dimmed">
        {t("telegrams.description")}
      </Text>

      <DataTable
        records={list.data?.items ?? []}
        loading={list.isPending}
        empty={t("telegrams.empty")}
        columns={[
          {
            accessor: "name",
            title: t("telegrams.name"),
            render: (row) => (
              <AnchorLink to="/$slug/settings/channels/telegrams/$id" params={{ slug, id: row.id }} size="sm" fw={500}>
                {row.name}
              </AnchorLink>
            ),
          },
          {
            accessor: "audience",
            title: t("channels.audience"),
            render: (row) => (
              <Badge size="xs" variant="light" color={row.audience === "internal" ? "green" : "bright-blue"}>
                {kit.label("ChannelAudience", row.audience)}
              </Badge>
            ),
          },
          {
            accessor: "bot_username",
            title: t("telegrams.bot_username"),
            render: (row) => (row.bot_username ? `@${row.bot_username}` : <Text c="dimmed" size="sm">—</Text>),
          },
          {
            accessor: "team_name",
            title: t("common.team_inbox"),
            render: (row) => row.team_name || <Text c="dimmed" size="sm">{t("telegrams.team_unassigned")}</Text>,
          },
          {
            accessor: "gateway_managed",
            title: t("telegrams.mode"),
            render: (row) => t(row.gateway_managed ? "telegrams.mode_gateway" : "telegrams.mode_direct"),
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
                editLabel={t("telegrams.configure")}
                onEdit={() => void navigate({ to: "/$slug/settings/channels/telegrams/$id", params: { slug, id: row.id } })}
              />
            ),
          },
        ]}
      />

      <Modal opened={creating} onClose={() => setCreating(false)} title={t("telegrams.create_title")}>
        <form onSubmit={form.onSubmit((v) => create.mutate(v))}>
          <Stack gap="md">
            <FormError error={createError} />
            <TextInput label={t("telegrams.name")} required maxLength={60} data-autofocus {...form.getInputProps("name")} />
            <Stack gap={6}>
              <Text size="sm" fw={500}>
                {t("channels.audience")}
              </Text>
              <SegmentedControl
                fullWidth
                value={form.values.audience}
                onChange={(v) => form.setFieldValue("audience", v === "internal" ? "internal" : "customer")}
                data={[
                  { value: "customer", label: t("channels.audience_customer") },
                  { value: "internal", label: t("channels.audience_internal") },
                ]}
              />
              <Text size="xs" c="dimmed">
                {t(form.values.audience === "internal" ? "channels.audience_internal_desc" : "channels.audience_customer_desc")}
              </Text>
            </Stack>
            <PasswordInput
              label={t("telegrams.bot_token")}
              description={t("telegrams.bot_token_hint")}
              required
              autoComplete="off"
              {...form.getInputProps("bot_token")}
            />
            <Switch
              label={t("telegrams.enable_groups")}
              description={t("telegrams.enable_groups_desc")}
              {...form.getInputProps("enable_groups", { type: "checkbox" })}
            />
            <Stack gap={6}>
              <Text size="sm" fw={500}>
                {t("telegrams.mode")}
              </Text>
              <SegmentedControl
                fullWidth
                value={form.values.gateway_managed ? "gateway" : "direct"}
                onChange={(v) => form.setFieldValue("gateway_managed", v === "gateway")}
                data={[
                  { value: "direct", label: t("telegrams.mode_direct") },
                  { value: "gateway", label: t("telegrams.mode_gateway") },
                ]}
              />
              <Text size="xs" c="dimmed">
                {t(form.values.gateway_managed ? "telegrams.mode_gateway_desc" : "telegrams.mode_direct_desc")}
              </Text>
            </Stack>
            <ResourceSelect
              model={teams}
              label={t("telegrams.team_hint")}
              placeholder={t("telegrams.team_unassigned")}
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
                {t("telegrams.create_submit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
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
    audience: "internal" | "customer"
    bot_id: string
    secret: string
    team_id: string | null
  }>({
    initialValues: { name: "", audience: "internal", bot_id: "", secret: "", team_id: null },
  })

  const create = useMutation({
    mutationFn: (v: typeof form.values) =>
      tenantApi.wecom.create({
        name: v.name.trim(),
        audience: v.audience,
        bot_id: v.bot_id.trim(),
        secret: v.secret.trim(),
        team_id: v.team_id || undefined,
      }),
    onSuccess: (created) => {
      setCreating(false)
      setCreateError(undefined)
      form.reset()
      void queryClient.invalidateQueries({ queryKey: ["resource", "wecom"] })
      void navigate({ to: "/$slug/settings/channels/wecom/$id", params: { slug, id: created.id } })
    },
    onError: (err) => setCreateError(applyServerErrors(form, err)),
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
              <AnchorLink to="/$slug/settings/channels/wecom/$id" params={{ slug, id: row.id }} size="sm" fw={500}>
                {row.name}
              </AnchorLink>
            ),
          },
          {
            accessor: "audience",
            title: t("channels.audience"),
            render: (row) => (
              <Badge size="xs" variant="light" color={row.audience === "internal" ? "green" : "bright-blue"}>
                {kit.label("ChannelAudience", row.audience)}
              </Badge>
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
                onEdit={() => void navigate({ to: "/$slug/settings/channels/wecom/$id", params: { slug, id: row.id } })}
              />
            ),
          },
        ]}
      />

      <Modal opened={creating} onClose={() => setCreating(false)} title={t("wecom.create_title")}>
        <form onSubmit={form.onSubmit((v) => create.mutate(v))}>
          <Stack gap="md">
            <FormError error={createError} />
            <TextInput label={t("wecom.name")} required maxLength={60} data-autofocus {...form.getInputProps("name")} />
            <Stack gap={6}>
              <Text size="sm" fw={500}>
                {t("channels.audience")}
              </Text>
              <SegmentedControl
                fullWidth
                value={form.values.audience}
                onChange={(v) => form.setFieldValue("audience", v === "customer" ? "customer" : "internal")}
                data={[
                  { value: "internal", label: t("channels.audience_internal") },
                  { value: "customer", label: t("channels.audience_customer") },
                ]}
              />
              <Text size="xs" c="dimmed">
                {t(form.values.audience === "internal" ? "channels.audience_internal_desc" : "channels.audience_customer_desc")}
              </Text>
            </Stack>
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
            <Group justify="flex-end">
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

function extGroupStatusColor(status: ExtgroupStatus): string {
  switch (status) {
    case "active":
      return "green"
    case "pending_setup":
      return "yellow"
    default:
      return "gray"
  }
}

/** ExternalGroupsTab 管理机器人所在第三方群聊的路由配置（默认团队、语言与启用状态）。 */
function ExternalGroupsTab(): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const [statusFilter, setStatusFilter] = useState<string>("all")
  const [editing, setEditing] = useState<ExtgroupGroupDTO | null>(null)

  const list = useQuery({
    queryKey: ["external-groups", "list", statusFilter],
    queryFn: () => tenantApi.external.groupList(statusFilter === "all" ? {} : { status: statusFilter as ExtgroupStatus }),
  })
  const teamsQuery = useQuery({
    queryKey: ["resource", "team", "all"],
    queryFn: () => tenantApi.team.list({ per_page: 100 }),
  })
  const teamNameById = new Map((teamsQuery.data?.items ?? []).map((tm) => [tm.id, tm.name]))

  const form = useForm<{ name: string; team_id: string | null; status: "pending_setup" | "active"; language: string }>({
    initialValues: { name: "", team_id: null, status: "active", language: "" },
  })

  const openEdit = (g: ExtgroupGroupDTO) => {
    setEditing(g)
    form.setValues({
      name: g.name,
      team_id: g.team_id ?? null,
      status: g.status === "pending_setup" ? "active" : g.status === "active" ? "active" : "pending_setup",
      language: g.language_manual ? g.language : "",
    })
  }

  const save = useMutation({
    mutationFn: (v: typeof form.values) => {
      if (!editing) throw new Error("no editing group")
      return tenantApi.external.groupUpdate(editing.id, {
        name: v.name.trim() || undefined,
        team_id: v.team_id ?? "",
        status: v.status,
        language: v.language,
      })
    },
    onSuccess: () => {
      setEditing(null)
      void queryClient.invalidateQueries({ queryKey: ["external-groups"] })
      notifySuccess(t("common.saved"))
    },
    onError: (err) => notifyError(err),
  })

  return (
    <Stack gap="sm">
      <Group justify="space-between" wrap="wrap" gap="sm">
        <Text size="sm" c="dimmed">
          {t("extgroups.description")}
        </Text>
        <SegmentedControl
          size="xs"
          value={statusFilter}
          onChange={setStatusFilter}
          data={[
            { value: "all", label: t("extgroups.filter_all") },
            { value: "pending_setup", label: t("extgroups.filter_pending") },
            { value: "active", label: t("extgroups.filter_active") },
            { value: "removed", label: t("extgroups.filter_removed") },
          ]}
        />
      </Group>

      <DataTable
        records={list.data ?? []}
        loading={list.isPending}
        empty={t("extgroups.empty")}
        columns={[
          {
            accessor: "name",
            title: t("extgroups.name"),
            render: (row) => (
              <Stack gap={2}>
                <Text size="sm" fw={500}>
                  {row.name || row.external_id}
                </Text>
                <Text size="xs" c="dimmed">
                  {row.external_id}
                </Text>
              </Stack>
            ),
          },
          {
            accessor: "channel",
            title: t("extgroups.channel"),
            render: (row) => (
              <Group gap={6} wrap="nowrap">
                <ChannelIcon kind={row.channel.kind} size={14} />
                <Text size="sm">{row.channel.name}</Text>
              </Group>
            ),
          },
          {
            accessor: "team_id",
            title: t("common.team_inbox"),
            render: (row) => (row.team_id ? teamNameById.get(row.team_id) ?? row.team_id : <Text c="dimmed" size="sm">{t("extgroups.team_inherit")}</Text>),
          },
          {
            accessor: "status",
            title: t("common.status"),
            render: (row) => <StatusDot color={extGroupStatusColor(row.status)}>{kit.label("ExtgroupStatus", row.status)}</StatusDot>,
          },
          {
            accessor: "last_active_at",
            title: t("extgroups.last_active"),
            render: (row) => (
              <Text size="xs" c="dimmed">
                {row.last_active_at ? activity(row.last_active_at, ui, t, zone) : "—"}
              </Text>
            ),
          },
          {
            accessor: "actions",
            title: "",
            textAlign: "right",
            render: (row) => (
              <RowActions
                editLabel={t("extgroups.configure")}
                onEdit={() => openEdit(row)}
              />
            ),
          },
        ]}
      />

      <Modal opened={Boolean(editing)} onClose={() => setEditing(null)} title={t("extgroups.edit_title")}>
        <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
          <Stack gap="md">
            <TextInput label={t("extgroups.name")} maxLength={120} {...form.getInputProps("name")} />
            <ResourceSelect
              model={teams}
              label={t("extgroups.team_label")}
              placeholder={t("extgroups.team_inherit")}
              filterRow={(row) => row.status === "active" || String(row.id) === String(form.values.team_id ?? "")}
              getOption={(row) => ({ value: String(row.id), label: String(row.name ?? "") })}
              value={form.values.team_id}
              onChange={(v) => form.setFieldValue("team_id", v)}
              clearable
            />
            {editing?.status !== "removed" && (
              <Stack gap={6}>
                <Text size="sm" fw={500}>
                  {t("common.status")}
                </Text>
                <SegmentedControl
                  fullWidth
                  value={form.values.status}
                  onChange={(v) => form.setFieldValue("status", v === "pending_setup" ? "pending_setup" : "active")}
                  data={[
                    { value: "active", label: t("extgroups.filter_active") },
                    { value: "pending_setup", label: t("extgroups.filter_pending") },
                  ]}
                />
              </Stack>
            )}
            <Stack gap={6}>
              <Text size="sm" fw={500}>
                {t("inbox.contact_language")}
              </Text>
              <LanguageSelect
                before={[{ value: "auto", label: t("inbox.language_auto") }]}
                value={form.values.language || "auto"}
                onChange={(v) => form.setFieldValue("language", !v || v === "auto" ? "" : v)}
                allowDeselect={false}
              />
            </Stack>
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setEditing(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={save.isPending}>
                {t("common.save")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  )
}
