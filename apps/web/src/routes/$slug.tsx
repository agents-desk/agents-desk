import { Alert, Button, Group, Stack, Text } from "@mantine/core"
import { useDisclosure } from "@mantine/hooks"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconAlertTriangle, IconBell, IconSettings, IconWifiOff } from "@tabler/icons-react"
import {
  enterTenant,
  Fill,
  ImpersonationBanner,
  OperationsMenu,
  RailLayout,
  requireAuth,
  TimeZoneProvider,
  UserMenu,
  useApp,
  useCan,
  useFavicon,
  useMenuBadge,
  useT,
} from "@go-tenon/ui"
import { useEffect, useState } from "react"
import { apiKit, kit, tenantApi } from "../api"
import { logout, me } from "../auth"
import { CommandPalette, CommandSearchButton } from "../command-palette"
import { dispatchPushAlert } from "../inbox/alerts"
import { NARROW_QUERY } from "../layout"
import { RealtimeContext, useRealtime, useRealtimeState } from "../realtime"
import { ShortcutsModal } from "../shortcuts-modal"
import { memberStatusColor, StatusControl } from "../status"
import { writeLastWorkspace } from "../workspace"
import { WorkspaceSwitcher } from "../workspace-switcher"

// 工作区的布局路由：地址 /app/{slug}/ 中的 slug 就是当前工作区。进入时会话身份切换到这个工作区，
// 再读取工作区和成员的权限：不是成员（含 slug 不存在）时是 404，工作区停用时是 403，由 RouteError 显示。
export const Route = createFileRoute("/$slug")({
  beforeLoad: async (ctx) => {
    await requireAuth(ctx)
    enterTenant(ctx.context.app, ctx.params.slug)
  },
  loader: () => tenantApi.tenant.current(),
  component: TenantLayout,
})

function TenantLayout() {
  const tenant = Route.useLoaderData()
  const app = useApp()
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  const site = useQuery(apiKit.queries.site.get())
  useFavicon(tenant.logo_url || site.data?.logo_url || undefined)
  const myStatus = useQuery({ queryKey: ["me", "status", tenant.slug], queryFn: () => tenantApi.member.myStatus() })
  const can = useCan()
  const navigate = useNavigate()
  const t = useT()
  const [shortcutsOpened, shortcutsHandlers] = useDisclosure(false)
  const [exitingImpersonation, setExitingImpersonation] = useState(false)
  const [notifDismissed, setNotifDismissed] = useState(
    () => typeof window !== "undefined" && localStorage.getItem("agents-desk:notif-prompt-dismissed") === "1",
  )
  const [notifPermission, setNotifPermission] = useState<NotificationPermission | "unsupported">(() =>
    typeof Notification === "undefined" ? "unsupported" : Notification.permission,
  )
  const impersonation = user.data?.impersonation

  // 普通成员进入工作区后记录为上次访问的工作区，供下次打开 /app/ 直接进入；代为登录不覆写该记录。
  useEffect(() => {
    if (!user.data || impersonation) return
    writeLastWorkspace(app, user.data.id, tenant.slug)
  }, [app, impersonation, tenant.slug, user.data])

  // 在线心跳：成员处于在线状态且页签可见时每 60 秒保活一次，切回前台立即同步一次。
  const isOnline = myStatus.data?.status === "online"
  useEffect(() => {
    if (impersonation || !isOnline) return
    const ping = () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return
      void tenantApi.member.heartbeat().catch(() => {
        // 心跳为尽力而为的保活请求，失败后等待下个周期
      })
    }
    const timer = setInterval(ping, 60_000)
    const onVisible = () => {
      if (document.visibilityState === "visible") ping()
    }
    document.addEventListener("visibilitychange", onVisible)
    return () => {
      clearInterval(timer)
      document.removeEventListener("visibilitychange", onVisible)
    }
  }, [impersonation, isOnline])

  // 退出代为登录：撤销当前代登令牌并清除独立的 __Host-impersonation cookie，然后回到管理员端。
  const exitImpersonation = async () => {
    setExitingImpersonation(true)
    try {
      await logout()
      window.location.assign("/admin/tenants")
    } finally {
      setExitingImpersonation(false)
    }
  }

  // 实时推送：会话有变化时刷新收件箱的数量和会话，命中提醒时按通知偏好播放提示音或弹桌面通知；
  // 聊天和工单都在收件箱中，图标栏只有收件箱一个角标：“我的”中等待回复的会话数加上有未读消息的聊天数，即等着自己处理的事。
  const centrifuge = useRealtime(tenant.slug, tenant.id, user.data?.id, ({ type, id, alert }) => {
    dispatchPushAlert({
      slug: tenant.slug,
      kind: type,
      conversationId: id,
      alert,
      settings: user.data?.notifications,
      t,
      onOpen: (convId) => {
        if (type === "chat") {
          void navigate({ to: "/$slug/inbox/chat/$id", params: { slug: tenant.slug, id: convId } })
          return
        }
        if (type === "ticket") {
          void navigate({
            to: "/$slug/inbox/tickets/$id",
            params: { slug: tenant.slug, id: convId },
            search: { box: "all", status: "unresolved" },
          })
          return
        }
        void navigate({
          to: "/$slug/inbox/$id",
          params: { slug: tenant.slug, id: convId },
          search: { box: "all", status: "open", sort: "waiting" },
        })
      },
    })
  })
  const inbox = useQuery({ ...kit.queries.conversation.inbox(), enabled: can("conversations:read") })
  const chatUnread = useQuery({ ...kit.queries.chat.unread(), enabled: can("conversations:read") })
  const waitingOnMe = inbox.data && chatUnread.data ? inbox.data.mine.waiting + chatUnread.data.unread_chats : undefined
  useMenuBadge("/$slug/inbox", waitingOnMe)

  const realtimeState = useRealtimeState(centrifuge)
  const showRealtimeBanner =
    realtimeState.offline || (Boolean(user.data?.id) && realtimeState.everConnected && realtimeState.status !== "connected")

  const notifPref = user.data?.notifications
  const browserNotifWanted = Boolean(notifPref?.assigned.browser || notifPref?.new_message.browser || notifPref?.mention.browser)
  const showNotifPrompt = !impersonation && browserNotifWanted && notifPermission === "default" && !notifDismissed

  const requestDesktopNotif = async () => {
    if (typeof Notification === "undefined") return
    const result = await Notification.requestPermission()
    setNotifPermission(result)
  }
  const dismissNotifPrompt = () => {
    setNotifDismissed(true)
    if (typeof window !== "undefined") {
      localStorage.setItem("agents-desk:notif-prompt-dismissed", "1")
    }
  }

  const banners: React.ReactNode[] = []
  if (impersonation && user.data) {
    banners.push(
      <ImpersonationBanner
        key="impersonation"
        target={user.data.name}
        actor={impersonation.actor_name || undefined}
        tenant={tenant.name}
        readOnly={impersonation.read_only}
        exiting={exitingImpersonation}
        onExit={exitImpersonation}
      />,
    )
  }
  if (showRealtimeBanner) {
    const severe = realtimeState.offline || realtimeState.status === "disconnected"
    banners.push(
      <Alert
        key="realtime"
        variant="light"
        color={severe ? "red" : "yellow"}
        radius={0}
        py={6}
        px="md"
        icon={severe ? <IconWifiOff size={16} /> : <IconAlertTriangle size={16} />}
      >
        <Group justify="space-between" gap="sm" wrap="wrap">
          <Text size="xs">
            {realtimeState.offline
              ? t("layout.realtime_offline")
              : realtimeState.status === "connecting"
                ? t("layout.realtime_reconnecting")
                : t("layout.realtime_disconnected")}
          </Text>
          {!realtimeState.offline && realtimeState.status === "disconnected" && (
            <Button size="compact-xs" variant="light" color="red" onClick={realtimeState.reconnect}>
              {t("layout.realtime_reconnect")}
            </Button>
          )}
        </Group>
      </Alert>,
    )
  }
  if (showNotifPrompt) {
    banners.push(
      <Alert key="notif-prompt" variant="light" color="bright-blue" radius={0} py={6} px="md" icon={<IconBell size={16} />}>
        <Group justify="space-between" gap="sm" wrap="wrap">
          <Text size="xs">{t("notifications_prompt.text")}</Text>
          <Group gap={6} wrap="nowrap">
            <Button size="compact-xs" variant="light" color="bright-blue" onClick={() => void requestDesktopNotif()}>
              {t("notifications_prompt.allow")}
            </Button>
            <Button size="compact-xs" variant="subtle" color="gray" onClick={dismissNotifPrompt}>
              {t("notifications_prompt.dismiss")}
            </Button>
          </Group>
        </Group>
      </Alert>,
    )
  }

  // 左上角由 WorkspaceSwitcher 管理工作区切换与工作区入口，左下角个人菜单聚焦个人设置、在线状态与退出登录。
  const settings = `/${tenant.slug}/settings`
  const items = [{ label: t("nav.settings"), icon: <IconSettings size={16} />, to: settings }]
  const statusColor = myStatus.data ? memberStatusColor[myStatus.data.status] : undefined

  return (
    <TimeZoneProvider zone={user.data?.time_zone}>
      <RealtimeContext.Provider value={centrifuge}>
        <RailLayout
          title={tenant.name}
          documentTitle={false}
          titleBadge={waitingOnMe}
          logo={<WorkspaceSwitcher tenant={tenant} impersonation={Boolean(impersonation)} />}
          settings={settings}
          navWidth={200}
          compactQuery={NARROW_QUERY}
          railTop={<CommandSearchButton />}
          railBottom={<OperationsMenu />}
          banner={banners.length > 0 ? <Stack gap={0}>{banners}</Stack> : undefined}
          user={
            <UserMenu
              name={user.data?.name}
              email={user.data?.email}
              avatar={user.data?.avatar_url || undefined}
              statusColor={statusColor}
              position="right-end"
              items={items}
              logout={logout}
            />
          }
        />
        <CommandPalette slug={tenant.slug} impersonation={Boolean(impersonation)} onShowShortcuts={shortcutsHandlers.open} />
        <ShortcutsModal opened={shortcutsOpened} onClose={shortcutsHandlers.close} onToggle={shortcutsHandlers.toggle} />
        {/* 个人菜单中切换我在这个工作区的在线状态 */}
        <Fill name="user-menu">
          <div className="tn-menu-section">
            <StatusControl />
          </div>
        </Fill>
      </RealtimeContext.Provider>
    </TimeZoneProvider>
  )
}
