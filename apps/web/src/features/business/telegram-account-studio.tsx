import type {
  ChannelBusinessPolicy,
  TgaccountAccountDTO,
  TgaccountSyncConfig,
  TgaccountSyncedPeer,
} from "@agents-desk/client/tenant"
import { fieldErrors } from "@go-tenon/client"
import {
  Alert,
  Badge,
  Box,
  Button,
  Checkbox,
  Divider,
  Group,
  MultiSelect,
  Paper,
  SegmentedControl,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Switch,
  Text,
  TextInput,
  ThemeIcon,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import {
  IconFolder,
  IconKey,
  IconPlayerPause,
  IconPlayerPlay,
  IconPlus,
  IconRefresh,
  IconTrash,
  IconUser,
  IconUsersGroup,
} from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useSearch } from "@tanstack/react-router"
import {
  applyServerErrors,
  confirm,
  DataTable,
  FormError,
  FormSection,
  notifyError,
  notifySuccess,
  Page,
  PageActions,
  RowActions,
  StatusDot,
  useLanguage,
  useT,
  useTimeZone,
} from "@go-tenon/ui"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { kit, tenantApi } from "../../api"
import { useUnsavedGuard } from "../../components/unsaved-guard"
import { useMe, useMembers } from "../../inbox/data"
import { fullDateTime } from "../../inbox/format"
import { SyncedPeersModal, TelegramLoginModal } from "./telegram-modals"

interface TelegramAccountFormValues {
  name: string
  enabled: boolean
  sync: {
    mode: "whitelist" | "folders"
    folder_ids: number[]
    backfill_limit: string
    sync_outgoing: boolean
    sync_media: boolean
    group_mode: "all" | "mentions_only"
  }
  business: {
    default_collaborator_ids: string[]
    default_tag_ids: string[]
    translation_tone: "" | "friendly" | "neutral" | "formal"
    auto_translate: "inherit" | "on" | "off"
  }
}

function toFormValues(acc: TgaccountAccountDTO): TelegramAccountFormValues {
  const autoTranslate =
    acc.business.auto_translate === true ? "on" : acc.business.auto_translate === false ? "off" : "inherit"
  const backfill = acc.sync.backfill_limit === 0 ? "0" : acc.sync.backfill_limit >= 50 ? "50" : "20"
  return {
    name: acc.name,
    enabled: acc.enabled,
    sync: {
      mode: acc.sync.mode === "folders" ? "folders" : "whitelist",
      folder_ids: acc.sync.folder_ids ?? [],
      backfill_limit: backfill,
      sync_outgoing: acc.sync.sync_outgoing,
      sync_media: acc.sync.sync_media,
      group_mode: acc.sync.group_mode === "mentions_only" ? "mentions_only" : "all",
    },
    business: {
      default_collaborator_ids: acc.business.default_collaborator_ids ?? [],
      default_tag_ids: acc.business.default_tag_ids ?? [],
      translation_tone: (acc.business.translation_tone ?? "") as "" | "friendly" | "neutral" | "formal",
      auto_translate: autoTranslate,
    },
  }
}

/**
 * TelegramAccountStudio 是成员单个 Telegram 个人号商务渠道的统一配置工作台：
 * 分为「基本与同步策略」（健康诊断、重连、原地重新授权、文件夹/白名单模式、群聊降噪、媒体同步与通用商务策略）与「同步会话管理」（单会话暂停/恢复、手动回填、移出）。
 */
export function TelegramAccountStudio({ slug, id }: { slug: string; id: string }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const tab = (useSearch({ strict: false }) as { tab?: string }).tab || "config"

  const accountQuery = useQuery(kit.queries.tgaccount.get(id))
  const listQuery = useQuery(kit.queries.tgaccount.list())
  const me = useMe()
  const members = useMembers().list
  const tagsQuery = useQuery(kit.queries.tag.list({ per_page: 100 }))

  const account = accountQuery.data
  const platformConfigured = listQuery.data?.configured ?? true

  const [serverError, setServerError] = useState<ReturnType<typeof applyServerErrors>>()
  const [reauthOpen, setReauthOpen] = useState(false)
  const [peersModalOpen, setPeersModalOpen] = useState(false)
  const [backfillingPeer, setBackfillingPeer] = useState<string | null>(null)

  const form = useForm<TelegramAccountFormValues>({
    initialValues: {
      name: "",
      enabled: true,
      sync: {
        mode: "whitelist",
        folder_ids: [],
        backfill_limit: "20",
        sync_outgoing: true,
        sync_media: true,
        group_mode: "all",
      },
      business: {
        default_collaborator_ids: [],
        default_tag_ids: [],
        translation_tone: "",
        auto_translate: "inherit",
      },
    },
  })

  const [syncedKey, setSyncedKey] = useState("")
  useEffect(() => {
    if (!account) return
    const key = `${account.id}:${account.updated_at}`
    if (key === syncedKey) return
    const next = toFormValues(account)
    form.setValues(next)
    form.resetDirty(next)
    setSyncedKey(key)
  }, [account, form, syncedKey])

  const dirty = form.isDirty()
  useUnsavedGuard(dirty)

  const foldersQuery = useQuery({
    ...kit.queries.tgaccount.folders(id),
    enabled: Boolean(account && account.status !== "auth_expired" && form.values.sync.mode === "folders"),
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.tgaccount.key() })
    void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
  }

  const save = useMutation({
    mutationFn: (v: TelegramAccountFormValues) => {
      const sync: TgaccountSyncConfig = {
        mode: v.sync.mode,
        folder_ids: v.sync.folder_ids,
        backfill_limit: Number(v.sync.backfill_limit) || 0,
        sync_outgoing: v.sync.sync_outgoing,
        sync_media: v.sync.sync_media,
        group_mode: v.sync.group_mode,
      }
      const business: ChannelBusinessPolicy = {
        default_collaborator_ids: v.business.default_collaborator_ids,
        default_tag_ids: v.business.default_tag_ids,
        translation_tone: v.business.translation_tone || undefined,
        auto_translate:
          v.business.auto_translate === "on" ? true : v.business.auto_translate === "off" ? false : undefined,
      }
      return tenantApi.tgaccount.update(id, {
        name: v.name.trim(),
        enabled: v.enabled,
        sync,
        business,
      })
    },
    onSuccess: (updated) => {
      setServerError(undefined)
      queryClient.setQueryData(kit.queries.tgaccount.get(id).queryKey, updated)
      const next = toFormValues(updated)
      form.setValues(next)
      form.resetDirty(next)
      setSyncedKey(`${updated.id}:${updated.updated_at}`)
      invalidate()
      notifySuccess(t("common.saved"))
    },
    onError: (err) => {
      setServerError(applyServerErrors(form, err))
      const fields = Object.keys(fieldErrors(err))
      if (fields.length === 0) return
      const onlyPolicy = fields.every((f) => f.startsWith("business."))
      const hasConfig = fields.some((f) => !f.startsWith("business."))
      if (onlyPolicy && tab !== "policy") {
        void navigate({ to: ".", search: ((prev: Record<string, unknown>) => ({ ...prev, tab: "policy" })) as never, replace: true })
      } else if (hasConfig && tab === "policy") {
        void navigate({ to: ".", search: ((prev: Record<string, unknown>) => ({ ...prev, tab: undefined })) as never, replace: true })
      }
    },
  })

  const reconnect = useMutation({
    mutationFn: () => tenantApi.tgaccount.reconnect(id),
    onSuccess: (updated) => {
      queryClient.setQueryData(kit.queries.tgaccount.get(id).queryKey, updated)
      invalidate()
      notifySuccess(t("tg_studio.reconnected"))
    },
    onError: (err) => notifyError(err),
  })

  const updatePeers = useMutation({
    mutationFn: (peers: TgaccountSyncedPeer[]) =>
      tenantApi.tgaccount.syncedPeers(id, { peers, backfill_limit: 0 }),
    onSuccess: (updated) => {
      queryClient.setQueryData(kit.queries.tgaccount.get(id).queryKey, updated)
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const backfillPeer = useMutation({
    mutationFn: (externalChatID: string) => {
      setBackfillingPeer(externalChatID)
      return tenantApi.tgaccount.peerBackfill(id, { external_chat_id: externalChatID, limit: 20 })
    },
    onSuccess: (res) => {
      invalidate()
      if (res.imported > 0) {
        notifySuccess(t("tg_studio.peer_backfilled", { count: res.imported }))
      } else {
        notifySuccess(t("tg_studio.peer_backfill_none"))
      }
    },
    onError: (err) => notifyError(err),
    onSettled: () => setBackfillingPeer(null),
  })

  const handleTogglePause = (peer: TgaccountSyncedPeer) => {
    if (!account) return
    const nextPeers = account.synced_peers.map((p) =>
      p.external_chat_id === peer.external_chat_id ? { ...p, paused: !p.paused } : p,
    )
    updatePeers.mutate(nextPeers, {
      onSuccess: () => notifySuccess(t(peer.paused ? "tg_studio.peer_resumed_ok" : "tg_studio.peer_paused_ok")),
    })
  }

  const handleRemovePeer = async (peer: TgaccountSyncedPeer) => {
    if (!account) return
    const ok = await confirm({
      title: t("tg_studio.peer_remove_confirm", { name: peer.title }),
      message: t("tg_studio.peer_remove_message"),
      danger: true,
      confirmLabel: t("tg_studio.peer_remove"),
    })
    if (!ok) return
    const nextPeers = account.synced_peers.filter((p) => p.external_chat_id !== peer.external_chat_id)
    updatePeers.mutate(nextPeers, {
      onSuccess: () => notifySuccess(t("tg_studio.peer_removed")),
    })
  }

  const handleDiscard = () => {
    if (!account) return
    const next = toFormValues(account)
    form.setValues(next)
    form.resetDirty(next)
    setServerError(undefined)
  }

  const toggleFolder = (folderID: number) => {
    const cur = form.values.sync.folder_ids
    const next = cur.includes(folderID) ? cur.filter((x) => x !== folderID) : [...cur, folderID]
    form.setFieldValue("sync.folder_ids", next)
  }

  const memberOptions = useMemo(
    () => members.filter((m) => m.id !== me.data?.id).map((m) => ({ value: m.id, label: m.name })),
    [me.data?.id, members],
  )
  const tagOptions = useMemo(
    () => (tagsQuery.data?.items ?? []).map((tg) => ({ value: tg.id, label: tg.name })),
    [tagsQuery.data?.items],
  )

  const statusBadge = useMemo(() => {
    if (!account) return null
    const [color, label] = !account.enabled
      ? (["gray", t("common.disabled")] as const)
      : account.status === "connected"
        ? (["green", t("business_channels.tg_status_connected")] as const)
        : account.status === "auth_expired"
          ? (["red", t("business_channels.tg_status_expired")] as const)
          : (["gray", t("business_channels.tg_status_disconnected")] as const)
    return <StatusDot color={color}>{label}</StatusDot>
  }, [account, t])

  const identityMeta = account
    ? [account.display_name, account.username ? `@${account.username}` : "", account.phone].filter(Boolean).join(" · ")
    : ""

  return (
    <>
      <Page
        title={account ? account.name : t("tg_studio.title")}
        back={() => void navigate({ to: "/$slug/settings/business-channels", params: { slug }, search: { tab: "telegram" } })}
        actions={
          account ? (
            <Group
              gap="xs"
              style={{
                visibility: tab !== "peers" ? "visible" : "hidden",
                pointerEvents: tab !== "peers" ? "auto" : "none",
              }}
            >
              {dirty && (
                <Badge variant="light" color="yellow">
                  {t("common.unsaved")}
                </Badge>
              )}
              <Button variant="default" size="sm" disabled={!dirty || save.isPending} onClick={handleDiscard}>
                {t("common.discard")}
              </Button>
              <Button size="sm" disabled={!dirty} loading={save.isPending} onClick={() => void form.onSubmit((v) => save.mutate(v))()}>
                {t("common.save")}
              </Button>
            </Group>
          ) : undefined
        }
        tabs={[
          {
            value: "config",
            label: t("tg_studio.tab_config"),
            content: accountQuery.isPending ? (
              <Stack gap="md">
                <Skeleton height={130} radius="md" />
                <Skeleton height={220} radius="md" />
                <Skeleton height={200} radius="md" />
              </Stack>
            ) : account ? (
              <form noValidate onSubmit={form.onSubmit((v) => save.mutate(v))}>
                <Stack gap="md">
                  <FormSection
                    title={t("tg_studio.health_title")}
                    description={t("tg_studio.health_desc")}
                    actions={
                      <Group gap="xs">
                        <Button
                          size="xs"
                          variant="default"
                          leftSection={<IconRefresh size={14} />}
                          loading={reconnect.isPending}
                          disabled={account.status === "auth_expired"}
                          onClick={() => reconnect.mutate()}
                        >
                          {t("tg_studio.reconnect")}
                        </Button>
                        {platformConfigured && (
                          <Button
                            size="xs"
                            variant={account.status === "auth_expired" ? "filled" : "light"}
                            color={account.status === "auth_expired" ? "deep-orange" : "bright-blue"}
                            leftSection={<IconKey size={14} />}
                            onClick={() => setReauthOpen(true)}
                          >
                            {t("tg_studio.reauth")}
                          </Button>
                        )}
                      </Group>
                    }
                  >
                    <Group justify="space-between" align="center" wrap="wrap" gap="sm">
                      <Stack gap={2}>
                        <Group gap="sm">
                          {statusBadge}
                          {identityMeta && (
                            <Text size="xs" c="dimmed">
                              {identityMeta}
                            </Text>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed">
                          {account.last_synced_at
                            ? t("tg_studio.last_synced", { time: fullDateTime(account.last_synced_at, ui, zone) })
                            : t("tg_studio.never_synced")}
                        </Text>
                      </Stack>
                    </Group>
                    {account.last_error && (
                      <Alert color="red" variant="light">
                        <Text size="xs">{account.last_error}</Text>
                      </Alert>
                    )}
                  </FormSection>

                  <FormSection title={t("tg_studio.section_basic")} description={t("tg_studio.section_basic_desc")}>
                    <TextInput
                      label={t("business_channels.tg_account_name")}
                      placeholder={t("business_channels.tg_account_name_placeholder")}
                      required
                      maxLength={60}
                      {...form.getInputProps("name")}
                    />
                    <Switch
                      label={t("tg_studio.enabled")}
                      description={t("tg_studio.enabled_hint")}
                      {...form.getInputProps("enabled", { type: "checkbox" })}
                    />
                  </FormSection>

                  <FormSection title={t("tg_studio.section_sync")} description={t("tg_studio.section_sync_desc")}>
                    <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                      <Stack gap={4}>
                        <Text size="sm" fw={500}>
                          {t("tg_studio.sync_mode")}
                        </Text>
                        <SegmentedControl
                          value={form.values.sync.mode}
                          onChange={(v) => form.setFieldValue("sync.mode", v === "folders" ? "folders" : "whitelist")}
                          data={[
                            { value: "whitelist", label: t("tg_studio.mode_whitelist") },
                            { value: "folders", label: t("tg_studio.mode_folders") },
                          ]}
                        />
                      </Stack>
                      <Select
                        label={t("tg_studio.backfill_limit")}
                        data={[
                          { value: "0", label: t("tg_studio.backfill_0") },
                          { value: "20", label: t("tg_studio.backfill_20") },
                          { value: "50", label: t("tg_studio.backfill_50") },
                        ]}
                        value={form.values.sync.backfill_limit}
                        error={form.errors["sync.backfill_limit"]}
                        onChange={(v) => form.setFieldValue("sync.backfill_limit", v ?? "20")}
                      />
                    </SimpleGrid>

                    {form.values.sync.mode === "folders" && (
                      <Paper withBorder p="sm" radius="sm">
                        <Stack gap="xs">
                          <Group justify="space-between" align="center">
                            <Text size="xs" c="dimmed">
                              {t("tg_studio.folders_hint")}
                            </Text>
                            <Button
                              size="xs"
                              variant="subtle"
                              leftSection={<IconRefresh size={13} />}
                              loading={foldersQuery.isFetching}
                              onClick={() => void foldersQuery.refetch()}
                            >
                              {t("tg_studio.folders_refresh")}
                            </Button>
                          </Group>
                          {foldersQuery.isPending ? (
                            <Stack gap={6}>
                              <Skeleton h={32} radius="sm" />
                              <Skeleton h={32} radius="sm" />
                            </Stack>
                          ) : foldersQuery.isError ? (
                            <Alert color="red" variant="light">
                              <Text size="xs">{t("tg_studio.folders_failed")}</Text>
                            </Alert>
                          ) : (foldersQuery.data ?? []).length === 0 ? (
                            <Text size="xs" c="dimmed">
                              {t("tg_studio.folders_empty")}
                            </Text>
                          ) : (
                            <Stack gap={6}>
                              {(foldersQuery.data ?? []).map((f) => {
                                const checked = form.values.sync.folder_ids.includes(f.id)
                                return (
                                  <Paper
                                    key={f.id}
                                    withBorder
                                    px="sm"
                                    py={6}
                                    radius="sm"
                                    role="checkbox"
                                    tabIndex={0}
                                    aria-checked={checked}
                                    style={{ cursor: "pointer" }}
                                    onClick={() => toggleFolder(f.id)}
                                    onKeyDown={(e) => {
                                      if (e.key === "Enter" || e.key === " ") {
                                        e.preventDefault()
                                        toggleFolder(f.id)
                                      }
                                    }}
                                  >
                                    <Group justify="space-between" wrap="nowrap">
                                      <Group gap="xs" wrap="nowrap">
                                        <Checkbox checked={checked} onChange={() => {}} tabIndex={-1} style={{ pointerEvents: "none" }} />
                                        <IconFolder size={15} />
                                        <Text size="sm" fw={500}>
                                          {f.title}
                                        </Text>
                                      </Group>
                                      <Badge size="xs" variant="light" color="gray">
                                        {t("tg_studio.folder_peers_count", { count: f.peer_count })}
                                      </Badge>
                                    </Group>
                                  </Paper>
                                )
                              })}
                            </Stack>
                          )}
                        </Stack>
                      </Paper>
                    )}

                    <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                      <Switch
                        label={t("tg_studio.sync_outgoing")}
                        description={t("tg_studio.sync_outgoing_hint")}
                        {...form.getInputProps("sync.sync_outgoing", { type: "checkbox" })}
                      />
                      <Switch
                        label={t("tg_studio.sync_media")}
                        description={t("tg_studio.sync_media_hint")}
                        {...form.getInputProps("sync.sync_media", { type: "checkbox" })}
                      />
                    </SimpleGrid>

                    <Stack gap={4}>
                      <Text size="sm" fw={500}>
                        {t("tg_studio.group_mode")}
                      </Text>
                      <SegmentedControl
                        value={form.values.sync.group_mode}
                        onChange={(v) => form.setFieldValue("sync.group_mode", v === "mentions_only" ? "mentions_only" : "all")}
                        data={[
                          { value: "all", label: t("tg_studio.group_all") },
                          { value: "mentions_only", label: t("tg_studio.group_mentions") },
                        ]}
                      />
                    </Stack>
                  </FormSection>
                </Stack>
              </form>
            ) : null,
          },
          {
            value: "policy",
            label: t("biz_studio.section_policy"),
            content: accountQuery.isPending ? (
              <Skeleton height={240} radius="md" />
            ) : account ? (
              <form noValidate onSubmit={form.onSubmit((v) => save.mutate(v))}>
                <FormSection title={t("biz_studio.section_policy")} description={t("biz_studio.section_policy_desc")}>
                  <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
                    <MultiSelect
                      label={t("biz_studio.collaborators")}
                      description={t("biz_studio.collaborators_hint")}
                      inputWrapperOrder={["label", "input", "description", "error"]}
                      placeholder={t("biz_studio.collaborators_placeholder")}
                      data={memberOptions}
                      searchable
                      clearable
                      {...form.getInputProps("business.default_collaborator_ids")}
                    />
                    <MultiSelect
                      label={t("biz_studio.default_tags")}
                      description={t("biz_studio.default_tags_hint")}
                      inputWrapperOrder={["label", "input", "description", "error"]}
                      placeholder={t("biz_studio.default_tags_placeholder")}
                      data={tagOptions}
                      searchable
                      clearable
                      {...form.getInputProps("business.default_tag_ids")}
                    />
                  </SimpleGrid>
                  <Divider />
                  <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
                    <Select
                      label={t("biz_studio.translation_tone")}
                      data={[
                        { value: "", label: t("biz_studio.tone_inherit") },
                        { value: "formal", label: kit.label("TranslationTone", "formal") },
                        { value: "neutral", label: kit.label("TranslationTone", "neutral") },
                        { value: "friendly", label: kit.label("TranslationTone", "friendly") },
                      ]}
                      value={form.values.business.translation_tone}
                      error={form.errors["business.translation_tone"]}
                      onChange={(v) =>
                        form.setFieldValue("business.translation_tone", (v ?? "") as "" | "friendly" | "neutral" | "formal")
                      }
                    />
                    <Select
                      label={t("biz_studio.auto_translate")}
                      data={[
                        { value: "inherit", label: t("biz_studio.auto_translate_inherit") },
                        { value: "on", label: t("biz_studio.auto_translate_on") },
                        { value: "off", label: t("biz_studio.auto_translate_off") },
                      ]}
                      value={form.values.business.auto_translate}
                      error={form.errors["business.auto_translate"]}
                      onChange={(v) => form.setFieldValue("business.auto_translate", (v ?? "inherit") as "inherit" | "on" | "off")}
                    />
                  </SimpleGrid>
                </FormSection>
              </form>
            ) : null,
          },
          {
            value: "peers",
            label: t("tg_studio.tab_peers"),
            count: account?.synced_peers.length || undefined,
            content: (
              <Stack gap="sm">
                <PageActions>
                  <Button
                    leftSection={<IconPlus size={16} />}
                    disabled={!account || !account.enabled || account.status === "auth_expired"}
                    onClick={() => setPeersModalOpen(true)}
                  >
                    {t("business_channels.tg_pick_dialogs")}
                  </Button>
                </PageActions>
                <Text size="sm" c="dimmed">
                  {t("tg_studio.peers_desc")}
                </Text>

                <DataTable
                  records={account?.synced_peers ?? []}
                  rowKey="external_chat_id"
                  loading={accountQuery.isPending}
                  empty={t("tg_studio.peers_empty")}
                  columns={[
                    {
                      accessor: "title",
                      title: t("business_channels.col_synced"),
                      render: (peer) => (
                        <Group gap="sm" wrap="nowrap">
                          <ThemeIcon size={28} radius="xl" variant="light" color={peer.is_group ? "brand" : "bright-blue"} style={{ flex: "none" }}>
                            {peer.is_group ? <IconUsersGroup size={14} /> : <IconUser size={14} />}
                          </ThemeIcon>
                          <Box style={{ minWidth: 0 }}>
                            <Group gap={6} wrap="nowrap">
                              <Text size="sm" fw={500} truncate>
                                {peer.title}
                              </Text>
                              <Badge size="xs" variant="light" color={peer.is_group ? "brand" : "gray"} style={{ flex: "none" }}>
                                {t(peer.is_group ? "chat.mode_group" : "chat.mode_direct")}
                              </Badge>
                            </Group>
                            {peer.username && (
                              <Text size="xs" c="dimmed" truncate>
                                @{peer.username}
                              </Text>
                            )}
                          </Box>
                        </Group>
                      ),
                    },
                    {
                      accessor: "paused",
                      title: t("common.status"),
                      render: (peer) => (
                        <StatusDot color={peer.paused ? "yellow" : "green"}>
                          {t(peer.paused ? "tg_studio.peer_status_paused" : "tg_studio.peer_status_active")}
                        </StatusDot>
                      ),
                    },
                    {
                      accessor: "actions",
                      title: "",
                      textAlign: "right",
                      render: (peer) => (
                        <RowActions
                          actions={[
                            {
                              label: t(peer.paused ? "tg_studio.peer_resume" : "tg_studio.peer_pause"),
                              icon: peer.paused ? <IconPlayerPlay size={14} /> : <IconPlayerPause size={14} />,
                              disabled: updatePeers.isPending,
                              onClick: () => handleTogglePause(peer),
                            },
                            {
                              label: t("tg_studio.peer_remove"),
                              icon: <IconTrash size={14} />,
                              danger: true,
                              disabled: updatePeers.isPending,
                              onClick: () => void handleRemovePeer(peer),
                            },
                          ]}
                        >
                          <Button
                            size="xs"
                            variant="subtle"
                            loading={backfillingPeer === peer.external_chat_id}
                            disabled={backfillPeer.isPending || !account?.enabled || account.status === "auth_expired"}
                            onClick={() => backfillPeer.mutate(peer.external_chat_id)}
                          >
                            {t("tg_studio.peer_backfill")}
                          </Button>
                        </RowActions>
                      ),
                    },
                  ]}
                />
              </Stack>
            ),
          },
        ]}
      >
        <FormError error={serverError} />
      </Page>

      <TelegramLoginModal
        opened={reauthOpen}
        reconnectAccount={account ?? null}
        onClose={() => setReauthOpen(false)}
        onConnected={(updated) => {
          queryClient.setQueryData(kit.queries.tgaccount.get(id).queryKey, updated)
          invalidate()
          setReauthOpen(false)
        }}
      />

      <SyncedPeersModal
        account={peersModalOpen ? (account ?? null) : null}
        onClose={() => setPeersModalOpen(false)}
        onSaved={(updated) => {
          queryClient.setQueryData(kit.queries.tgaccount.get(id).queryKey, updated)
          invalidate()
          setPeersModalOpen(false)
        }}
      />
    </>
  )
}
