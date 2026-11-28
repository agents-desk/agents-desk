import type { WidgetAppearance } from "@agents-desk/client/tenant"
import { Box, Center, Group, SegmentedControl, Select, Stack, Text } from "@mantine/core"
import { IconDeviceDesktop, IconDeviceMobile, IconMessage, IconMessageCircle, IconWorld } from "@tabler/icons-react"
import { useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { languageName } from "../../languages"
import { PREVIEW_PREFIX, type PreviewMessage, type PreviewScene, type PreviewViewport } from "./preview-protocol"
import classes from "./widget-studio.module.css"

/** PreviewStageFrameProps 是同源 iframe 实时预览舞台外壳的输入。 */
export interface PreviewStageFrameProps<S extends string> {
  title: string
  hint: string
  scene: S
  scenes: ReadonlyArray<{ value: S; label: ReactNode }>
  validScenes: readonly S[]
  onSceneChange: (scene: S) => void
  language: string
  previewLanguages: string[]
  onLanguageChange: (lang: string) => void
  languageSelectWidth?: number
  viewport: PreviewViewport
  onViewportChange: (viewport: PreviewViewport) => void
  mobileFirstViewport?: boolean
  buildMessage: (scene: S, viewport: PreviewViewport) => PreviewMessage
}

/**
 * PreviewStageFrame 是同源 /widget/preview iframe 的通用预览舞台：
 * 托管场景切换、语言下拉、桌面/手机视口切换及与预览窗口之间的 postMessage 握手和同步。
 */
export function PreviewStageFrame<S extends string>({
  title,
  hint,
  scene,
  scenes,
  validScenes,
  onSceneChange,
  language,
  previewLanguages,
  onLanguageChange,
  languageSelectWidth = 160,
  viewport,
  onViewportChange,
  mobileFirstViewport = false,
  buildMessage,
}: PreviewStageFrameProps<S>): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const [ready, setReady] = useState(false)

  const postPreview = useCallback(
    (nextScene: S, nextViewport: PreviewViewport) => {
      const target = iframeRef.current?.contentWindow
      if (!target) return
      target.postMessage(buildMessage(nextScene, nextViewport), window.location.origin)
    },
    [buildMessage],
  )
  const latestPostRef = useRef<() => void>(() => {})
  latestPostRef.current = () => postPreview(scene, viewport)

  const onSceneChangeRef = useRef(onSceneChange)
  onSceneChangeRef.current = onSceneChange
  const validScenesRef = useRef(validScenes)
  validScenesRef.current = validScenes

  // 监听预览窗口上报的就绪信号与用户在预览内点击触发的场景切换。
  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin || e.source !== iframeRef.current?.contentWindow) return
      if (!e.data || typeof e.data.type !== "string" || !e.data.type.startsWith(PREVIEW_PREFIX)) return
      const kind = e.data.type.slice(PREVIEW_PREFIX.length)
      if (kind === "preview_ready") {
        setReady(true)
        latestPostRef.current()
        return
      }
      if (kind === "preview_scene" && typeof e.data.scene === "string") {
        if ((validScenesRef.current as readonly string[]).includes(e.data.scene)) {
          onSceneChangeRef.current(e.data.scene as S)
        }
      }
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }, [])

  // 草稿、语言、场景或视口变化后立即推送到预览 iframe。
  useEffect(() => {
    if (!ready) return
    postPreview(scene, viewport)
  }, [postPreview, ready, scene, viewport])

  const languageOptions = useMemo(() => {
    const tags = new Set<string>([...previewLanguages, language])
    return [...tags].filter(Boolean).map((code) => ({
      value: code,
      label: `${languageName(code, uiLang)} (${code})`,
    }))
  }, [language, previewLanguages, uiLang])

  const desktopOpt = {
    value: "desktop",
    label: (
      <Center style={{ gap: 6 }}>
        <IconDeviceDesktop size={14} />
        <span>{t("widget_studio.viewport_desktop")}</span>
      </Center>
    ),
  }
  const mobileOpt = {
    value: "mobile",
    label: (
      <Center style={{ gap: 6 }}>
        <IconDeviceMobile size={14} />
        <span>{t("widget_studio.viewport_mobile")}</span>
      </Center>
    ),
  }
  const viewportOptions = mobileFirstViewport ? [mobileOpt, desktopOpt] : [desktopOpt, mobileOpt]

  return (
    <Stack gap="sm" className={classes.previewColumn}>
      <Group justify="space-between" gap="xs" wrap="wrap">
        <SegmentedControl size="xs" value={scene} onChange={(v) => onSceneChange(v as S)} data={[...scenes]} />
        <Group gap="xs" wrap="nowrap">
          <Select
            size="xs"
            w={languageSelectWidth}
            aria-label={t("widget_i18n.preview_language")}
            leftSection={<IconWorld size={13} />}
            value={language}
            onChange={(v) => v && onLanguageChange(v)}
            data={languageOptions}
            allowDeselect={false}
          />
          <SegmentedControl
            size="xs"
            value={viewport}
            onChange={(next) => onViewportChange(next === "mobile" ? "mobile" : "desktop")}
            data={viewportOptions}
          />
        </Group>
      </Group>

      <Box className={classes.stage}>
        <Box className={viewport === "mobile" ? classes.mobileShell : classes.desktopShell}>
          <iframe
            ref={iframeRef}
            src="/widget/preview"
            title={title}
            className={classes.frame}
            onLoad={() => {
              setReady(true)
              latestPostRef.current()
            }}
          />
        </Box>
      </Box>

      <Text size="xs" c="dimmed" ta="center">
        {hint}
      </Text>
    </Stack>
  )
}

/** WidgetPreviewStageProps 是右侧实时预览画布的输入。 */
interface WidgetPreviewStageProps {
  name: string
  greeting: string
  appearance: WidgetAppearance
  language: string
  previewLanguages: string[]
  onLanguageChange: (lang: string) => void
}

const WIDGET_SCENES: readonly PreviewScene[] = ["conversation", "launcher"]

/**
 * WidgetPreviewStage 是聊天插件的右侧实时预览舞台：
 * 基于 PreviewStageFrame 嵌入 /widget/preview，实时同步草稿配置与预览语言，支持桌面浮窗与手机全屏视图。
 */
export function WidgetPreviewStage({
  name,
  greeting,
  appearance,
  language,
  previewLanguages,
  onLanguageChange,
}: WidgetPreviewStageProps): ReactNode {
  const t = useT()
  const [scene, setScene] = useState<PreviewScene>("conversation")
  const [viewport, setViewport] = useState<PreviewViewport>("desktop")

  const handleViewportChange = (v: PreviewViewport) => {
    setViewport(v)
    if (v === "mobile" && scene === "launcher") {
      setScene("conversation")
    }
  }

  const scenes = [
    {
      value: "conversation" as const,
      label: (
        <Center style={{ gap: 6 }}>
          <IconMessage size={14} />
          <span>{t("widget_studio.scene_conversation")}</span>
        </Center>
      ),
    },
    ...(viewport === "desktop"
      ? [
          {
            value: "launcher" as const,
            label: (
              <Center style={{ gap: 6 }}>
                <IconMessageCircle size={14} />
                <span>{t("widget_studio.scene_launcher")}</span>
              </Center>
            ),
          },
        ]
      : []),
  ]

  const buildMessage = useCallback(
    (nextScene: PreviewScene, nextViewport: PreviewViewport): PreviewMessage => ({
      type: `${PREVIEW_PREFIX}preview`,
      lang: language,
      name,
      greeting,
      appearance,
      language,
      scene: nextScene,
      viewport: nextViewport,
    }),
    [appearance, greeting, language, name],
  )

  return (
    <PreviewStageFrame
      title={t("widget_studio.preview_title")}
      hint={t("widget_studio.preview_hint")}
      scene={scene}
      scenes={scenes}
      validScenes={WIDGET_SCENES}
      onSceneChange={setScene}
      language={language}
      previewLanguages={previewLanguages}
      onLanguageChange={onLanguageChange}
      viewport={viewport}
      onViewportChange={handleViewportChange}
      buildMessage={buildMessage}
    />
  )
}
