import { isApiError } from "@agents-desk/client/widget"
import { Box, Button, Center, Indicator, Loader, Paper, Text } from "@mantine/core"
import { IconTicket } from "@tabler/icons-react"
import { useQueryClient } from "@tanstack/react-query"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { channel, embedded, hasCloseButton, type Init, inline, listen, tell, waitInit } from "../host"
import {
  applyChannelDefaultLanguage,
  applyHostCommandLanguage,
  applyHostInitLanguage,
  i18n,
} from "../i18n"
import { identity } from "../identity"
import { useIdentity, useRealtime } from "../realtime"
import { DEFAULT_WIDGET_COLOR, useSetWidgetBrand } from "../theme"
import { BusinessCardHeader } from "./BusinessCardHeader"
import { ExpiredView, IntakeForm, needsIntake, PasscodeGate } from "./BusinessGates"
import { useBrandForeground } from "./contrast"
import { ConversationView } from "./Conversation"
import { Header } from "./Header"
import { Launcher } from "./Launcher"
import { useConfig, useTicket, useTickets, useTimeline } from "./queries"
import { type ReplyTicketContext, TicketDetailView, TicketListItems } from "./TicketDetail"
import { useViewportHeight } from "./viewport"

/** View 是聊天窗口当前显示的视图：统一连续对话流、工单列表或单张工单详情。 */
type View =
  | { kind: "chat" }
  | { kind: "tickets" }
  | { kind: "ticket"; id: string; from: "chat" | "tickets" }

/**
 * Widget 是整个聊天窗口（同时承载公共网页聊天插件与个人网页商务沟通链接）。
 * 对于受口令保护或已过期的商务沟通链接，先等待公开配置到达再决定是展示门禁还是直接建立会话。
 */
export function Widget(): ReactNode {
  const t = useT()
  const fg = useBrandForeground()
  const queryClient = useQueryClient()
  const [ready, setReady] = useState(false)
  const [failed, setFailed] = useState(false)
  const [gate, setGate] = useState<"none" | "passcode" | "expired">("none")
  const [intakeSkipped, setIntakeSkipped] = useState(false)
  const [intakeCompleted, setIntakeCompleted] = useState(false)
  const floating = embedded && !inline
  const [mobile, setMobile] = useState(!floating)
  const [open, setOpen] = useState(!floating)
  const openRef = useRef(open)
  openRef.current = open
  const initStartedRef = useRef(false)
  const [view, setView] = useState<View>({ kind: "chat" })
  const [replyTicket, setReplyTicket] = useState<ReplyTicketContext | null>(null)
  const [visible, setVisible] = useState(() => typeof document === "undefined" || document.visibilityState === "visible")
  const height = useViewportHeight()
  const { contact } = useIdentity()
  const config = useConfig()
  const timeline = useTimeline()
  const tickets = useTickets()
  useRealtime()

  const handleSessionError = (err: unknown) => {
    if (isApiError(err)) {
      if (err.code === "widget.invalid_passcode") {
        setGate("passcode")
        return
      }
      if (err.code === "widget.business_link_expired") {
        setGate("expired")
        return
      }
    }
    setFailed(true)
  }

  // toggle 展开或收起，并告诉 loader.js 调整 iframe 的大小；展开时确保已激活身份
  const toggle = (next: boolean) => {
    openRef.current = next
    setOpen(next)
    tell(next ? "open" : "close")
    if (next) void identity.ensure().catch(handleSessionError)
  }

  // 公开配置到达后应用渠道默认语言（仅作为无显式/浏览器命中时的回退）
  const defaultLanguage = config.data?.default_language
  useEffect(() => {
    if (defaultLanguage) void applyChannelDefaultLanguage(defaultLanguage)
  }, [defaultLanguage])

  // 等到公开配置到达后再按需启动会话：过期链接一律停在过期页，需口令的新访客直接停在口令页，不盲发 /session
  useEffect(() => {
    if (!config.data || initStartedRef.current) return
    initStartedRef.current = true
    const biz = config.data.kind === "business_link" ? config.data.business : undefined
    const hasStored = identity.hasStoredSession()
    if (biz?.expired) {
      setGate("expired")
      setReady(true)
      if (!embedded) tell("ready")
      return
    }
    if (biz && !hasStored && biz.requires_passcode) {
      setGate("passcode")
      setReady(true)
      if (!embedded) tell("ready")
      return
    }
    void waitInit()
      .then(async (init: Init) => {
        if (floating) setMobile(init.mobile)
        await applyHostInitLanguage({ lang: init.lang, hostLang: init.hostLang })
        await identity.start(init.userToken, init.referrer, !embedded || inline)
        if (!embedded) tell("ready")
        setReady(true)
        if (openRef.current) {
          await identity.ensure()
        }
      })
      .catch((err) => {
        setReady(true)
        handleSessionError(err)
      })
  }, [config.data, floating])

  // 外面的命令：展开、收起、登录、退出、切换语言
  useEffect(
    () =>
      listen({
        layout: (m) => {
          if (floating) setMobile(m)
        },
        command: (c) => {
          if (c.command === "open" || c.command === "close") {
            if (floating) toggle(c.command === "open")
            return
          }
          if (c.command === "setLanguage") {
            void applyHostCommandLanguage(c.lang).then(() => identity.syncLanguage(i18n.requestedLanguage()))
            return
          }
          if (c.command === "login") {
            void identity
              .login(c.user_token)
              .then((switched) => {
                if (!switched) return
                setView({ kind: "chat" })
                setReplyTicket(null)
                void queryClient.invalidateQueries({ queryKey: ["widget"] })
              })
              .catch(handleSessionError)
            return
          }
          setView({ kind: "chat" })
          setReplyTicket(null)
          void identity
            .logout(openRef.current)
            .then(() => queryClient.invalidateQueries({ queryKey: ["widget"] }))
            .catch(handleSessionError)
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
  const unread = timeline.data?.unread ?? 0
  useEffect(() => {
    if (ready) tell("unread", { count: unread })
  }, [ready, unread])

  const setBrandColor = useSetWidgetBrand()
  const rawColor = config.data?.appearance.color ?? DEFAULT_WIDGET_COLOR
  useEffect(() => {
    setBrandColor(rawColor)
  }, [rawColor, setBrandColor])

  const color = "brand"
  const name = config.data?.name ?? t("app.widget")

  // 同步文档标题与宿主 iframe.title，保证无障碍与浏览器标签页随语言即时更新
  useEffect(() => {
    if (typeof document !== "undefined") document.title = name
    if (ready) tell("meta", { title: name })
  }, [name, ready])

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

  const full = mobile || !floating
  const close = floating ? (full ? "close" : "collapse") : hasCloseButton() ? "close" : undefined
  const onClose = () => (floating ? toggle(false) : tell("close"))
  const ticketList = tickets.data ?? []
  const hasWaitingTicket = ticketList.some((tk) => tk.status === "waiting_on_customer")
  const latestConversation = timeline.data?.latest_conversation
  const hasMessages = (timeline.data?.items.length ?? 0) > 0
  const isBusinessLink = config.data?.kind === "business_link"
  const biz = config.data?.business
  const chatStatus =
    !isBusinessLink && latestConversation
      ? latestConversation.status === "closed"
        ? t("chat.status_closed")
        : t("chat.status_open")
      : undefined

  const showIntake =
    isBusinessLink &&
    !intakeCompleted &&
    needsIntake(contact ?? null, biz?.intake, intakeSkipped, hasMessages)

  const ticketAction =
    !isBusinessLink && ticketList.length > 0 ? (
      <Button
        size="compact-xs"
        variant="subtle"
        c={fg}
        leftSection={
          <Indicator color="yellow" size={7} disabled={!hasWaitingTicket} offset={1}>
            <IconTicket size={14} />
          </Indicator>
        }
        onClick={() => setView({ kind: "tickets" })}
      >
        {t("tickets.single_entry", { n: ticketList.length })}
      </Button>
    ) : undefined

  const renderMainHeader = () => {
    if (isBusinessLink) {
      return (
        <BusinessCardHeader
          color={color}
          title={name}
          owner={biz?.owner}
          greeting={hasMessages ? undefined : config.data?.greeting || t("home.default_greeting")}
          awayMessage={biz?.away_message}
          compact={hasMessages && !showIntake}
          close={close}
          onClose={onClose}
        />
      )
    }
    return (
      <Header
        color={color}
        title={name}
        greeting={latestConversation ? undefined : config.data?.greeting || t("home.default_greeting")}
        status={chatStatus}
        extra={ticketAction}
        close={close}
        onClose={onClose}
      />
    )
  }

  const panel = (
    <Box
      className="w-panel"
      data-full={full || undefined}
      style={full && height ? { height } : undefined}
    >
      {!ready || !config.data ? (
        <Center style={{ flex: 1 }}>
          <Loader color={color} />
        </Center>
      ) : gate === "expired" ? (
        <Box key="expired" className="w-view">
          {renderMainHeader()}
          <ExpiredView />
        </Box>
      ) : gate === "passcode" ? (
        <Box key="passcode" className="w-view">
          {renderMainHeader()}
          <PasscodeGate
            color={color}
            onUnlock={async (passcode) => {
              await identity.unlockWithPasscode(passcode)
              setGate("none")
            }}
          />
        </Box>
      ) : !contact || timeline.isPending ? (
        <Center style={{ flex: 1 }}>
          <Loader color={color} />
        </Center>
      ) : view.kind === "ticket" ? (
        <Box key={`ticket:${view.id}`} className="w-view">
          <TicketPage
            id={view.id}
            color={color}
            name={name}
            close={close}
            onClose={onClose}
            onBack={() => setView(view.from === "tickets" ? { kind: "tickets" } : { kind: "chat" })}
            onReplyInChat={(tk) => {
              setReplyTicket(tk)
              setView({ kind: "chat" })
            }}
          />
        </Box>
      ) : view.kind === "tickets" ? (
        <Box key="tickets" className="w-view">
          <Header
            color={color}
            title={t("tickets.my_tickets")}
            onBack={() => setView({ kind: "chat" })}
            close={close}
            onClose={onClose}
          />
          <Box className="w-scroll" p="md">
            <Paper withBorder radius="lg" style={{ overflow: "hidden" }}>
              <TicketListItems tickets={ticketList} color={color} onOpen={(id) => setView({ kind: "ticket", id, from: "tickets" })} />
            </Paper>
          </Box>
        </Box>
      ) : (
        <Box key="chat" className="w-view">
          {renderMainHeader()}
          {showIntake && biz?.intake ? (
            <IntakeForm
              color={color}
              intake={biz.intake}
              contact={contact}
              onDone={() => setIntakeCompleted(true)}
              onSkip={() => setIntakeSkipped(true)}
            />
          ) : (
            <ConversationView
              color={color}
              name={name}
              touch={full}
              visible={open && visible}
              allowAttachments={config.data?.visitor_uploads !== false}
              starters={isBusinessLink ? biz?.starters : undefined}
              replyTicket={replyTicket}
              onClearReplyTicket={() => setReplyTicket(null)}
              onOpenTicket={(id) => setView({ kind: "ticket", id, from: "chat" })}
            />
          )}
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

/** TicketPage 是单张工单的页面壳：顶栏显示工单编号与返回按钮，下方展示工单进度和去对话补充信息入口。 */
function TicketPage({
  id,
  color,
  name,
  close,
  onClose,
  onBack,
  onReplyInChat,
}: {
  id: string
  color: string
  name: string
  close?: "collapse" | "close"
  onClose: () => void
  onBack: () => void
  onReplyInChat: (ticket: ReplyTicketContext) => void
}): ReactNode {
  const t = useT()
  const ticket = useTicket(id)
  const title = ticket.data ? t("tickets.title", { number: ticket.data.number }) : t("tickets.my_tickets")
  return (
    <>
      <Header color={color} title={title} onBack={onBack} close={close} onClose={onClose} />
      <TicketDetailView id={id} color={color} name={name} onReplyInChat={onReplyInChat} />
    </>
  )
}
