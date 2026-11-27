import type {
  TgaccountAccountDTO,
  TgaccountDialogDTO,
  TgaccountLoginMode,
  TgaccountLoginSessionDTO,
  TgaccountSyncedPeer,
  WidgetBusinessLinkDTO,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Center,
  Checkbox,
  CopyButton,
  Divider,
  Group,
  Loader,
  Modal,
  Paper,
  PasswordInput,
  ScrollArea,
  SegmentedControl,
  Skeleton,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
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
  IconRefresh,
  IconSearch,
  IconTrash,
  IconUser,
  IconUsersGroup,
} from "@tabler/icons-react"
import { AnchorLink, applyServerErrors, confirm, DataTable, FormError, notifyError, notifySuccess, Page, preloadQueries, StatusDot, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { kit, tenantApi } from "../../../../../../api"
import { RowActions } from "../../../../../../components/row-actions"

// 我的商务渠道列表：采用与其他设置页一致的 Page 页内标签 + 标准表格布局，
// 分为「网页商务链接」与「Telegram 个人号」两个标签；点击链接进入可视化配置工作台。
export const Route = createFileRoute("/t/$slug/settings/_personal/business-channels/")({
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
          content: <TelegramAccountsTab />,
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
      void navigate({ to: "/t/$slug/settings/business-channels/links/$id", params: { slug, id: created.id } })
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
    <Stack gap="md">
      <Group justify="space-between" align="center" wrap="wrap" gap="sm">
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
        <Tooltip
          label={!linksAllowed ? t("business_channels.links_disabled_title") : t("business_channels.quota_reached", { limit: maxLinks })}
          disabled={linksAllowed && !quotaReached}
        >
          <Button leftSection={<IconPlus size={16} />} disabled={!linksAllowed || quotaReached} onClick={openCreate}>
            {t("business_channels.link_new")}
          </Button>
        </Tooltip>
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
                      to="/t/$slug/settings/business-channels/links/$id"
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
                onEdit={() => void navigate({ to: "/t/$slug/settings/business-channels/links/$id", params: { slug, id: item.id } })}
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

/** TelegramAccountsTab 以与其他渠道设置页一致的标准表格管理当前成员绑定的 Telegram 个人号。 */
function TelegramAccountsTab(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const list = useQuery(kit.queries.tgaccount.list())
  const policy = useQuery(kit.queries.business.policyGet()).data
  const configured = list.data?.configured ?? false
  const accounts = list.data?.accounts ?? []
  const tgAllowed = policy ? policy.platform_telegram_enabled && policy.telegram_accounts_enabled : true
  const maxTG = policy?.max_telegram_accounts_per_member ?? 0
  const quotaReached = maxTG > 0 && accounts.length >= maxTG

  const [loginOpen, setLoginOpen] = useState(false)
  const [peersTarget, setPeersTarget] = useState<TgaccountAccountDTO | null>(null)
  const [renaming, setRenaming] = useState<TgaccountAccountDTO | null>(null)
  const [renameValue, setRenameValue] = useState("")

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

  const saveRename = useMutation({
    mutationFn: () => {
      if (!renaming) throw new Error("missing account")
      return tenantApi.tgaccount.update(renaming.id, { name: renameValue.trim(), enabled: renaming.enabled })
    },
    onSuccess: () => {
      notifySuccess(t("common.saved"))
      setRenaming(null)
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

  return (
    <Stack gap="md">
      <Group justify="space-between" align="center" wrap="wrap" gap="sm">
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
          <Button leftSection={<IconPlus size={16} />} disabled={!tgAllowed || !configured || quotaReached} onClick={() => setLoginOpen(true)}>
            {t("business_channels.tg_connect")}
          </Button>
        </Tooltip>
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
                  <Text size="sm" fw={500} truncate>
                    {acc.name}
                  </Text>
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
                        color={p.is_group ? "brand" : "gray"}
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
                editLabel={t("business_channels.tg_rename")}
                onEdit={() => {
                  setRenaming(acc)
                  setRenameValue(acc.name)
                }}
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
                  <Button size="xs" variant="subtle" color="deep-orange" onClick={() => setLoginOpen(true)}>
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
        onClose={() => setLoginOpen(false)}
        onConnected={(account) => {
          invalidate()
          setLoginOpen(false)
          setPeersTarget(account)
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

      <Modal opened={renaming !== null} onClose={() => setRenaming(null)} title={t("business_channels.tg_rename")}>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (!renameValue.trim()) return
            saveRename.mutate()
          }}
        >
          <Stack gap="md">
            <TextInput
              label={t("business_channels.tg_account_name")}
              value={renameValue}
              onChange={(e) => setRenameValue(e.currentTarget.value)}
              maxLength={60}
              required
              data-autofocus
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setRenaming(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={saveRename.isPending} disabled={!renameValue.trim()}>
                {t("common.save")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  )
}

/** TelegramLoginModal 是绑定 Telegram 个人号的交互向导，同时支持扫码登录与手机号验证码 + 两步验证登录。 */
function TelegramLoginModal({
  opened,
  onClose,
  onConnected,
}: {
  opened: boolean
  onClose: () => void
  onConnected: (account: TgaccountAccountDTO) => void
}): ReactNode {
  const t = useT()
  const [mode, setMode] = useState<TgaccountLoginMode>("qr")
  const [name, setName] = useState("")
  const [phone, setPhone] = useState("")
  const [code, setCode] = useState("")
  const [password, setPassword] = useState("")
  const [session, setSession] = useState<TgaccountLoginSessionDTO | null>(null)

  useEffect(() => {
    if (!opened) {
      setMode("qr")
      setName("")
      setPhone("")
      setCode("")
      setPassword("")
      setSession(null)
    }
  }, [opened])

  const startLogin = useMutation({
    mutationFn: () =>
      tenantApi.tgaccount.loginStart({
        mode,
        name: name.trim() || undefined,
        phone: mode === "phone" ? phone.trim() : undefined,
      }),
    onSuccess: (next) => {
      setSession(next)
      if (next.step === "connected" && next.account) {
        notifySuccess(t("business_channels.tg_connected"))
        onConnected(next.account)
      }
    },
    onError: (err) => notifyError(err),
  })

  const submitCode = useMutation({
    mutationFn: () => tenantApi.tgaccount.loginCode(session!.login_id, { code: code.trim() }),
    onSuccess: (next) => {
      setCode("")
      setSession(next)
      if (next.step === "connected" && next.account) {
        notifySuccess(t("business_channels.tg_connected"))
        onConnected(next.account)
      }
    },
    onError: (err) => notifyError(err),
  })

  const submitPassword = useMutation({
    mutationFn: () => tenantApi.tgaccount.loginPassword(session!.login_id, { password }),
    onSuccess: (next) => {
      setPassword("")
      setSession(next)
      if (next.step === "connected" && next.account) {
        notifySuccess(t("business_channels.tg_connected"))
        onConnected(next.account)
      }
    },
    onError: (err) => notifyError(err),
  })

  // 扫码等待期间每 2 秒轮询登录状态，自动刷新二维码或进入 2FA / 完成步骤
  useEffect(() => {
    if (!opened || !session || session.step !== "awaiting_qr") return
    let active = true
    const timer = setInterval(() => {
      void tenantApi.tgaccount
        .loginGet(session.login_id)
        .then((next) => {
          if (!active) return
          setSession(next)
          if (next.step === "connected" && next.account) {
            notifySuccess(t("business_channels.tg_connected"))
            onConnected(next.account)
          }
        })
        .catch(() => {})
    }, 2000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [onConnected, opened, session, t])

  return (
    <Modal opened={opened} onClose={onClose} title={t("business_channels.tg_connect_title")} size="md">
      {!session ? (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            startLogin.mutate()
          }}
        >
          <Stack gap="md">
            <SegmentedControl
              fullWidth
              value={mode}
              onChange={(v) => setMode(v as TgaccountLoginMode)}
              data={[
                { value: "qr", label: t("business_channels.tg_mode_qr") },
                { value: "phone", label: t("business_channels.tg_mode_phone") },
              ]}
            />
            <TextInput
              label={t("business_channels.tg_account_name")}
              placeholder={t("business_channels.tg_account_name_placeholder")}
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              maxLength={60}
            />
            {mode === "phone" && (
              <TextInput
                label={t("business_channels.tg_phone")}
                description={t("business_channels.tg_phone_hint")}
                placeholder="+8613800138000"
                value={phone}
                onChange={(e) => setPhone(e.currentTarget.value)}
                required
                data-autofocus
              />
            )}
            <Group justify="flex-end">
              <Button variant="default" onClick={onClose}>
                {t("common.cancel")}
              </Button>
              <Button
                type="submit"
                color="bright-blue"
                loading={startLogin.isPending}
                disabled={mode === "phone" && !phone.trim()}
              >
                {t(mode === "qr" ? "business_channels.tg_generate_qr" : "business_channels.tg_send_code")}
              </Button>
            </Group>
          </Stack>
        </form>
      ) : session.step === "awaiting_qr" ? (
        <Stack gap="md" align="center">
          {session.qr_data_url ? (
            <Paper withBorder p="sm" radius="md" bg="white">
              <img src={session.qr_data_url} alt={t("business_channels.tg_mode_qr")} width={220} height={220} style={{ display: "block" }} />
            </Paper>
          ) : (
            <Center h={220}>
              <Loader size="sm" />
            </Center>
          )}
          <Text size="sm" ta="center" c="dimmed">
            {t("business_channels.tg_qr_instructions")}
          </Text>
          <Group gap="xs">
            <Loader size={14} />
            <Text size="xs" c="dimmed">
              {t("business_channels.tg_waiting_scan")}
            </Text>
          </Group>
          <Group justify="flex-end" w="100%">
            <Button variant="default" onClick={() => setSession(null)}>
              {t("business_channels.tg_back")}
            </Button>
          </Group>
        </Stack>
      ) : session.step === "awaiting_code" ? (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (!code.trim()) return
            submitCode.mutate()
          }}
        >
          <Stack gap="md">
            <Text size="sm" c="dimmed">
              {t("business_channels.tg_code_hint")}
            </Text>
            <TextInput
              label={t("business_channels.tg_code")}
              placeholder="12345"
              value={code}
              onChange={(e) => setCode(e.currentTarget.value)}
              required
              data-autofocus
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setSession(null)}>
                {t("business_channels.tg_back")}
              </Button>
              <Button type="submit" color="bright-blue" loading={submitCode.isPending} disabled={!code.trim()}>
                {t("business_channels.tg_verify")}
              </Button>
            </Group>
          </Stack>
        </form>
      ) : session.step === "awaiting_password" ? (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (!password) return
            submitPassword.mutate()
          }}
        >
          <Stack gap="md">
            <Text size="sm" c="dimmed">
              {session.password_hint
                ? t("business_channels.tg_password_hint_with_tip", { hint: session.password_hint })
                : t("business_channels.tg_password_hint")}
            </Text>
            <PasswordInput
              label={t("business_channels.tg_password")}
              value={password}
              onChange={(e) => setPassword(e.currentTarget.value)}
              required
              data-autofocus
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setSession(null)}>
                {t("business_channels.tg_back")}
              </Button>
              <Button type="submit" color="bright-blue" loading={submitPassword.isPending} disabled={!password}>
                {t("business_channels.tg_verify")}
              </Button>
            </Group>
          </Stack>
        </form>
      ) : (
        <Stack gap="md">
          <Alert color="red" variant="light" title={t("business_channels.tg_login_failed")}>
            <Text size="sm">{session.error || t("business_channels.tg_login_failed")}</Text>
          </Alert>
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button color="bright-blue" onClick={() => setSession(null)}>
              {t("business_channels.tg_retry")}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}

/** SyncedPeersModal 让成员从已绑定 Telegram 个人号中挑选需要同步到商务沟通的 1:1 单聊与群聊（白名单模式，不同步私人闲聊）。 */
function SyncedPeersModal({
  account,
  onClose,
  onSaved,
}: {
  account: TgaccountAccountDTO | null
  onClose: () => void
  onSaved: () => void
}): ReactNode {
  const t = useT()
  const [q, setQ] = useState("")
  const [kindFilter, setKindFilter] = useState<"all" | "direct" | "group">("all")
  const [selected, setSelected] = useState<Map<string, TgaccountSyncedPeer>>(new Map())
  const [backfill, setBackfill] = useState(true)

  const dialogsQuery = useQuery({
    ...kit.queries.tgaccount.dialogs(account?.id ?? ""),
    enabled: Boolean(account),
  })

  // 打开弹窗时初始化已勾选白名单
  useEffect(() => {
    if (!account) return
    setQ("")
    setKindFilter("all")
    setBackfill(true)
    const next = new Map<string, TgaccountSyncedPeer>()
    for (const p of account.synced_peers) {
      next.set(p.external_chat_id, p)
    }
    setSelected(next)
  }, [account])

  const dialogs = dialogsQuery.data ?? []
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return dialogs.filter((d) => {
      if (kindFilter === "direct" && d.is_group) return false
      if (kindFilter === "group" && !d.is_group) return false
      if (!needle) return true
      return d.title.toLowerCase().includes(needle) || (d.username ?? "").toLowerCase().includes(needle)
    })
  }, [dialogs, kindFilter, q])

  const toggleDialog = (d: TgaccountDialogDTO) => {
    setSelected((prev) => {
      const next = new Map(prev)
      if (next.has(d.external_chat_id)) {
        next.delete(d.external_chat_id)
      } else {
        next.set(d.external_chat_id, {
          external_chat_id: d.external_chat_id,
          peer_type: d.peer_type,
          peer_id: d.peer_id,
          access_hash: d.access_hash,
          title: d.title,
          username: d.username,
          is_group: d.is_group,
        })
      }
      return next
    })
  }

  const save = useMutation({
    mutationFn: () =>
      tenantApi.tgaccount.syncedPeers(account!.id, {
        peers: Array.from(selected.values()),
        backfill_limit: backfill ? 20 : 0,
      }),
    onSuccess: () => {
      notifySuccess(t("business_channels.tg_peers_saved"))
      onSaved()
    },
    onError: (err) => notifyError(err),
  })

  return (
    <Modal
      opened={account !== null}
      onClose={onClose}
      title={account ? t("business_channels.tg_pick_dialogs_title", { name: account.name }) : ""}
      size="lg"
    >
      <Stack gap="md">
        <Text size="xs" c="dimmed">
          {t("business_channels.tg_pick_dialogs_hint")}
        </Text>

        <Group gap="xs" wrap="wrap">
          <TextInput
            style={{ flex: 1, minWidth: 180 }}
            size="sm"
            placeholder={t("business_channels.tg_dialogs_search")}
            leftSection={<IconSearch size={14} />}
            value={q}
            onChange={(e) => setQ(e.currentTarget.value)}
          />
          <SegmentedControl
            size="xs"
            value={kindFilter}
            onChange={(v) => setKindFilter(v as "all" | "direct" | "group")}
            data={[
              { value: "all", label: t("chat.filter_all") },
              { value: "direct", label: t("chat.filter_direct") },
              { value: "group", label: t("chat.filter_group") },
            ]}
          />
          <Tooltip label={t("business_channels.tg_refresh_dialogs")}>
            <ActionIcon variant="default" size="input-sm" loading={dialogsQuery.isFetching} onClick={() => void dialogsQuery.refetch()}>
              <IconRefresh size={16} />
            </ActionIcon>
          </Tooltip>
        </Group>

        {dialogsQuery.isPending ? (
          <Stack gap="xs" py="md">
            <Skeleton h={40} radius="sm" />
            <Skeleton h={40} radius="sm" />
            <Skeleton h={40} radius="sm" />
          </Stack>
        ) : dialogsQuery.isError ? (
          <Alert color="red" variant="light">
            <Group justify="space-between" align="center">
              <Text size="sm">{t("business_channels.tg_dialogs_failed")}</Text>
              <Button size="xs" variant="light" color="red" onClick={() => void dialogsQuery.refetch()}>
                {t("business_channels.tg_retry")}
              </Button>
            </Group>
          </Alert>
        ) : filtered.length === 0 ? (
          <Center py="xl">
            <Text size="sm" c="dimmed">
              {t("business_channels.tg_dialogs_empty")}
            </Text>
          </Center>
        ) : (
          <ScrollArea.Autosize mah={340} type="auto">
            <Stack gap={6}>
              {filtered.map((d) => {
                const checked = selected.has(d.external_chat_id)
                return (
                  <Paper
                    key={d.external_chat_id}
                    withBorder
                    px="sm"
                    py={8}
                    radius="sm"
                    style={{ cursor: "pointer" }}
                    onClick={() => toggleDialog(d)}
                  >
                    <Group gap="sm" wrap="nowrap">
                      <Checkbox checked={checked} onChange={() => {}} tabIndex={-1} style={{ pointerEvents: "none" }} />
                      <ThemeIcon size={28} radius="xl" variant="light" color={d.is_group ? "brand" : "bright-blue"} style={{ flex: "none" }}>
                        {d.is_group ? <IconUsersGroup size={14} /> : <IconUser size={14} />}
                      </ThemeIcon>
                      <Box style={{ flex: 1, minWidth: 0 }}>
                        <Group gap={6} wrap="nowrap">
                          <Text size="sm" fw={500} truncate>
                            {d.title}
                          </Text>
                          <Badge size="xs" variant="light" color={d.is_group ? "brand" : "gray"} style={{ flex: "none" }}>
                            {t(d.is_group ? "chat.mode_group" : "chat.mode_direct")}
                          </Badge>
                        </Group>
                        {d.username && (
                          <Text size="xs" c="dimmed" truncate>
                            @{d.username}
                          </Text>
                        )}
                      </Box>
                    </Group>
                  </Paper>
                )
              })}
            </Stack>
          </ScrollArea.Autosize>
        )}

        <Divider />

        <Group justify="space-between" align="center" wrap="wrap" gap="sm">
          <Checkbox
            size="xs"
            checked={backfill}
            onChange={(e) => setBackfill(e.currentTarget.checked)}
            label={t("business_channels.tg_backfill_label")}
          />
          <Group gap="xs">
            <Text size="xs" c="dimmed">
              {t("business_channels.tg_selected_count", { n: selected.size })}
            </Text>
            <Button variant="default" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button color="bright-blue" loading={save.isPending} onClick={() => save.mutate()}>
              {t("common.save")}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
