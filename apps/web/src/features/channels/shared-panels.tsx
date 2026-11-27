import type { ExtgroupGroupDTO, ExtgroupStatus } from "@agents-desk/client/tenant"
import { Badge, Button, Group, Modal, Paper, PasswordInput, SegmentedControl, Stack, Switch, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus, IconSparkles } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  AnchorLink,
  applyServerErrors,
  DataTable,
  FormError,
  notifyError,
  notifySuccess,
  PageActions,
  ResourceSelect,
  StatusDot,
  useLanguage,
  useT,
  useTimeZone,
} from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../api"
import { AiReceptionCard } from "../../components/ai-placeholders"
import { RowActions } from "../../components/row-actions"
import { ChannelIcon } from "../../inbox/channel-badge"
import { activity } from "../../inbox/format"
import { LanguageSelect } from "../../LanguageSelect"
import { teams } from "../../resources"

/** BotAudience 是公共服务机器人的两个业务场景域：对外客户服务或内部员工服务。 */
export type BotAudience = "customer" | "internal"

/** telegramDetailPath 返回指定场景下 Telegram 机器人配置工作台的路由路径。 */
function telegramDetailPath(audience: BotAudience): "/$slug/settings/channels/telegrams/$id" | "/$slug/settings/internal-channels/telegrams/$id" {
  return audience === "internal" ? "/$slug/settings/internal-channels/telegrams/$id" : "/$slug/settings/channels/telegrams/$id"
}

/** TelegramsPanel 按业务场景（对外客服 / 内部员工服务）管理对应的 Telegram 机器人渠道列表与新建弹窗。 */
export function TelegramsPanel({ slug, audience }: { slug: string; audience: BotAudience }): ReactNode {
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<ReturnType<typeof applyServerErrors>>()

  const list = useQuery({
    queryKey: ["resource", "telegram", "list", audience],
    queryFn: () => tenantApi.telegram.list({ audience }),
  })

  const form = useForm<{
    name: string
    bot_token: string
    team_id: string | null
    gateway_managed: boolean
    enable_groups: boolean
  }>({
    initialValues: { name: "", bot_token: "", team_id: null, gateway_managed: false, enable_groups: true },
  })

  const create = useMutation({
    mutationFn: (v: typeof form.values) =>
      tenantApi.telegram.create({
        name: v.name.trim(),
        audience,
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
      void navigate({ to: telegramDetailPath(audience), params: { slug, id: created.id } })
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
        {t(audience === "internal" ? "telegrams.description_internal" : "telegrams.description")}
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
              <AnchorLink to={telegramDetailPath(audience)} params={{ slug, id: row.id }} size="sm" fw={500}>
                {row.name}
              </AnchorLink>
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
                onEdit={() => void navigate({ to: telegramDetailPath(audience), params: { slug, id: row.id } })}
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

/** ExternalGroupsPanel 按场景列出并配置对应渠道加入的外部群（默认团队、语言、状态与 AI 接待占位）。 */
export function ExternalGroupsPanel({ audience }: { audience: BotAudience }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const [statusFilter, setStatusFilter] = useState<string>("all")
  const [editing, setEditing] = useState<ExtgroupGroupDTO | null>(null)

  const list = useQuery({
    queryKey: ["external-groups", "list", audience, statusFilter],
    queryFn: () =>
      tenantApi.external.groupList({
        audience,
        status: statusFilter === "all" ? undefined : (statusFilter as ExtgroupStatus),
      }),
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
          {t(audience === "internal" ? "extgroups.description_internal" : "extgroups.description_customer")}
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
            render: (row) =>
              row.team_id ? teamNameById.get(row.team_id) ?? row.team_id : <Text c="dimmed" size="sm">{t("extgroups.team_inherit")}</Text>,
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
            render: (row) => <RowActions editLabel={t("extgroups.configure")} onEdit={() => openEdit(row)} />,
          },
        ]}
      />

      <Modal opened={Boolean(editing)} onClose={() => setEditing(null)} title={t("extgroups.edit_title")} size="md">
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
            <AiReceptionCard scene="group" />
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

/** UpcomingChannelsBanner 展示当前业务场景下规划中的后续渠道接入占位。 */
export function UpcomingChannelsBanner({ audience }: { audience: BotAudience }): ReactNode {
  const t = useT()
  const items =
    audience === "internal"
      ? [t("channels.upcoming_dingtalk"), t("channels.upcoming_feishu"), t("channels.upcoming_slack")]
      : [t("channels.upcoming_whatsapp"), t("channels.upcoming_wechat_mp"), t("channels.upcoming_email")]

  return (
    <Paper withBorder p="sm" radius="md" style={{ borderStyle: "dashed" }}>
      <Group justify="space-between" wrap="wrap" gap="xs">
        <Group gap="xs" wrap="nowrap">
          <IconSparkles size={15} style={{ flex: "none", color: "var(--tn-muted)" }} />
          <Text size="xs" c="dimmed">
            {t("channels.upcoming_prefix")}
          </Text>
        </Group>
        <Group gap={6} wrap="wrap">
          {items.map((name) => (
            <Badge key={name} size="xs" variant="light" color="gray">
              {name}
            </Badge>
          ))}
        </Group>
      </Group>
    </Paper>
  )
}
