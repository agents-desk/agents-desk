import type { ConversationContactConversationDTO, WidgetAppearance } from "@agents-desk/client/widget"
import { Box, Stack } from "@mantine/core"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react"
import { DEFAULT_WIDGET_COLOR, isValidHexColor, useSetWidgetBrand } from "../theme"
import { Composer } from "./Composer"
import { Bubble } from "./Conversation"
import { Header } from "./Header"
import { Home } from "./Home"
import { Launcher } from "./Launcher"

/** PREFIX 是工作区配置工作台与预览窗口之间 postMessage 的消息前缀。 */
const PREFIX = "agents-desk:"

/** PreviewScene 是预览窗口展示的页面场景：首页、对话中、收起态（启动气泡）。 */
type PreviewScene = "home" | "conversation" | "launcher"

/** PreviewViewport 是预览窗口模拟的宿主视口形态：桌面浮窗或手机全屏。 */
type PreviewViewport = "desktop" | "mobile"

/** PreviewState 是工作区配置工作台推送的实时草稿与预览参数。 */
interface PreviewState {
  name: string
  appearance: WidgetAppearance
  scene: PreviewScene
  viewport: PreviewViewport
}

/** PreviewMessage 是预览对话时间线中的本地模拟消息。 */
interface PreviewMessage {
  id: string
  mine: boolean
  author?: string
  text: string
  time: string
}

/** notifyParent 向同源父窗口发送预览事件（就绪、用户点击切换场景）。 */
function notifyParent(type: "preview_ready" | "preview_scene", data: Record<string, unknown> = {}): void {
  if (typeof window === "undefined" || window.parent === window) return
  window.parent.postMessage({ type: PREFIX + type, ...data }, window.location.origin)
}

/**
 * Preview 是网页聊天插件的纯客户端可交互预览壳（挂载于 /widget/preview）。
 * 复用真实的 Header、Home、Bubble、Composer、Launcher 组件与根主题，以本地示例数据驱动交互预览。
 */
export function Preview(): ReactNode {
  const t = useT()
  const setBrandColor = useSetWidgetBrand()
  const [state, setState] = useState<PreviewState>({
    name: "",
    appearance: { color: DEFAULT_WIDGET_COLOR, greeting: "" },
    scene: "home",
    viewport: "desktop",
  })
  const [extraMessages, setExtraMessages] = useState<PreviewMessage[]>([])
  const scrollRef = useRef<HTMLDivElement>(null)

  // 监听同源父窗口推送的实时草稿与场景切换指令，挂载时上报 preview_ready。
  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin || e.source !== window.parent) return
      if (!e.data || e.data.type !== `${PREFIX}preview`) return
      const nextColor = typeof e.data.appearance?.color === "string" ? e.data.appearance.color : DEFAULT_WIDGET_COLOR
      const nextGreeting = typeof e.data.appearance?.greeting === "string" ? e.data.appearance.greeting : ""
      const nextScene: PreviewScene =
        e.data.scene === "conversation" || e.data.scene === "launcher" ? e.data.scene : "home"
      const nextViewport: PreviewViewport = e.data.viewport === "mobile" ? "mobile" : "desktop"
      setState({
        name: typeof e.data.name === "string" ? e.data.name : "",
        appearance: { color: nextColor, greeting: nextGreeting },
        scene: nextScene,
        viewport: nextViewport,
      })
    }
    window.addEventListener("message", onMessage)
    notifyParent("preview_ready")
    return () => window.removeEventListener("message", onMessage)
  }, [])

  // 输入为有效 #rrggbb 颜色时同步更新根主题品牌色。
  const colorValue = state.appearance.color ?? DEFAULT_WIDGET_COLOR
  useEffect(() => {
    if (isValidHexColor(colorValue)) setBrandColor(colorValue)
  }, [colorValue, setBrandColor])

  // 桌面浮窗态透明底以展示真实浮层与阴影；手机全屏态铺满实体背景。
  useEffect(() => {
    if (state.viewport === "desktop") {
      document.documentElement.dataset.embedded = ""
    } else {
      delete document.documentElement.dataset.embedded
    }
    return () => {
      delete document.documentElement.dataset.embedded
    }
  }, [state.viewport])

  // 模拟发送新消息后滚动到时间线底部。
  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [extraMessages.length, state.scene])

  const sampleConversations = useMemo<ConversationContactConversationDTO[]>(() => {
    const now = new Date().toISOString()
    const yesterday = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString()
    return [
      {
        id: "preview-1",
        status: "open",
        unread: 1,
        created_at: now,
        last_message_at: now,
        last_seq: 2,
        preview: t("preview.sample_agent_reply"),
        preview_author: { type: "member", name: t("preview.agent_name") },
        rating_requested: false,
      },
      {
        id: "preview-2",
        status: "closed",
        unread: 0,
        created_at: yesterday,
        last_message_at: yesterday,
        last_seq: 4,
        preview: t("preview.sample_prev_msg"),
        preview_author: { type: "contact", name: "" },
        rating_requested: false,
      },
    ]
  }, [t])

  const sampleMessages = useMemo<PreviewMessage[]>(
    () => [
      {
        id: "m1",
        mine: true,
        text: t("preview.sample_customer_msg"),
        time: t("time.just_now"),
      },
      {
        id: "m2",
        mine: false,
        author: t("preview.agent_name"),
        text: t("preview.sample_agent_reply"),
        time: t("time.just_now"),
      },
      ...extraMessages,
    ],
    [extraMessages, t],
  )

  const switchScene = (next: PreviewScene) => {
    setState((prev) => ({ ...prev, scene: next }))
    notifyParent("preview_scene", { scene: next })
  }

  const handleSend = (text: string) => {
    setExtraMessages((prev) => [
      ...prev,
      { id: `extra-${Date.now()}`, mine: true, text, time: t("time.just_now") },
    ])
  }

  const color = "brand"
  const title = state.name.trim() || t("app.widget")
  const greeting = state.appearance.greeting?.trim() || t("home.default_greeting")
  const full = state.viewport === "mobile"

  // 桌面收起态仅渲染右下角启动按钮。
  if (!full && state.scene === "launcher") {
    return (
      <Box className="w-dock">
        <Launcher color={color} open={false} unread={1} onClick={() => switchScene("home")} />
      </Box>
    )
  }

  const closeMode: "collapse" | "close" = full ? "close" : "collapse"
  const onClose = () => switchScene(full ? "home" : "launcher")

  const panel = (
    <Box className="w-panel" data-full={full || undefined}>
      {state.scene === "conversation" ? (
        <Box key="conversation" className="w-view">
          <Header
            color={color}
            title={title}
            status={t("chat.status_open")}
            onBack={() => switchScene("home")}
            close={closeMode}
            onClose={onClose}
          />
          <Box className="w-scroll-wrap">
            <Box ref={scrollRef} className="w-scroll" p="md">
              <Stack gap="sm">
                {sampleMessages.map((m) => (
                  <Bubble
                    key={m.id}
                    mine={m.mine}
                    author={m.author}
                    text={m.text}
                    meta={m.time}
                    color={color}
                    onPreview={() => {}}
                  />
                ))}
              </Stack>
            </Box>
          </Box>
          <Composer
            color={color}
            touch={full}
            placeholder={t("composer.placeholder")}
            allowAttachments={false}
            onSend={handleSend}
          />
        </Box>
      ) : (
        <Box key="home" className="w-view">
          <Header color={color} title={title} greeting={greeting} close={closeMode} onClose={onClose} />
          <Home
            color={color}
            name={title}
            conversations={sampleConversations}
            onOpen={() => switchScene("conversation")}
            onNew={() => switchScene("conversation")}
          />
        </Box>
      )}
    </Box>
  )

  if (full) return panel
  return (
    <Box className="w-dock w-dock-open">
      {panel}
      <Launcher color={color} open unread={0} onClick={() => switchScene("launcher")} />
    </Box>
  )
}
