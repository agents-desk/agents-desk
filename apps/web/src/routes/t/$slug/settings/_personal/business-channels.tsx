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
  Code,
  ColorInput,
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
  Switch,
  Text,
  Textarea,
  TextInput,
  ThemeIcon,
  Title,
  Tooltip,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import {
  IconBrandTelegram,
  IconCheck,
  IconCopy,
  IconExternalLink,
  IconInfoCircle,
  IconLink,
  IconPencil,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconTrash,
  IconUser,
  IconUsersGroup,
} from "@tabler/icons-react"
import {
  applyServerErrors,
  confirm,
  FormError,
  notifyError,
  notifySuccess,
  Page,
  primaryColors,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { kit, tenantApi } from "../../../../../api"

/** presetSwatches 是主题预设色的十六进制色块，供商务沟通链接外观配色点选。 */
const presetSwatches = Object.values(primaryColors)

/** defaultLinkColor 是个人商务沟通链接默认主题色。 */
const defaultLinkColor = primaryColors["bright-blue"]

// 我的商务渠道：每个工作区成员自助管理自己的网页商务沟通链接与绑定的 Telegram 个人号（支持多个个人号与按对话白名单同步）。
export const Route = createFileRoute("/t/$slug/settings/_personal/business-channels")({
  staticData: { title: "nav.business_channels", icon: "link", order: 5, permission: "conversations:read" },
  component: BusinessChannelsPage,
})

function BusinessChannelsPage(): ReactNode {
  const t = useT()
  return (
    <Page title={t("nav.business_channels")} description={t("business_channels.description")}>
      <Stack gap="xl">
        <BusinessLinksSection />
        <TelegramAccountsSection />
      </Stack>
    </Page>
  )
}

/** BusinessLinksSection 管理当前成员的网页商务沟通链接。 */
function BusinessLinksSection(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const list = useQuery(kit.queries.business.linkList())
  const items = list.data ?? []

  const [editing, setEditing] = useState<WidgetBusinessLinkDTO | "new" | null>(null)
  const [formError, setFormError] = useState<ReturnType<typeof applyServerErrors>>()

  const form = useForm<{ name: string; greeting: string; color: string }>({
    initialValues: { name: "", greeting: "", color: defaultLinkColor },
  })

  const openCreate = () => {
    setFormError(undefined)
    form.setValues({ name: "", greeting: "", color: defaultLinkColor })
    setEditing("new")
  }

  const openEdit = (item: WidgetBusinessLinkDTO) => {
    setFormError(undefined)
    form.setValues({
      name: item.name,
      greeting: item.appearance.greeting ?? "",
      color: item.appearance.color || defaultLinkColor,
    })
    setEditing(item)
  }

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.business.key() })
    void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
  }

  const save = useMutation({
    mutationFn: (v: { name: string; greeting: string; color: string }) => {
      const appearance = { color: v.color || undefined, greeting: v.greeting.trim() || undefined }
      if (editing === "new") {
        return tenantApi.business.linkCreate({ name: v.name.trim(), appearance })
      }
      return tenantApi.business.linkUpdate((editing as WidgetBusinessLinkDTO).id, {
        name: v.name.trim(),
        enabled: (editing as WidgetBusinessLinkDTO).enabled,
        appearance,
      })
    },
    onSuccess: () => {
      notifySuccess(t(editing === "new" ? "business_channels.link_created" : "common.saved"))
      setEditing(null)
      invalidate()
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
    onSuccess: invalidate,
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
      confirmLabel: t("attributes.delete"),
    })
    if (ok) remove.mutate(item.id)
  }

  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
        <Box>
          <Group gap="xs">
            <ThemeIcon size={26} radius="md" variant="light" color="brand">
              <IconLink size={15} />
            </ThemeIcon>
            <Title order={4}>{t("business_channels.links_title")}</Title>
          </Group>
          <Text size="sm" c="dimmed" mt={4}>
            {t("business_channels.links_desc")}
          </Text>
        </Box>
        <Button size="sm" leftSection={<IconPlus size={15} />} onClick={openCreate}>
          {t("business_channels.link_new")}
        </Button>
      </Group>

      {list.isPending ? (
        <Stack gap="sm">
          <Skeleton h={88} radius="md" />
        </Stack>
      ) : items.length === 0 ? (
        <Paper withBorder p="lg" radius="md">
          <Group justify="space-between" align="center" wrap="wrap" gap="sm">
            <Text size="sm" c="dimmed">
              {t("business_channels.links_empty")}
            </Text>
            <Button size="xs" variant="light" leftSection={<IconPlus size={14} />} onClick={openCreate}>
              {t("business_channels.link_new")}
            </Button>
          </Group>
        </Paper>
      ) : (
        <Stack gap="sm">
          {items.map((item) => (
            <Paper key={item.id} withBorder p="md" radius="md">
              <Stack gap="sm">
                <Group justify="space-between" align="center" wrap="wrap" gap="sm">
                  <Group gap="sm" wrap="nowrap" style={{ minWidth: 0 }}>
                    <Box
                      w={10}
                      h={10}
                      style={{
                        borderRadius: "50%",
                        backgroundColor: item.appearance.color || "var(--mantine-primary-color-filled)",
                        flex: "none",
                      }}
                    />
                    <Text fw={600} size="sm" truncate>
                      {item.name}
                    </Text>
                    <Badge size="xs" variant="dot" color={item.enabled ? "green" : "gray"}>
                      {t(item.enabled ? "widgets.enabled" : "widgets.disabled")}
                    </Badge>
                  </Group>

                  <Group gap="xs" wrap="nowrap">
                    <Switch
                      size="sm"
                      checked={item.enabled}
                      disabled={toggleEnabled.isPending}
                      onChange={() => toggleEnabled.mutate(item)}
                      aria-label={t("widgets.enabled")}
                    />
                    <Tooltip label={t("contacts.edit")} openDelay={250}>
                      <ActionIcon variant="subtle" color="gray" onClick={() => openEdit(item)} aria-label={t("contacts.edit")}>
                        <IconPencil size={15} />
                      </ActionIcon>
                    </Tooltip>
                    <Tooltip label={t("attributes.delete")} openDelay={250}>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        disabled={remove.isPending}
                        onClick={() => void handleDelete(item)}
                        aria-label={t("attributes.delete")}
                      >
                        <IconTrash size={15} />
                      </ActionIcon>
                    </Tooltip>
                  </Group>
                </Group>

                {item.appearance.greeting && (
                  <Text size="xs" c="dimmed">
                    {item.appearance.greeting}
                  </Text>
                )}

                <Group gap="xs" wrap="nowrap" align="center">
                  <Code block style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {item.chat_url}
                  </Code>
                  <CopyButton value={item.chat_url} timeout={1500}>
                    {({ copied, copy }) => (
                      <Button
                        size="xs"
                        variant={copied ? "light" : "default"}
                        color={copied ? "green" : undefined}
                        leftSection={copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                        onClick={copy}
                      >
                        {t(copied ? "contacts.copied" : "business_channels.copy_url")}
                      </Button>
                    )}
                  </CopyButton>
                  <Button
                    size="xs"
                    variant="subtle"
                    component="a"
                    href={item.chat_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    rightSection={<IconExternalLink size={13} />}
                  >
                    {t("business_channels.open_preview")}
                  </Button>
                </Group>
              </Stack>
            </Paper>
          ))}
        </Stack>
      )}

      <Modal
        opened={editing !== null}
        onClose={() => setEditing(null)}
        title={t(editing === "new" ? "business_channels.link_create_title" : "business_channels.link_edit_title")}
      >
        <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
          <Stack gap="md">
            <FormError error={formError} />
            <TextInput
              label={t("business_channels.link_name")}
              placeholder={t("business_channels.link_name_placeholder")}
              required
              maxLength={60}
              data-autofocus
              {...form.getInputProps("name")}
            />
            <Textarea
              label={t("business_channels.link_greeting")}
              placeholder={t("business_channels.link_greeting_placeholder")}
              autosize
              minRows={2}
              maxRows={4}
              maxLength={300}
              {...form.getInputProps("greeting")}
            />
            <ColorInput
              label={t("business_channels.link_color")}
              swatches={presetSwatches}
              swatchesPerRow={7}
              {...form.getInputProps("color")}
            />
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

/** TelegramAccountsSection 管理当前成员绑定的一个或多个 Telegram 个人号。 */
function TelegramAccountsSection(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const list = useQuery(kit.queries.tgaccount.list())
  const configured = list.data?.configured ?? false
  const accounts = list.data?.accounts ?? []

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
    onSuccess: invalidate,
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
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
        <Box>
          <Group gap="xs">
            <ThemeIcon size={26} radius="md" variant="light" color="bright-blue">
              <IconBrandTelegram size={16} />
            </ThemeIcon>
            <Title order={4}>{t("business_channels.tg_title")}</Title>
          </Group>
          <Text size="sm" c="dimmed" mt={4}>
            {t("business_channels.tg_desc")}
          </Text>
        </Box>
        <Tooltip label={t("business_channels.tg_not_configured_short")} disabled={configured || list.isPending}>
          <Button
            size="sm"
            color="bright-blue"
            leftSection={<IconPlus size={15} />}
            disabled={!configured}
            onClick={() => setLoginOpen(true)}
          >
            {t("business_channels.tg_connect")}
          </Button>
        </Tooltip>
      </Group>

      {!list.isPending && !configured && (
        <Alert icon={<IconInfoCircle size={16} />} color="bright-blue" variant="light" title={t("business_channels.tg_not_configured_title")}>
          <Text size="sm">{t("business_channels.tg_not_configured_desc")}</Text>
        </Alert>
      )}

      {list.isPending ? (
        <Stack gap="sm">
          <Skeleton h={96} radius="md" />
        </Stack>
      ) : accounts.length === 0 ? (
        <Paper withBorder p="lg" radius="md">
          <Group justify="space-between" align="center" wrap="wrap" gap="sm">
            <Text size="sm" c="dimmed">
              {t("business_channels.tg_empty")}
            </Text>
            {configured && (
              <Button size="xs" variant="light" color="bright-blue" leftSection={<IconPlus size={14} />} onClick={() => setLoginOpen(true)}>
                {t("business_channels.tg_connect")}
              </Button>
            )}
          </Group>
        </Paper>
      ) : (
        <Stack gap="sm">
          {accounts.map((acc) => {
            const statusColor = !acc.enabled
              ? "gray"
              : acc.status === "connected"
                ? "green"
                : acc.status === "auth_expired"
                  ? "red"
                  : "gray"
            const statusLabel = !acc.enabled
              ? t("widgets.disabled")
              : acc.status === "connected"
                ? t("business_channels.tg_status_connected")
                : acc.status === "auth_expired"
                  ? t("business_channels.tg_status_expired")
                  : t("business_channels.tg_status_disconnected")
            const directCount = acc.synced_peers.filter((p) => !p.is_group).length
            const groupCount = acc.synced_peers.filter((p) => p.is_group).length
            const identityMeta = [acc.display_name, acc.username ? `@${acc.username}` : "", acc.phone].filter(Boolean).join(" · ")

            return (
              <Paper key={acc.id} withBorder p="md" radius="md">
                <Stack gap="sm">
                  <Group justify="space-between" align="center" wrap="wrap" gap="sm">
                    <Box style={{ minWidth: 0 }}>
                      <Group gap="xs" wrap="nowrap">
                        <Text fw={600} size="sm" truncate>
                          {acc.name}
                        </Text>
                        <Badge size="xs" variant="dot" color={statusColor}>
                          {statusLabel}
                        </Badge>
                      </Group>
                      {identityMeta && (
                        <Text size="xs" c="dimmed" mt={2}>
                          {identityMeta}
                        </Text>
                      )}
                    </Box>

                    <Group gap="xs" wrap="nowrap">
                      <Switch
                        size="sm"
                        checked={acc.enabled}
                        disabled={toggleEnabled.isPending}
                        onChange={() => toggleEnabled.mutate(acc)}
                        aria-label={t("widgets.enabled")}
                      />
                      <Button
                        size="xs"
                        variant="light"
                        color="bright-blue"
                        disabled={!acc.enabled || acc.status === "auth_expired"}
                        onClick={() => setPeersTarget(acc)}
                      >
                        {t("business_channels.tg_pick_dialogs")}
                      </Button>
                      {acc.status === "auth_expired" && configured && (
                        <Button size="xs" variant="light" color="deep-orange" onClick={() => setLoginOpen(true)}>
                          {t("business_channels.tg_relogin")}
                        </Button>
                      )}
                      <Tooltip label={t("business_channels.tg_rename")} openDelay={250}>
                        <ActionIcon
                          variant="subtle"
                          color="gray"
                          onClick={() => {
                            setRenaming(acc)
                            setRenameValue(acc.name)
                          }}
                          aria-label={t("business_channels.tg_rename")}
                        >
                          <IconPencil size={15} />
                        </ActionIcon>
                      </Tooltip>
                      <Tooltip label={t("business_channels.tg_disconnect")} openDelay={250}>
                        <ActionIcon
                          variant="subtle"
                          color="red"
                          disabled={disconnect.isPending}
                          onClick={() => void handleDisconnect(acc)}
                          aria-label={t("business_channels.tg_disconnect")}
                        >
                          <IconTrash size={15} />
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                  </Group>

                  {acc.last_error && (
                    <Alert color="red" variant="light" py={6} px="sm">
                      <Text size="xs">{acc.last_error}</Text>
                    </Alert>
                  )}

                  <Group gap="xs" wrap="wrap" align="center">
                    <Text size="xs" c="dimmed">
                      {acc.synced_peers.length === 0
                        ? t("business_channels.tg_no_synced_peers")
                        : t("business_channels.tg_synced_summary", {
                            total: acc.synced_peers.length,
                            direct: directCount,
                            groups: groupCount,
                          })}
                    </Text>
                    {acc.synced_peers.slice(0, 6).map((p) => (
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
                    {acc.synced_peers.length > 6 && (
                      <Text size="xs" c="dimmed">
                        +{acc.synced_peers.length - 6}
                      </Text>
                    )}
                  </Group>
                </Stack>
              </Paper>
            )
          })}
        </Stack>
      )}

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
              <IconRefresh size={15} />
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
