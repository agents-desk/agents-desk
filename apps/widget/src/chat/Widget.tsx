import { Box, Center, Loader, Text } from "@mantine/core"
import { useQueryClient } from "@tanstack/react-query"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { channel, embedded, hasCloseButton, type Init, inline, listen, tell, waitInit } from "../host"
import { identity } from "../identity"
import { useIdentity, useRealtime } from "../realtime"
import { DEFAULT_WIDGET_COLOR, useSetWidgetBrand } from "../theme"
import { ConversationView } from "./Conversation"
import { Header } from "./Header"
import { FirstMessage, Home } from "./Home"
import { Launcher } from "./Launcher"
import { useConfig, useConversations, useConversation } from "./queries"
import { useViewportHeight } from "./viewport"

/** started 是页面的初始化：整个页面只做一次（开发时 React 的 StrictMode 会把 effect 执行两遍）。 */
let started: Promise<Init> | undefined

/**
 * initOnce 拿到外面给出的初始状态；独立模式、容器内联模式、已登录用户或本地已有访客记录时立即建立/恢复会话，
 * 浮窗收起态的全新匿名访客保持静默，等到第一次打开聊天窗口时再建立线索。
 */
function initOnce(): Promise<Init> {
  started ??= waitInit().then(async (init) => {
    await identity.start(init.userToken, init.referrer, !embedded || inline)
    if (!embedded) tell("ready")
    return init
  })
  return started
}

/** View 是聊天窗口当前显示的页面：首页、某个会话，或者还没有发出第一条的新会话。 */
type View = { kind: "home" } | { kind: "conversation"; id: string } | { kind: "new" }

/**
 * Widget 是整个聊天窗口。四种运行方式：
 *   - 嵌入电脑网页：收起时只有右下角的启动按钮；展开后是浮层，下面是收起按钮。iframe 的大小由 loader.js 按 open 和 close 调整；
 *   - 嵌入手机网页：展开后占满屏幕，右上角关闭；
 *   - 容器内联（embed=1&inline=1）：铺满父页面容器，一直展开，通过 postMessage 与宿主通信；
 *   - 独立（App 的 WebView、直接打开）：一直占满屏幕，有 App 的桥时右上角的关闭按钮通知 App。
 * 拿到外面给出的初始状态（用户身份、是不是手机）后建立会话，之后按外面的命令展开、收起、登录、退出。
 */
export function Widget(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [ready, setReady] = useState(false)
  const [failed, setFailed] = useState(false)
  const floating = embedded && !inline
  const [mobile, setMobile] = useState(!floating)
  const [open, setOpen] = useState(!floating)
  const openRef = useRef(open)
  openRef.current = open
  const [view, setView] = useState<View>({ kind: "home" })
  const [visible, setVisible] = useState(() => typeof document === "undefined" || document.visibilityState === "visible")
  const height = useViewportHeight()
  const { contact } = useIdentity()
  const config = useConfig()
  const conversations = useConversations()
  useRealtime()

  // toggle 展开或收起，并告诉 loader.js 调整 iframe 的大小；展开时确保已激活身份（全新访客此时才建立线索）
  const toggle = (next: boolean) => {
    openRef.current = next
    setOpen(next)
    tell(next ? "open" : "close")
    if (next) void identity.ensure().catch(() => setFailed(true))
  }

  // 初始状态：嵌入时等父页面 init，独立时读 App 设置的 window.agentsDesk；然后按条件建立或延迟建立会话
  useEffect(() => {
    void initOnce()
      .then((init) => {
        if (floating) setMobile(init.mobile)
        setReady(true)
        // 如果用户在握手期间就点开了窗口，补上激活身份
        if (openRef.current) void identity.ensure().catch(() => setFailed(true))
      })
      .catch(() => setFailed(true))
  }, [])

  // 外面的命令：展开、收起、登录、退出；同用户续签 token 静默刷新不打断当前聊天页，换人或退出才回到首页
  useEffect(
    () =>
      listen({
        layout: (m) => {
          if (floating) setMobile(m)
        },
        command: (c) => {
          // 展开和收起只在浮窗嵌入网页时有意义；容器内联和独立模式一直占满容器
          if (c.command === "open" || c.command === "close") {
            if (floating) toggle(c.command === "open")
            return
          }
          if (c.command === "login") {
            void identity
              .login(c.user_token)
              .then((switched) => {
                if (!switched) return
                setView({ kind: "home" })
                void queryClient.invalidateQueries({ queryKey: ["widget"] })
              })
              .catch(() => setFailed(true))
            return
          }
          setView({ kind: "home" })
          void identity
            .logout(openRef.current)
            .then(() => queryClient.invalidateQueries({ queryKey: ["widget"] }))
            .catch(() => setFailed(true))
        },
      }),
    [floating, queryClient],
  )

  useEffect(() => {
    const onVisibility = () => setVisible(document.visibilityState === "visible")
    document.addEventListener("visibilitychange", onVisibility)
    return () => document.removeEventListener("visibilitychange", onVisibility)
  }, [])

  // 未读数：启动按钮的角标；嵌入时告诉 loader.js，App 中告诉 App
  const unread = (conversations.data ?? []).reduce((n, c) => n + c.unread, 0)
  useEffect(() => {
    if (ready) tell("unread", { count: unread })
  }, [ready, unread])

  const setBrandColor = useSetWidgetBrand()
  const rawColor = config.data?.color ?? DEFAULT_WIDGET_COLOR
  useEffect(() => {
    setBrandColor(rawColor)
  }, [rawColor, setBrandColor])

  const color = "brand"
  const name = config.data?.name ?? t("app.widget")
  if (!channel || config.isError || failed) {
    return floating ? null : (
      <Center h="100dvh" p="xl">
        <Text c="dimmed" ta="center">
          {t("panel.unavailable")}
        </Text>
      </Center>
    )
  }
  if (floating && !open) {
    return (
      <Box className="w-dock">
        <Launcher color={color} open={false} unread={unread} onClick={() => toggle(true)} />
      </Box>
    )
  }

  const full = mobile || !floating // 占满屏幕或容器：手机、容器内联和 App
  const close = floating ? (full ? "close" : "collapse") : hasCloseButton() ? "close" : undefined
  const onClose = () => (floating ? toggle(false) : tell("close"))
  const panel = (
    <Box
      className="w-panel"
      data-full={full || undefined}
      style={full && height ? { height } : undefined}
    >
      {!ready || !config.data || !contact || !conversations.data ? (
        <Center style={{ flex: 1 }}>
          <Loader color={color} />
        </Center>
      ) : view.kind === "home" ? (
        <Box key="home" className="w-view">
          <Header color={color} title={name} greeting={config.data.greeting || t("home.default_greeting")} close={close} onClose={onClose} />
          {conversations.data.length > 0 ? (
            <Home
              color={color}
              name={name}
              conversations={conversations.data}
              onOpen={(id) => setView({ kind: "conversation", id })}
              onNew={() => setView({ kind: "new" })}
            />
          ) : (
            <ConversationView
              intro={<FirstMessage />}
              color={color}
              name={name}
              touch={full}
              visible={open && visible}
              onStarted={(id) => setView({ kind: "conversation", id })}
              onBack={() => setView({ kind: "home" })}
            />
          )}
        </Box>
      ) : (
        <Box key={view.kind === "conversation" ? view.id : "new"} className="w-view">
          <ConversationPage
            id={view.kind === "conversation" ? view.id : undefined}
            color={color}
            name={name}
            full={full}
            visible={open && visible}
            close={close}
            onClose={onClose}
            onView={setView}
          />
        </Box>
      )}
    </Box>
  )
  if (full) return panel
  return (
    <Box className="w-dock w-dock-open">
      {panel}
      <Launcher color={color} open unread={0} onClick={() => toggle(false)} />
    </Box>
  )
}

/** ConversationPage 是会话页：顶部有返回首页和会话的状态，下面是会话。 */
function ConversationPage({
  id,
  color,
  name,
  full,
  visible,
  close,
  onClose,
  onView,
}: {
  id?: string
  color: string
  name: string
  full: boolean
  visible: boolean
  close?: "collapse" | "close"
  onClose: () => void
  onView: (v: View) => void
}): ReactNode {
  const t = useT()
  const conversation = useConversation(id ?? "")
  const status = !id ? t("chat.new") : conversation.data?.status === "closed" ? t("chat.status_closed") : t("chat.status_open")
  return (
    <>
      <Header color={color} title={name} status={status} onBack={() => onView({ kind: "home" })} close={close} onClose={onClose} />
      <ConversationView
        id={id}
        intro={<FirstMessage />}
        color={color}
        name={name}
        touch={full}
        visible={visible}
        onStarted={(next) => onView({ kind: "conversation", id: next })}
        onBack={() => onView({ kind: "home" })}
      />
    </>
  )
}
