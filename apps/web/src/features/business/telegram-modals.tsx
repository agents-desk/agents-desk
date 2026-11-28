import type {
  TgaccountAccountDTO,
  TgaccountDialogDTO,
  TgaccountLoginMode,
  TgaccountLoginSessionDTO,
  TgaccountSyncedPeer,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Center,
  Checkbox,
  Divider,
  Group,
  Loader,
  Modal,
  Paper,
  PasswordInput,
  ScrollArea,
  SegmentedControl,
  Select,
  Skeleton,
  Stack,
  Text,
  TextInput,
  ThemeIcon,
  Tooltip,
} from "@mantine/core"
import type { ErrorInfo } from "@go-tenon/client"
import { describeError } from "@go-tenon/client"
import { useMutation, useQuery } from "@tanstack/react-query"
import { IconRefresh, IconSearch, IconUser, IconUsersGroup } from "@tabler/icons-react"
import { EmptyState, FormError, notifySuccess, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { kit, tenantApi } from "../../api"

/** TelegramLoginModal 是绑定或原地重新授权 Telegram 个人号的交互向导，同时支持扫码登录与手机号验证码 + 两步验证登录。 */
export function TelegramLoginModal({
  opened,
  reconnectAccount,
  onClose,
  onConnected,
}: {
  opened: boolean
  reconnectAccount?: TgaccountAccountDTO | null
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
  const [formError, setFormError] = useState<ErrorInfo>()

  useEffect(() => {
    setFormError(undefined)
    if (!opened) {
      setMode("qr")
      setName("")
      setPhone("")
      setCode("")
      setPassword("")
      setSession(null)
    } else if (reconnectAccount) {
      setName(reconnectAccount.name)
    }
  }, [opened, reconnectAccount])

  const startLogin = useMutation({
    mutationFn: () =>
      tenantApi.tgaccount.loginStart({
        mode,
        name: name.trim() || undefined,
        phone: mode === "phone" ? phone.trim() : undefined,
        account_id: reconnectAccount?.id,
      }),
    onSuccess: (next) => {
      setFormError(undefined)
      setSession(next)
      if (next.step === "connected" && next.account) {
        notifySuccess(t("business_channels.tg_connected"))
        onConnected(next.account)
      }
    },
    onError: (err) => setFormError(describeError(err)),
  })

  const submitCode = useMutation({
    mutationFn: () => tenantApi.tgaccount.loginCode(session!.login_id, { code: code.trim() }),
    onSuccess: (next) => {
      setFormError(undefined)
      setCode("")
      setSession(next)
      if (next.step === "connected" && next.account) {
        notifySuccess(t("business_channels.tg_connected"))
        onConnected(next.account)
      }
    },
    onError: (err) => setFormError(describeError(err)),
  })

  const submitPassword = useMutation({
    mutationFn: () => tenantApi.tgaccount.loginPassword(session!.login_id, { password }),
    onSuccess: (next) => {
      setFormError(undefined)
      setPassword("")
      setSession(next)
      if (next.step === "connected" && next.account) {
        notifySuccess(t("business_channels.tg_connected"))
        onConnected(next.account)
      }
    },
    onError: (err) => setFormError(describeError(err)),
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

  const modalTitle = reconnectAccount
    ? t("tg_studio.reauth_title", { name: reconnectAccount.name })
    : t("business_channels.tg_connect_title")

  return (
    <Modal opened={opened} onClose={onClose} closeOnClickOutside={false} title={modalTitle} size="md">
      {!session ? (
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            setFormError(undefined)
            startLogin.mutate()
          }}
        >
          <Stack gap="md">
            <FormError error={formError} />
            {reconnectAccount && (
              <Text size="xs" c="dimmed">
                {t("tg_studio.reauth_hint")}
              </Text>
            )}
            <SegmentedControl
              fullWidth
              value={mode}
              onChange={(v) => setMode(v as TgaccountLoginMode)}
              data={[
                { value: "qr", label: t("business_channels.tg_mode_qr") },
                { value: "phone", label: t("business_channels.tg_mode_phone") },
              ]}
            />
            {!reconnectAccount && (
              <TextInput
                label={t("business_channels.tg_account_name")}
                placeholder={t("business_channels.tg_account_name_placeholder")}
                value={name}
                onChange={(e) => setName(e.currentTarget.value)}
                maxLength={60}
              />
            )}
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
            <Group justify="flex-end" gap="xs">
              <Button variant="default" onClick={onClose}>
                {t("common.cancel")}
              </Button>
              <Button
                type="submit"
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
              <img
                src={session.qr_data_url}
                alt={t("business_channels.tg_mode_qr")}
                width={220}
                height={220}
                style={{ display: "block", imageRendering: "pixelated" }}
              />
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
          <Group justify="flex-end" w="100%" gap="xs">
            <Button variant="default" onClick={() => { setFormError(undefined); setSession(null) }}>
              {t("business_channels.tg_back")}
            </Button>
          </Group>
        </Stack>
      ) : session.step === "awaiting_code" ? (
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (!code.trim()) return
            setFormError(undefined)
            submitCode.mutate()
          }}
        >
          <Stack gap="md">
            <FormError error={formError} />
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
            <Group justify="flex-end" gap="xs">
              <Button variant="default" onClick={() => { setFormError(undefined); setSession(null) }}>
                {t("business_channels.tg_back")}
              </Button>
              <Button type="submit" loading={submitCode.isPending} disabled={!code.trim()}>
                {t("business_channels.tg_verify")}
              </Button>
            </Group>
          </Stack>
        </form>
      ) : session.step === "awaiting_password" ? (
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (!password) return
            setFormError(undefined)
            submitPassword.mutate()
          }}
        >
          <Stack gap="md">
            <FormError error={formError} />
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
            <Group justify="flex-end" gap="xs">
              <Button variant="default" onClick={() => { setFormError(undefined); setSession(null) }}>
                {t("business_channels.tg_back")}
              </Button>
              <Button type="submit" loading={submitPassword.isPending} disabled={!password}>
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
          <Group justify="flex-end" gap="xs">
            <Button variant="default" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button onClick={() => { setFormError(undefined); setSession(null) }}>
              {t("business_channels.tg_retry")}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}

/** SyncedPeersModal 让成员从已绑定 Telegram 个人号中挑选需要同步到商务沟通的 1:1 单聊与群聊（支持搜索、类型筛选与历史回填条数）。 */
export function SyncedPeersModal({
  account,
  onClose,
  onSaved,
}: {
  account: TgaccountAccountDTO | null
  onClose: () => void
  onSaved: (updated: TgaccountAccountDTO) => void
}): ReactNode {
  const t = useT()
  const [q, setQ] = useState("")
  const [kindFilter, setKindFilter] = useState<"all" | "direct" | "group">("all")
  const [selected, setSelected] = useState<Map<string, TgaccountSyncedPeer>>(new Map())
  const [backfillLimit, setBackfillLimit] = useState<string>("20")
  const [saveError, setSaveError] = useState<ErrorInfo>()

  const dialogsQuery = useQuery({
    ...kit.queries.tgaccount.dialogs(account?.id ?? ""),
    enabled: Boolean(account),
  })

  useEffect(() => {
    setSaveError(undefined)
    if (!account) return
    setQ("")
    setKindFilter("all")
    const defLimit = account.sync?.backfill_limit ?? 20
    setBackfillLimit(defLimit === 0 ? "0" : defLimit >= 50 ? "50" : "20")
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
        backfill_limit: Number(backfillLimit) || 0,
      }),
    onSuccess: (updated) => {
      setSaveError(undefined)
      notifySuccess(t("business_channels.tg_peers_saved"))
      onSaved(updated)
    },
    onError: (err) => setSaveError(describeError(err)),
  })

  return (
    <Modal
      opened={account !== null}
      onClose={onClose}
      closeOnClickOutside={false}
      title={account ? t("business_channels.tg_pick_dialogs_title", { name: account.name }) : ""}
      size="lg"
    >
      <form
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          setSaveError(undefined)
          save.mutate()
        }}
      >
        <Stack gap="md">
          <FormError error={saveError} />
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
              value={kindFilter}
              onChange={(v) => setKindFilter(v as "all" | "direct" | "group")}
              data={[
                { value: "all", label: t("chat.filter_all") },
                { value: "direct", label: t("chat.filter_direct") },
                { value: "group", label: t("chat.filter_group") },
              ]}
            />
            <Tooltip label={t("business_channels.tg_refresh_dialogs")}>
              <ActionIcon
                variant="default"
                size="input-sm"
                aria-label={t("business_channels.tg_refresh_dialogs")}
                loading={dialogsQuery.isFetching}
                onClick={() => void dialogsQuery.refetch()}
              >
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
            <EmptyState title={t("business_channels.tg_dialogs_empty")} />
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
                      role="checkbox"
                      tabIndex={0}
                      aria-checked={checked}
                      style={{ cursor: "pointer" }}
                      onClick={() => toggleDialog(d)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault()
                          toggleDialog(d)
                        }
                      }}
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
            <Group gap="xs" align="center">
              <Text size="xs" c="dimmed">
                {t("tg_studio.modal_backfill_label")}
              </Text>
              <Select
                size="xs"
                w={136}
                aria-label={t("tg_studio.modal_backfill_label")}
                value={backfillLimit}
                onChange={(v) => setBackfillLimit(v ?? "20")}
                data={[
                  { value: "0", label: t("tg_studio.backfill_0") },
                  { value: "20", label: t("tg_studio.backfill_20") },
                  { value: "50", label: t("tg_studio.backfill_50") },
                ]}
              />
            </Group>
            <Group gap="xs">
              <Text size="xs" c="dimmed">
                {t("business_channels.tg_selected_count", { n: selected.size })}
              </Text>
              <Button variant="default" onClick={onClose}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={save.isPending}>
                {t("common.save")}
              </Button>
            </Group>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
