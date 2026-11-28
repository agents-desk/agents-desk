import type { WidgetAppearance, WidgetBusinessIntakeConfig, WidgetPublicOwnerCard } from "@agents-desk/client/widget"
import { Box, Button, Stack, Text } from "@mantine/core"
import { useT } from "@go-tenon/ui/core"
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react"
import { i18n } from "../i18n"
import { DEFAULT_WIDGET_COLOR, isValidHexColor, useSetWidgetBrand } from "../theme"
import { BusinessCardHeader } from "./BusinessCardHeader"
import { IntakeForm, PasscodeGate } from "./BusinessGates"
import { Composer } from "./Composer"
import { Bubble } from "./Conversation"
import { Header } from "./Header"
import { Launcher } from "./Launcher"

/** PREFIX 是工作区配置工作台与预览窗口之间 postMessage 的消息前缀。 */
const PREFIX = "agents-desk:"

/** PreviewMode 区分公共网页聊天插件与个人网页商务沟通链接两种预览模式。 */
type PreviewMode = "widget" | "business_link"

/** PreviewScene 是预览窗口展示的页面场景：对话中、收起态启动气泡、开场留资或口令校验。 */
type PreviewScene = "conversation" | "launcher" | "intake" | "passcode"

/** PreviewViewport 是预览窗口模拟的宿主视口形态：桌面或手机全屏。 */
type PreviewViewport = "desktop" | "mobile"

/** PreviewBusinessPayload 是商务沟通链接工作台推送的实时名片、留资与快捷话题草稿。 */
interface PreviewBusinessPayload {
  owner?: WidgetPublicOwnerCard
  away_message?: string
  intake?: WidgetBusinessIntakeConfig
  starters?: string[]
  requires_passcode?: boolean
}

/** PreviewState 是工作区配置工作台推送的实时草稿与预览参数。 */
interface PreviewState {
  mode: PreviewMode
  name: string
  greeting: string
  appearance: WidgetAppearance
  scene: PreviewScene
  viewport: PreviewViewport
  business?: PreviewBusinessPayload
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
 * Preview 是网页聊天插件与商务沟通链接共用的纯客户端可交互预览壳（挂载于 /widget/preview）。
 * 复用真实的 Header、BusinessCardHeader、IntakeForm、PasscodeGate、Bubble、Composer 与 Launcher，零后端写入。
 */
export function Preview(): ReactNode {
  const t = useT()
  const setBrandColor = useSetWidgetBrand()
  const [state, setState] = useState<PreviewState>({
    mode: "widget",
    name: "",
    greeting: "",
    appearance: { color: DEFAULT_WIDGET_COLOR },
    scene: "conversation",
    viewport: "desktop",
  })
  const [extraMessages, setExtraMessages] = useState<PreviewMessage[]>([])
  const scrollRef = useRef<HTMLDivElement>(null)

  // 监听同源父窗口推送的实时草稿、语言与场景切换指令，挂载时上报 preview_ready。
  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin || e.source !== window.parent) return
      if (!e.data || e.data.type !== `${PREFIX}preview`) return
      const nextMode: PreviewMode = e.data.mode === "business_link" ? "business_link" : "widget"
      if (typeof e.data.lang === "string" && e.data.lang) {
        i18n.setLanguage(e.data.lang)
      }
      const nextColor = typeof e.data.appearance?.color === "string" ? e.data.appearance.color : DEFAULT_WIDGET_COLOR
      const nextGreeting = typeof e.data.greeting === "string" ? e.data.greeting : ""
      const nextLanguage = typeof e.data.language === "string" && e.data.language ? e.data.language : "en"
      const rawScene = e.data.scene
      const nextScene: PreviewScene =
        rawScene === "launcher" || rawScene === "intake" || rawScene === "passcode" ? rawScene : "conversation"
      const nextViewport: PreviewViewport = e.data.viewport === "mobile" ? "mobile" : "desktop"
      i18n.setLanguage(nextLanguage)
      setState({
        mode: nextMode,
        name: typeof e.data.name === "string" ? e.data.name : "",
        greeting: nextGreeting,
        appearance: { color: nextColor },
        scene: nextMode === "widget" && nextViewport === "mobile" && nextScene === "launcher" ? "conversation" : nextScene,
        viewport: nextViewport,
        business: nextMode === "business_link" && e.data.business ? (e.data.business as PreviewBusinessPayload) : undefined,
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

  // 仅网页聊天插件的桌面浮窗态透明底以展示真实浮层与阴影；商务沟通链接与手机全屏态铺满实体背景。
  useEffect(() => {
    if (state.mode === "widget" && state.viewport === "desktop") {
      document.documentElement.dataset.embedded = ""
    } else {
      delete document.documentElement.dataset.embedded
    }
    return () => {
      delete document.documentElement.dataset.embedded
    }
  }, [state.mode, state.viewport])

  // 模拟发送新消息后滚动到时间线底部。
  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [extraMessages.length, state.scene])

  const isBiz = state.mode === "business_link"
  const ownerName = state.business?.owner?.name || t("preview.business_owner_name")

  const sampleMessages = useMemo<PreviewMessage[]>(
    () => [
      {
        id: "m1",
        mine: true,
        text: t(isBiz ? "preview.business_sample_partner" : "preview.sample_customer_msg"),
        time: t("time.just_now"),
      },
      {
        id: "m2",
        mine: false,
        author: isBiz ? ownerName : t("preview.agent_name"),
        text: t(isBiz ? "preview.business_sample_owner" : "preview.sample_agent_reply"),
        time: t("time.just_now"),
      },
      ...extraMessages,
    ],
    [extraMessages, isBiz, ownerName, t],
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
  const greeting = state.greeting.trim() || t("home.default_greeting")
  const full = isBiz || state.viewport === "mobile"

  if (isBiz) {
    const biz = state.business
    const starters = biz?.starters ?? []
    const intake: WidgetBusinessIntakeConfig = biz?.intake ?? {
      enabled: true,
      name: "required",
      company: "optional",
      email: "optional",
      note: "off",
    }
    return (
      <Box className="w-panel" data-full>
        <Box key={state.scene} className="w-view">
          <BusinessCardHeader
            color={color}
            title={title}
            owner={biz?.owner}
            greeting={state.scene === "conversation" ? undefined : greeting}
            awayMessage={biz?.away_message}
            compact={state.scene === "conversation"}
          />
          {state.scene === "passcode" ? (
            <PasscodeGate
              color={color}
              preview
              onUnlock={async () => switchScene(intake.enabled ? "intake" : "conversation")}
            />
          ) : state.scene === "intake" ? (
            <IntakeForm
              color={color}
              intake={{ ...intake, enabled: true }}
              preview
              onDone={() => switchScene("conversation")}
              onSkip={() => switchScene("conversation")}
            />
          ) : (
            <>
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
                    {starters.length > 0 && (
                      <Stack gap={6} pt="xs">
                        <Text size="xs" c="dimmed">
                          {t("business.starters_heading")}
                        </Text>
                        {starters.map((topic) => (
                          <Button
                            key={topic}
                            variant="light"
                            color={color}
                            size="xs"
                            radius="md"
                            justify="flex-start"
                            onClick={() => handleSend(topic)}
                            styles={{ label: { whiteSpace: "normal", textAlign: "left" } }}
                          >
                            {topic}
                          </Button>
                        ))}
                      </Stack>
                    )}
                  </Stack>
                </Box>
              </Box>
              <Composer
                color={color}
                touch={state.viewport === "mobile"}
                placeholder={t("composer.placeholder")}
                allowAttachments={false}
                onSend={handleSend}
              />
            </>
          )}
        </Box>
      </Box>
    )
  }

  // 桌面收起态仅渲染右下角启动按钮。
  if (!full && state.scene === "launcher") {
    return (
      <Box className="w-dock">
        <Launcher color={color} open={false} unread={1} onClick={() => switchScene("conversation")} />
      </Box>
    )
  }

  const closeMode: "collapse" | "close" = full ? "close" : "collapse"
  const onClose = () => switchScene(full ? "conversation" : "launcher")

  const panel = (
    <Box className="w-panel" data-full={full || undefined}>
      <Box key="conversation" className="w-view">
        <Header
          color={color}
          title={title}
          greeting={greeting}
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
