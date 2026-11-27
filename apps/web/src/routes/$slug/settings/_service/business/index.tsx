import type { BusinessChannelItemDTO, BusinessPolicy, TgaccountCredentials } from "@agents-desk/client/tenant"
import {
  Alert,
  Avatar,
  Badge,
  Box,
  Button,
  Checkbox,
  Group,
  Modal,
  MultiSelect,
  NumberInput,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconInfoCircle, IconPlayerPause, IconPlayerPlay, IconPlugConnected, IconSearch, IconUserPlus } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import {
  applyServerErrors,
  DataTable,
  FormError,
  notifyError,
  notifySuccess,
  Page,
  type PageTab,
  preloadQueries,
  SecretInput,
  StatusDot,
  useCan,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { RowActions } from "../../../../../components/row-actions"
import { useUnsavedGuard } from "../../../../../components/unsaved-guard"
import { ExternalGroupsPanel, TelegramsPanel } from "../../../../../features/channels/shared-panels"
import { ChannelIcon } from "../../../../../inbox/channel-badge"
import { useMembers } from "../../../../../inbox/data"

// 工作区商务渠道治理页：管理员总览全员个人商务渠道、配置面向合作方的 Telegram 机器人与合作群（AI Agent + 人工协作）、管理策略配额与 Telegram 个人号 API 凭据。
export const Route = createFileRoute("/$slug/settings/_service/business/")({
  validateSearch: (search: Record<string, unknown>): { tab?: string } => ({
    tab: typeof search.tab === "string" ? search.tab : undefined,
  }),
  loader: ({ context }) =>
    preloadQueries(context.app.queryClient, [
      kit.queries.business.channelList({}),
      kit.queries.business.policyGet(),
      kit.queries.tgaccount.credentialsGet(),
    ]),
  component: BusinessGovernancePage,
})

function BusinessGovernancePage(): ReactNode {
  const { slug } = Route.useParams()
  const t = useT()
  const can = useCan()
  const canManageChannels = can("channels:manage")
  const channelsQuery = useQuery(kit.queries.business.channelList({}))
  const partnerBotsQuery = useQuery({
    queryKey: ["resource", "telegram", "list", "partner"],
    queryFn: () => tenantApi.telegram.list({ audience: "partner" }),
    enabled: canManageChannels,
  })
  const partnerGroupsQuery = useQuery({
    queryKey: ["external-groups", "list", "partner", "all"],
    queryFn: () => tenantApi.external.groupList({ audience: "partner" }),
    enabled: canManageChannels,
  })
  const totalCount = channelsQuery.data?.length ?? 0
  const pendingGroups = (partnerGroupsQuery.data ?? []).filter((g) => g.status === "pending_setup").length

  const tabs: PageTab[] = [
    {
      value: "overview",
      label: t("business_gov.tab_overview"),
      count: totalCount || undefined,
      content: <MemberChannelsTab />,
    },
    ...(canManageChannels
      ? [
          {
            value: "telegram_bots",
            label: t("nav.telegrams"),
            count: partnerBotsQuery.data?.items.length || undefined,
            content: <TelegramsPanel slug={slug} audience="partner" />,
          },
          {
            value: "groups",
            label: t("nav.partner_groups"),
            count: pendingGroups || partnerGroupsQuery.data?.length || undefined,
            content: <ExternalGroupsPanel audience="partner" />,
          },
        ]
      : []),
    {
      value: "policy",
      label: t("business_gov.tab_policy"),
      content: <BusinessPolicyTab />,
    },
    {
      value: "telegram",
      label: t("business_gov.tab_telegram"),
      content: <TelegramCredentialsTab />,
    },
  ]

  return <Page title={t("nav.business_governance")} description={t("business_gov.description")} tabs={tabs} />
}

/** MemberChannelsTab 展示工作区全体成员的个人商务渠道，支持筛选、强制启停和应急添加协作成员。 */
function MemberChannelsTab(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [kind, setKind] = useState<"" | "business_link" | "telegram_account">("")
  const [enabledFilter, setEnabledFilter] = useState<"" | "true" | "false">("")
  const [q, setQ] = useState("")
  const [collabTarget, setCollabTarget] = useState<BusinessChannelItemDTO | null>(null)

  const list = useQuery(
    kit.queries.business.channelList({
      kind: kind || undefined,
      enabled: enabledFilter || undefined,
      q: q.trim() || undefined,
    }),
  )
  const items = list.data ?? []

  const toggleEnabled = useMutation({
    mutationFn: (item: BusinessChannelItemDTO) => tenantApi.business.channelSetEnabled(item.id, { enabled: !item.enabled }),
    onSuccess: (updated) => {
      notifySuccess(t(updated.enabled ? "common.enabled" : "common.disabled"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.business.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.tgaccount.key() })
    },
    onError: (err) => notifyError(err),
  })

  return (
    <Stack gap="md">
      <Group gap="xs" wrap="wrap" align="center">
        <TextInput
          style={{ flex: 1, minWidth: 220 }}
          placeholder={t("business_gov.search_placeholder")}
          leftSection={<IconSearch size={14} />}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
        />
        <SegmentedControl
          value={kind}
          onChange={(v) => setKind(v as "" | "business_link" | "telegram_account")}
          data={[
            { value: "", label: t("business_gov.filter_all_kinds") },
            { value: "business_link", label: kit.label("ChannelKind", "business_link") },
            { value: "telegram_account", label: kit.label("ChannelKind", "telegram_account") },
          ]}
        />
        <Select
          size="sm"
          w={130}
          value={enabledFilter}
          onChange={(v) => setEnabledFilter((v ?? "") as "" | "true" | "false")}
          data={[
            { value: "", label: t("business_gov.filter_all_status") },
            { value: "true", label: t("common.enabled") },
            { value: "false", label: t("common.disabled") },
          ]}
        />
      </Group>

      <DataTable
        records={items}
        loading={list.isPending}
        empty={t("business_gov.empty")}
        columns={[
          {
            accessor: "owner",
            title: t("business_gov.col_owner"),
            render: (item) => (
              <Group gap="xs" wrap="nowrap">
                <Avatar size={28} radius="xl" name={item.owner.name} color="initials" src={item.owner.avatar_url || undefined} />
                <Box style={{ minWidth: 0 }}>
                  <Text size="sm" fw={500} truncate>
                    {item.owner.name || item.owner.id}
                  </Text>
                  {item.owner.title && (
                    <Text size="xs" c="dimmed" truncate>
                      {item.owner.title}
                    </Text>
                  )}
                </Box>
              </Group>
            ),
          },
          {
            accessor: "name",
            title: t("business_gov.col_channel"),
            render: (item) => (
              <Group gap="xs" wrap="nowrap">
                <ChannelIcon kind={item.kind} size={16} />
                <Box style={{ minWidth: 0 }}>
                  <Text size="sm" fw={500} truncate>
                    {item.name}
                  </Text>
                  <Text size="xs" c="dimmed" truncate>
                    {kit.label("ChannelKind", item.kind)}
                  </Text>
                </Box>
              </Group>
            ),
          },
          {
            accessor: "detail",
            title: t("business_gov.col_detail"),
            render: (item) => {
              if (item.kind === "business_link") {
                return (
                  <Box style={{ minWidth: 0 }}>
                    {item.public_code && (
                      <Badge size="xs" variant="light" color="gray" style={{ textTransform: "none" }}>
                        /c/{item.public_code}
                      </Badge>
                    )}
                    {item.chat_url && (
                      <Text size="xs" c="dimmed" ff="monospace" truncate maw={260}>
                        {item.chat_url}
                      </Text>
                    )}
                  </Box>
                )
              }
              const [dotColor, statusText] =
                item.status === "connected"
                  ? (["green", t("business_channels.tg_status_connected")] as const)
                  : item.status === "auth_expired"
                    ? (["red", t("business_channels.tg_status_expired")] as const)
                    : (["gray", t("business_channels.tg_status_disconnected")] as const)
              return (
                <Stack gap={2}>
                  <Group gap="xs" wrap="nowrap">
                    <StatusDot color={dotColor}>{statusText}</StatusDot>
                    <Text size="xs" c="dimmed">
                      {t("business_gov.peers_count", { n: item.synced_peer_count })}
                    </Text>
                  </Group>
                  {item.identity_label && (
                    <Text size="xs" c="dimmed" truncate maw={240}>
                      {item.identity_label}
                    </Text>
                  )}
                  {item.last_error && (
                    <Text size="xs" c="red" truncate maw={240}>
                      {item.last_error}
                    </Text>
                  )}
                </Stack>
              )
            },
          },
          {
            accessor: "conversation_count",
            title: t("business_gov.col_conversations"),
            render: (item) => (
              <Text size="sm" style={{ fontVariantNumeric: "tabular-nums" }}>
                {item.conversation_count}
              </Text>
            ),
          },
          {
            accessor: "business",
            title: t("business_gov.col_collaborators"),
            render: (item) => {
              const count = item.business.default_collaborator_ids?.length ?? 0
              return (
                <Text size="xs" c={count > 0 ? undefined : "dimmed"}>
                  {count > 0 ? t("business_gov.collaborators_count", { n: count }) : "—"}
                </Text>
              )
            },
          },
          {
            accessor: "enabled",
            title: t("common.status"),
            render: (item) => <StatusDot color={item.enabled ? "green" : "gray"}>{t(item.enabled ? "common.enabled" : "common.disabled")}</StatusDot>,
          },
          {
            accessor: "actions",
            title: "",
            textAlign: "right",
            render: (item) => (
              <RowActions
                actions={[
                  {
                    label: t("business_gov.action_collaborators"),
                    icon: <IconUserPlus size={14} />,
                    onClick: () => setCollabTarget(item),
                  },
                  {
                    label: t(item.enabled ? "common.disable" : "common.enable"),
                    icon: item.enabled ? <IconPlayerPause size={14} /> : <IconPlayerPlay size={14} />,
                    disabled: toggleEnabled.isPending,
                    onClick: () => toggleEnabled.mutate(item),
                  },
                ]}
              />
            ),
          },
        ]}
      />

      <AddCollaboratorsModal
        target={collabTarget}
        onClose={() => setCollabTarget(null)}
        onDone={() => {
          setCollabTarget(null)
          void queryClient.invalidateQueries({ queryKey: kit.queries.business.key() })
          void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
        }}
      />
    </Stack>
  )
}

/** AddCollaboratorsModal 允许管理员应急将同事加入指定商务渠道的现有会话与默认协作名单。 */
function AddCollaboratorsModal({
  target,
  onClose,
  onDone,
}: {
  target: BusinessChannelItemDTO | null
  onClose: () => void
  onDone: () => void
}): ReactNode {
  const t = useT()
  const { list: members } = useMembers()
  const [memberIds, setMemberIds] = useState<string[]>([])
  const [applyExisting, setApplyExisting] = useState(true)
  const [addDefault, setAddDefault] = useState(true)

  useEffect(() => {
    if (!target) return
    setMemberIds([])
    setApplyExisting(true)
    setAddDefault(true)
  }, [target])

  const options = members
    .filter((m) => m.id !== target?.owner.id)
    .map((m) => ({ value: m.id, label: m.name }))

  const submit = useMutation({
    mutationFn: () =>
      tenantApi.business.channelAddCollaborators(target!.id, {
        member_ids: memberIds,
        apply_to_existing: applyExisting,
        add_to_default: addDefault,
      }),
    onSuccess: (res) => {
      if (applyExisting) {
        notifySuccess(t("business_gov.collab_success", { chats: res.updated_chats }))
      } else {
        notifySuccess(t("business_gov.collab_success_default_only"))
      }
      onDone()
    },
    onError: (err) => notifyError(err),
  })

  return (
    <Modal
      opened={target !== null}
      onClose={onClose}
      title={target ? t("business_gov.collab_modal_title", { name: target.name }) : ""}
    >
      <Stack gap="md">
        <Text size="xs" c="dimmed">
          {t("business_gov.collab_modal_hint")}
        </Text>
        <MultiSelect
          label={t("business_gov.collab_members_label")}
          placeholder={t("business_gov.collab_members_placeholder")}
          data={options}
          value={memberIds}
          onChange={setMemberIds}
          searchable
          clearable
        />
        <Checkbox
          label={t("business_gov.collab_apply_existing")}
          checked={applyExisting}
          onChange={(e) => setApplyExisting(e.currentTarget.checked)}
        />
        <Checkbox
          label={t("business_gov.collab_add_default")}
          checked={addDefault}
          onChange={(e) => setAddDefault(e.currentTarget.checked)}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={memberIds.length === 0 || (!applyExisting && !addDefault)}
            loading={submit.isPending}
            onClick={() => submit.mutate()}
          >
            {t("business_gov.collab_submit")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

/** BusinessPolicyTab 管理工作区级的商务渠道功能开关、配额与默认接待基线。 */
function BusinessPolicyTab(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const policyQuery = useQuery(kit.queries.business.policyGet())
  const [serverError, setServerError] = useState<ReturnType<typeof applyServerErrors>>()

  const form = useForm<BusinessPolicy>({
    initialValues: {
      links_enabled: true,
      telegram_accounts_enabled: true,
      max_links_per_member: 10,
      max_telegram_accounts_per_member: 5,
      default_require_intake: false,
      default_company_name: "",
      default_translation_tone: "formal",
    },
  })

  useEffect(() => {
    const data = policyQuery.data
    if (!data) return
    const values: BusinessPolicy = {
      links_enabled: data.links_enabled,
      telegram_accounts_enabled: data.telegram_accounts_enabled,
      max_links_per_member: data.max_links_per_member,
      max_telegram_accounts_per_member: data.max_telegram_accounts_per_member,
      default_require_intake: data.default_require_intake,
      default_company_name: data.default_company_name,
      default_translation_tone: data.default_translation_tone,
    }
    form.setInitialValues(values)
    form.setValues(values)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅在服务端策略加载后同步表单基准
  }, [policyQuery.data])

  const save = useMutation({
    mutationFn: (values: BusinessPolicy) =>
      tenantApi.business.policySave({
        ...values,
        max_links_per_member: Number(values.max_links_per_member) || 0,
        max_telegram_accounts_per_member: Number(values.max_telegram_accounts_per_member) || 0,
      }),
    onSuccess: (saved) => {
      setServerError(undefined)
      queryClient.setQueryData(kit.queries.business.policyGet().queryKey, saved)
      const next: BusinessPolicy = {
        links_enabled: saved.links_enabled,
        telegram_accounts_enabled: saved.telegram_accounts_enabled,
        max_links_per_member: saved.max_links_per_member,
        max_telegram_accounts_per_member: saved.max_telegram_accounts_per_member,
        default_require_intake: saved.default_require_intake,
        default_company_name: saved.default_company_name,
        default_translation_tone: saved.default_translation_tone,
      }
      form.setInitialValues(next)
      form.resetDirty(next)
      notifySuccess(t("common.saved"))
    },
    onError: (err) => setServerError(applyServerErrors(form, err)),
  })

  useUnsavedGuard(form.isDirty())
  const data = policyQuery.data

  return (
    <form noValidate onSubmit={form.onSubmit((v) => save.mutate(v))}>
      <Stack gap="md">
        <FormError error={serverError} />

        {data && !data.platform_links_enabled && (
          <Alert icon={<IconInfoCircle size={16} />} color="yellow" variant="light">
            <Text size="sm">{t("business_gov.platform_links_off")}</Text>
          </Alert>
        )}
        {data && !data.platform_telegram_enabled && (
          <Alert icon={<IconInfoCircle size={16} />} color="yellow" variant="light">
            <Text size="sm">{t("business_gov.platform_tg_off")}</Text>
          </Alert>
        )}

        <Paper withBorder p="md" radius="md">
          <Stack gap="md">
            <Box>
              <Title order={5}>{t("business_gov.policy_section_access")}</Title>
              <Text size="xs" c="dimmed" mt={2}>
                {t("business_gov.policy_section_access_desc")}
              </Text>
            </Box>
            <Switch
              label={t("business_gov.policy_links_enabled")}
              description={t("business_gov.policy_links_enabled_hint")}
              {...form.getInputProps("links_enabled", { type: "checkbox" })}
            />
            <NumberInput
              label={t("business_gov.policy_max_links")}
              description={t("business_gov.policy_max_links_hint")}
              min={0}
              max={100}
              allowDecimal={false}
              allowNegative={false}
              {...form.getInputProps("max_links_per_member")}
            />
            <Switch
              label={t("business_gov.policy_tg_enabled")}
              description={t("business_gov.policy_tg_enabled_hint")}
              {...form.getInputProps("telegram_accounts_enabled", { type: "checkbox" })}
            />
            <NumberInput
              label={t("business_gov.policy_max_tg")}
              description={t("business_gov.policy_max_tg_hint")}
              min={0}
              max={20}
              allowDecimal={false}
              allowNegative={false}
              {...form.getInputProps("max_telegram_accounts_per_member")}
            />
          </Stack>
        </Paper>

        <Paper withBorder p="md" radius="md">
          <Stack gap="md">
            <Box>
              <Title order={5}>{t("business_gov.policy_section_defaults")}</Title>
              <Text size="xs" c="dimmed" mt={2}>
                {t("business_gov.policy_section_defaults_desc")}
              </Text>
            </Box>
            <TextInput
              label={t("business_gov.policy_company_name")}
              placeholder={t("business_gov.policy_company_name_placeholder")}
              maxLength={80}
              {...form.getInputProps("default_company_name")}
            />
            <Switch
              label={t("business_gov.policy_require_intake")}
              description={t("business_gov.policy_require_intake_hint")}
              {...form.getInputProps("default_require_intake", { type: "checkbox" })}
            />
            <Select
              label={t("business_gov.policy_translation_tone")}
              description={t("business_gov.policy_translation_tone_hint")}
              data={[
                { value: "", label: t("business_gov.tone_inherit") },
                { value: "formal", label: kit.label("TranslationTone", "formal") },
                { value: "neutral", label: kit.label("TranslationTone", "neutral") },
                { value: "friendly", label: kit.label("TranslationTone", "friendly") },
              ]}
              value={form.values.default_translation_tone}
              onChange={(v) => form.setFieldValue("default_translation_tone", (v ?? "") as BusinessPolicy["default_translation_tone"])}
            />
          </Stack>
        </Paper>

        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty() || !data} loading={save.isPending}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}

/** TelegramCredentialsTab 管理本工作区独立的 Telegram MTProto 应用凭据（API ID、API Hash）与可选 SOCKS5 代理。 */
function TelegramCredentialsTab(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const credsQuery = useQuery(kit.queries.tgaccount.credentialsGet())
  const policyQuery = useQuery(kit.queries.business.policyGet())
  const [serverError, setServerError] = useState<ReturnType<typeof applyServerErrors>>()

  const form = useForm<{ api_id: number | string; api_hash: string; proxy_url: string }>({
    initialValues: {
      api_id: "",
      api_hash: "",
      proxy_url: "",
    },
  })

  useEffect(() => {
    const data = credsQuery.data
    if (!data) return
    const values = {
      api_id: data.api_id > 0 ? data.api_id : "",
      api_hash: data.api_hash,
      proxy_url: data.proxy_url,
    }
    form.setInitialValues(values)
    form.setValues(values)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅在服务端凭据加载后同步表单基准
  }, [credsQuery.data])

  const save = useMutation({
    mutationFn: (values: { api_id: number | string; api_hash: string; proxy_url: string }) => {
      const payload: TgaccountCredentials = {
        api_id: Number(values.api_id) || 0,
        api_hash: values.api_hash.trim(),
        proxy_url: values.proxy_url.trim(),
      }
      return tenantApi.tgaccount.credentialsUpdate(payload)
    },
    onSuccess: (saved) => {
      setServerError(undefined)
      queryClient.setQueryData(kit.queries.tgaccount.credentialsGet().queryKey, saved)
      void queryClient.invalidateQueries({ queryKey: kit.queries.tgaccount.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.business.policyGet().queryKey })
      const next = {
        api_id: saved.api_id > 0 ? saved.api_id : "",
        api_hash: saved.api_hash,
        proxy_url: saved.proxy_url,
      }
      form.setInitialValues(next)
      form.resetDirty(next)
      notifySuccess(t("common.saved"))
    },
    onError: (err) => setServerError(applyServerErrors(form, err)),
  })

  const testConn = useMutation({
    mutationFn: () => tenantApi.tgaccount.credentialsTest({ proxy_url: form.values.proxy_url.trim() }),
    onSuccess: (res) => {
      if (res.ok) {
        notifySuccess(t("business_gov.tg_test_ok", { ms: res.latency_ms }))
      } else {
        notifyError(undefined, t("business_gov.tg_test_failed", { error: res.error ?? "" }))
      }
    },
    onError: (err) => notifyError(err),
  })

  useUnsavedGuard(form.isDirty())
  const data = credsQuery.data
  const configured = Boolean(data && data.api_id > 0 && data.api_hash.trim() !== "")
  const platformOff = policyQuery.data ? !policyQuery.data.platform_telegram_enabled : false

  return (
    <form noValidate onSubmit={form.onSubmit((v) => save.mutate(v))}>
      <Stack gap="md">
        <FormError error={serverError} />

        {platformOff && (
          <Alert icon={<IconInfoCircle size={16} />} color="yellow" variant="light">
            <Text size="sm">{t("business_gov.platform_tg_off")}</Text>
          </Alert>
        )}

        <Alert icon={<IconInfoCircle size={16} />} color="bright-blue" variant="light" title={t("business_gov.tg_isolation_title")}>
          <Text size="sm">{t("business_gov.tg_isolation_desc")}</Text>
        </Alert>

        <Paper withBorder p="md" radius="md">
          <Stack gap="md">
            <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
              <Box>
                <Title order={5}>{t("business_gov.tg_section_credentials")}</Title>
                <Text size="xs" c="dimmed" mt={2}>
                  {t("business_gov.tg_section_credentials_desc")}
                </Text>
              </Box>
              {data && (
                <StatusDot color={configured ? "green" : "gray"}>
                  {t(configured ? "business_gov.tg_status_configured" : "business_gov.tg_status_unconfigured")}
                </StatusDot>
              )}
            </Group>

            <NumberInput
              label={t("business_gov.tg_api_id")}
              description={t("business_gov.tg_api_id_hint")}
              placeholder={t("business_gov.tg_api_id_placeholder")}
              min={0}
              allowDecimal={false}
              allowNegative={false}
              hideControls
              {...form.getInputProps("api_id")}
            />

            <SecretInput
              label={t("business_gov.tg_api_hash")}
              description={t("business_gov.tg_api_hash_hint")}
              placeholder={t("business_gov.tg_api_hash_placeholder")}
              {...form.getInputProps("api_hash")}
            />

            <Stack gap="xs">
              <TextInput
                label={t("business_gov.tg_proxy_url")}
                description={t("business_gov.tg_proxy_url_hint")}
                placeholder={t("business_gov.tg_proxy_placeholder")}
                {...form.getInputProps("proxy_url")}
              />
              <Group gap="xs">
                <Button
                  type="button"
                  variant="default"
                  size="xs"
                  leftSection={<IconPlugConnected size={14} />}
                  loading={testConn.isPending}
                  onClick={() => testConn.mutate()}
                >
                  {t("business_gov.tg_test")}
                </Button>
                <Text size="xs" c="dimmed">
                  {t("business_gov.tg_reconnect_hint")}
                </Text>
              </Group>
            </Stack>
          </Stack>
        </Paper>

        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty() || !data} loading={save.isPending}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
