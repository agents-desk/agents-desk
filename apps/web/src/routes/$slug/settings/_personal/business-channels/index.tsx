import type { TgaccountAccountDTO, WidgetBusinessLinkDTO } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  CopyButton,
  Group,
  Modal,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import {
  IconCheck,
  IconCopy,
  IconExternalLink,
  IconInfoCircle,
  IconLock,
  IconPlayerPause,
  IconPlayerPlay,
  IconPlugConnectedX,
  IconPlus,
  IconTrash,
  IconUser,
  IconUsersGroup,
} from "@tabler/icons-react"
import { AnchorLink, applyServerErrors, confirm, DataTable, FormError, notifyError, notifySuccess, Page, PageActions, preloadQueries, StatusDot, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { RowActions } from "../../../../../components/row-actions"
import { SyncedPeersModal, TelegramLoginModal } from "../../../../../features/business/telegram-modals"

// 我的商务渠道列表：采用与其他设置页一致的 Page 页内标签 + 标准表格布局，
// 分为「网页商务链接」与「Telegram 个人号」两个标签；点击链接或个人号进入各自的可视化配置工作台。
export const Route = createFileRoute("/$slug/settings/_personal/business-channels/")({
  loader: ({ context }) => preloadQueries(context.app.queryClient, [kit.queries.business.linkList(), kit.queries.tgaccount.list(), kit.queries.business.policyGet()]),
  component: BusinessChannelsPage,
})

function BusinessChannelsPage(): ReactNode {
  const { slug } = Route.useParams()
  const t = useT()
  const linksQuery = useQuery(kit.queries.business.linkList())
  const tgQuery = useQuery(kit.queries.tgaccount.list())
  const linkCount = linksQuery.data?.length ?? 0
  const tgCount = tgQuery.data?.accounts.length ?? 0

  return (
    <Page
      title={t("nav.business_channels")}
      description={t("business_channels.description")}
      tabs={[
        {
          value: "links",
          label: t("business_channels.links_title"),
          count: linkCount || undefined,
          content: <BusinessLinksTab slug={slug} />,
        },
        {
          value: "telegram",
          label: t("business_channels.tg_title"),
          count: tgCount || undefined,
          content: <TelegramAccountsTab slug={slug} />,
        },
      ]}
    />
  )
}

/** BusinessLinksTab 管理成员的网页商务沟通链接列表；新建与配置均进入可视化工作台。 */
function BusinessLinksTab({ slug }: { slug: string }): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const list = useQuery(kit.queries.business.linkList())
  const policy = useQuery(kit.queries.business.policyGet()).data
  const items = list.data ?? []
  const linksAllowed = policy ? policy.platform_links_enabled && policy.links_enabled : true
  const maxLinks = policy?.max_links_per_member ?? 0
  const quotaReached = maxLinks > 0 && items.length >= maxLinks

  const [creating, setCreating] = useState(false)
  const [formError, setFormError] = useState<ReturnType<typeof applyServerErrors>>()

  const form = useForm<{ name: string }>({
    initialValues: { name: "" },
  })

  const openCreate = () => {
    setFormError(undefined)
    form.setValues({ name: "" })
    setCreating(true)
  }

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.business.key() })
    void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
  }

  const create = useMutation({
    mutationFn: (v: { name: string }) => tenantApi.business.linkCreate({ name: v.name.trim() }),
    onSuccess: (created) => {
      notifySuccess(t("business_channels.link_created"))
      setCreating(false)
      invalidate()
      void navigate({ to: "/$slug/settings/business-channels/links/$id", params: { slug, id: created.id } })
    },
    onError: (err) => setFormError(applyServerErrors(form, err)),
  })

  const toggleEnabled = useMutation({
    mutationFn: (item: WidgetBusinessLinkDTO) =>
      tenantApi.business.linkUpdate(item.id, {
        name: item.name,
        enabled: !item.enabled,
        appearance: item.appearance,
      }),
    onSuccess: (updated) => {
      notifySuccess(t(updated.enabled ? "common.enabled" : "common.disabled"))
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const remove = useMutation({
    mutationFn: (id: string) => tenantApi.business.linkDelete(id),
    onSuccess: () => {
      notifySuccess(t("business_channels.link_deleted"))
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const handleDelete = async (item: WidgetBusinessLinkDTO) => {
    const ok = await confirm({
      title: t("business_channels.link_delete_confirm", { name: item.name }),
      message: t("business_channels.link_delete_message"),
      danger: true,
      confirmLabel: t("common.delete"),
    })
    if (ok) remove.mutate(item.id)
  }

  return (
    <Stack gap="sm">
      <PageActions>
        <Tooltip
          label={!linksAllowed ? t("business_channels.links_disabled_title") : t("business_channels.quota_reached", { limit: maxLinks })}
          disabled={linksAllowed && !quotaReached}
        >
          <Button leftSection={<IconPlus size={16} />} disabled={!linksAllowed || quotaReached} onClick={openCreate}>
            {t("business_channels.link_new")}
          </Button>
        </Tooltip>
      </PageActions>
      <Group gap="xs" wrap="wrap">
        <Text size="sm" c="dimmed">
          {t("business_channels.links_desc")}
        </Text>
        {maxLinks > 0 && (
          <Badge size="xs" variant="light" color={quotaReached ? "deep-orange" : "gray"}>
            {t("business_channels.quota_badge", { used: items.length, limit: maxLinks })}
          </Badge>
        )}
      </Group>

      {policy && !linksAllowed && (
        <Alert icon={<IconInfoCircle size={16} />} color="yellow" variant="light" title={t("business_channels.links_disabled_title")}>
          <Text size="sm">{t("business_channels.links_disabled_desc")}</Text>
        </Alert>
      )}

      <DataTable
        records={items}
        loading={list.isPending}
        empty={t("business_channels.links_empty")}
        columns={[
          {
            accessor: "name",
            title: t("business_channels.link_name"),
            render: (item) => (
              <Group gap="xs" wrap="nowrap">
                <span
                  className="desk-status-mark"
                  aria-hidden
                  style={{ background: item.appearance.color || "var(--mantine-primary-color-filled)" }}
                />
                <Box style={{ minWidth: 0 }}>
                  <Group gap={6} wrap="nowrap">
                    <AnchorLink
                      to="/$slug/settings/business-channels/links/$id"
                      params={{ slug, id: item.id }}
                      size="sm"
                      fw={500}
                      truncate
                    >
                      {item.name}
                    </AnchorLink>
                    {item.access.mode === "passcode" && (
                      <Badge size="xs" variant="light" color="yellow" leftSection={<IconLock size={10} />} style={{ flex: "none" }}>
                        {t("biz_studio.access_passcode")}
                      </Badge>
                    )}
                    {item.intake.enabled && (
                      <Badge size="xs" variant="light" color="bright-blue" style={{ flex: "none" }}>
                        {t("biz_studio.scene_intake")}
                      </Badge>
                    )}
                  </Group>
                  {item.appearance.greeting && (
                    <Text size="xs" c="dimmed" truncate maw={280}>
                      {item.appearance.greeting}
                    </Text>
                  )}
                </Box>
              </Group>
            ),
          },
          {
            accessor: "chat_url",
            title: t("business_channels.col_url"),
            render: (item) => (
              <Group gap={6} wrap="nowrap">
                <Text size="xs" c="dimmed" ff="monospace" truncate maw={320}>
                  {item.chat_url}
                </Text>
                <CopyButton value={item.chat_url} timeout={1500}>
                  {({ copied, copy }) => (
                    <Tooltip label={t(copied ? "common.copied" : "business_channels.copy_url")} openDelay={200}>
                      <ActionIcon color={copied ? "green" : "gray"} onClick={copy} aria-label={t("business_channels.copy_url")}>
                        {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                      </ActionIcon>
                    </Tooltip>
                  )}
                </CopyButton>
                <Tooltip label={t("business_channels.open_preview")} openDelay={200}>
                  <ActionIcon
                    component="a"
                    href={item.chat_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    aria-label={t("business_channels.open_preview")}
                  >
                    <IconExternalLink size={16} />
                  </ActionIcon>
                </Tooltip>
              </Group>
            ),
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
                editLabel={t("biz_studio.configure")}
                onEdit={() => void navigate({ to: "/$slug/settings/business-channels/links/$id", params: { slug, id: item.id } })}
                actions={[
                  {
                    label: t(item.enabled ? "common.disable" : "common.enable"),
                    icon: item.enabled ? <IconPlayerPause size={14} /> : <IconPlayerPlay size={14} />,
                    disabled: toggleEnabled.isPending,
                    onClick: () => toggleEnabled.mutate(item),
                  },
                  {
                    label: t("common.delete"),
                    icon: <IconTrash size={14} />,
                    danger: true,
                    disabled: remove.isPending,
                    onClick: () => void handleDelete(item),
                  },
                ]}
              />
            ),
          },
        ]}
      />

      <Modal opened={creating} onClose={() => setCreating(false)} title={t("business_channels.link_create_title")}>
        <form onSubmit={form.onSubmit((v) => create.mutate(v))}>
          <Stack gap="md">
            <FormError error={formError} />
            <Text size="xs" c="dimmed">
              {t("biz_studio.create_hint")}
            </Text>
            <TextInput
              label={t("business_channels.link_name")}
              placeholder={t("business_channels.link_name_placeholder")}
              required
              maxLength={60}
              data-autofocus
              {...form.getInputProps("name")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setCreating(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={create.isPending}>
                {t("business_channels.link_new")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  )
}

/** TelegramAccountsTab 以与其他渠道设置页一致的标准表格管理当前成员绑定的 Telegram 个人号，点击进入个人号工作台。 */
function TelegramAccountsTab({ slug }: { slug: string }): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const list = useQuery(kit.queries.tgaccount.list())
  const policy = useQuery(kit.queries.business.policyGet()).data
  const configured = list.data?.configured ?? false
  const accounts = list.data?.accounts ?? []
  const tgAllowed = policy ? policy.platform_telegram_enabled && policy.telegram_accounts_enabled : true
  const maxTG = policy?.max_telegram_accounts_per_member ?? 0
  const quotaReached = maxTG > 0 && accounts.length >= maxTG

  const [loginOpen, setLoginOpen] = useState(false)
  const [reconnectTarget, setReconnectTarget] = useState<TgaccountAccountDTO | null>(null)
  const [peersTarget, setPeersTarget] = useState<TgaccountAccountDTO | null>(null)

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.tgaccount.key() })
    void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
  }

  const toggleEnabled = useMutation({
    mutationFn: (acc: TgaccountAccountDTO) =>
      tenantApi.tgaccount.update(acc.id, { name: acc.name, enabled: !acc.enabled }),
    onSuccess: (updated) => {
      notifySuccess(t(updated.enabled ? "common.enabled" : "common.disabled"))
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const disconnect = useMutation({
    mutationFn: (id: string) => tenantApi.tgaccount.disconnect(id),
    onSuccess: () => {
      notifySuccess(t("business_channels.tg_disconnected"))
      invalidate()
    },
    onError: (err) => notifyError(err),
  })

  const handleDisconnect = async (acc: TgaccountAccountDTO) => {
    const ok = await confirm({
      title: t("business_channels.tg_disconnect_confirm", { name: acc.name }),
      message: t("business_channels.tg_disconnect_message"),
      danger: true,
      confirmLabel: t("business_channels.tg_disconnect"),
    })
    if (ok) disconnect.mutate(acc.id)
  }

  const openNewLogin = () => {
    setReconnectTarget(null)
    setLoginOpen(true)
  }

  const openReauth = (acc: TgaccountAccountDTO) => {
    setReconnectTarget(acc)
    setLoginOpen(true)
  }

  return (
    <Stack gap="sm">
      <PageActions>
        <Tooltip
          label={
            !tgAllowed
              ? t("business_channels.tg_disabled_title")
              : !configured
                ? t("business_channels.tg_not_configured_short")
                : t("business_channels.quota_reached", { limit: maxTG })
          }
          disabled={(tgAllowed && configured && !quotaReached) || list.isPending}
        >
          <Button leftSection={<IconPlus size={16} />} disabled={!tgAllowed || !configured || quotaReached} onClick={openNewLogin}>
            {t("business_channels.tg_connect")}
          </Button>
        </Tooltip>
      </PageActions>
      <Group gap="xs" wrap="wrap">
        <Text size="sm" c="dimmed">
          {t("business_channels.tg_desc")}
        </Text>
        {maxTG > 0 && (
          <Badge size="xs" variant="light" color={quotaReached ? "deep-orange" : "gray"}>
            {t("business_channels.quota_badge", { used: accounts.length, limit: maxTG })}
          </Badge>
        )}
      </Group>

      {policy && !tgAllowed && (
        <Alert icon={<IconInfoCircle size={16} />} color="yellow" variant="light" title={t("business_channels.tg_disabled_title")}>
          <Text size="sm">{t("business_channels.tg_disabled_desc")}</Text>
        </Alert>
      )}

      {!list.isPending && tgAllowed && !configured && (
        <Alert icon={<IconInfoCircle size={16} />} color="bright-blue" variant="light" title={t("business_channels.tg_not_configured_title")}>
          <Text size="sm">{t("business_channels.tg_not_configured_desc")}</Text>
        </Alert>
      )}

      <DataTable
        records={accounts}
        loading={list.isPending}
        empty={t("business_channels.tg_empty")}
        columns={[
          {
            accessor: "name",
            title: t("business_channels.tg_account_name"),
            render: (acc) => {
              const identityMeta = [acc.display_name, acc.username ? `@${acc.username}` : "", acc.phone].filter(Boolean).join(" · ")
              return (
                <Box style={{ minWidth: 0 }}>
                  <AnchorLink
                    to="/$slug/settings/business-channels/telegram/$id"
                    params={{ slug, id: acc.id }}
                    size="sm"
                    fw={500}
                    truncate
                  >
                    {acc.name}
                  </AnchorLink>
                  {identityMeta && (
                    <Text size="xs" c="dimmed" truncate>
                      {identityMeta}
                    </Text>
                  )}
                  {acc.last_error && (
                    <Text size="xs" c="red" truncate>
                      {acc.last_error}
                    </Text>
                  )}
                </Box>
              )
            },
          },
          {
            accessor: "synced_peers",
            title: t("business_channels.col_synced"),
            render: (acc) => {
              if (acc.synced_peers.length === 0) {
                return (
                  <Text size="xs" c="dimmed">
                    {t("business_channels.tg_no_synced_peers")}
                  </Text>
                )
              }
              const directCount = acc.synced_peers.filter((p) => !p.is_group).length
              const groupCount = acc.synced_peers.filter((p) => p.is_group).length
              return (
                <Stack gap={4}>
                  <Text size="xs" c="dimmed">
                    {t("business_channels.tg_synced_summary", { total: acc.synced_peers.length, direct: directCount, groups: groupCount })}
                  </Text>
                  <Group gap={4} wrap="wrap">
                    {acc.synced_peers.slice(0, 4).map((p) => (
                      <Badge
                        key={p.external_chat_id}
                        size="xs"
                        variant="light"
                        color={p.paused ? "yellow" : p.is_group ? "brand" : "gray"}
                        leftSection={p.is_group ? <IconUsersGroup size={10} /> : <IconUser size={10} />}
                        style={{ textTransform: "none", fontWeight: 500 }}
                      >
                        {p.title}
                      </Badge>
                    ))}
                    {acc.synced_peers.length > 4 && (
                      <Text size="xs" c="dimmed">
                        +{acc.synced_peers.length - 4}
                      </Text>
                    )}
                  </Group>
                </Stack>
              )
            },
          },
          {
            accessor: "status",
            title: t("common.status"),
            render: (acc) => {
              const [color, label] = !acc.enabled
                ? (["gray", t("common.disabled")] as const)
                : acc.status === "connected"
                  ? (["green", t("business_channels.tg_status_connected")] as const)
                  : acc.status === "auth_expired"
                    ? (["red", t("business_channels.tg_status_expired")] as const)
                    : (["gray", t("business_channels.tg_status_disconnected")] as const)
              return <StatusDot color={color}>{label}</StatusDot>
            },
          },
          {
            accessor: "actions",
            title: "",
            textAlign: "right",
            render: (acc) => (
              <RowActions
                editLabel={t("biz_studio.configure")}
                onEdit={() => void navigate({ to: "/$slug/settings/business-channels/telegram/$id", params: { slug, id: acc.id } })}
                actions={[
                  {
                    label: t(acc.enabled ? "common.disable" : "common.enable"),
                    icon: acc.enabled ? <IconPlayerPause size={14} /> : <IconPlayerPlay size={14} />,
                    disabled: toggleEnabled.isPending,
                    onClick: () => toggleEnabled.mutate(acc),
                  },
                  {
                    label: t("business_channels.tg_disconnect"),
                    icon: <IconPlugConnectedX size={14} />,
                    danger: true,
                    disabled: disconnect.isPending,
                    onClick: () => void handleDisconnect(acc),
                  },
                ]}
              >
                {acc.status === "auth_expired" && configured ? (
                  <Button size="xs" variant="subtle" color="deep-orange" onClick={() => openReauth(acc)}>
                    {t("business_channels.tg_relogin")}
                  </Button>
                ) : (
                  <Button size="xs" variant="subtle" disabled={!acc.enabled || acc.status === "auth_expired"} onClick={() => setPeersTarget(acc)}>
                    {t("business_channels.tg_pick_dialogs")}
                  </Button>
                )}
              </RowActions>
            ),
          },
        ]}
      />

      <TelegramLoginModal
        opened={loginOpen}
        reconnectAccount={reconnectTarget}
        onClose={() => {
          setLoginOpen(false)
          setReconnectTarget(null)
        }}
        onConnected={(account) => {
          const wasReconnect = Boolean(reconnectTarget)
          invalidate()
          setLoginOpen(false)
          setReconnectTarget(null)
          if (!wasReconnect) {
            void navigate({ to: "/$slug/settings/business-channels/telegram/$id", params: { slug, id: account.id } })
          }
        }}
      />

      <SyncedPeersModal
        account={peersTarget}
        onClose={() => setPeersTarget(null)}
        onSaved={() => {
          invalidate()
          setPeersTarget(null)
        }}
      />
    </Stack>
  )
}
