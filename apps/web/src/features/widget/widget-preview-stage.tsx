import type { WidgetAppearance } from "@agents-desk/client/tenant"
import { Box, Center, Group, SegmentedControl, Stack, Text } from "@mantine/core"
import { IconDeviceDesktop, IconDeviceMobile, IconMessage, IconMessageCircle } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { PREVIEW_PREFIX, type PreviewMessage, type PreviewScene, type PreviewViewport } from "./preview-protocol"
import classes from "./widget-studio.module.css"

/** WidgetPreviewStageProps 是右侧实时预览画布的输入。 */
interface WidgetPreviewStageProps {
  name: string
  appearance: WidgetAppearance
}

/**
 * WidgetPreviewStage 是聊天插件的右侧实时预览舞台：
 * 通过同源 iframe 嵌入真实的 /widget/preview，双向同步草稿配置与用户点击切换的场景，支持桌面浮窗与手机全屏视图。
 */
export function WidgetPreviewStage({ name, appearance }: WidgetPreviewStageProps): ReactNode {
  const t = useT()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const [ready, setReady] = useState(false)
  const [scene, setScene] = useState<PreviewScene>("conversation")
  const [viewport, setViewport] = useState<PreviewViewport>("desktop")

  const postPreview = useCallback(
    (nextScene: PreviewScene, nextViewport: PreviewViewport) => {
      const target = iframeRef.current?.contentWindow
      if (!target) return
      const payload: PreviewMessage = {
        type: `${PREVIEW_PREFIX}preview`,
        name,
        appearance,
        scene: nextScene,
        viewport: nextViewport,
      }
      target.postMessage(payload, window.location.origin)
    },
    [appearance, name],
  )
  const latestPostRef = useRef<() => void>(() => {})
  latestPostRef.current = () => postPreview(scene, viewport)

  // 监听预览窗口上报的就绪信号与用户直接点击预览内容触发的场景切换。
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
      if (kind === "preview_scene") {
        const next = e.data.scene
        if (next === "conversation" || next === "launcher") {
          setScene(next)
        }
      }
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }, [])

  // 草稿、场景或视口变化后立即推送到预览 iframe。
  useEffect(() => {
    if (!ready) return
    postPreview(scene, viewport)
  }, [postPreview, ready, scene, viewport])

  const handleViewportChange = (next: string) => {
    const v: PreviewViewport = next === "mobile" ? "mobile" : "desktop"
    setViewport(v)
    if (v === "mobile" && scene === "launcher") {
      setScene("conversation")
    }
  }

  const sceneOptions = [
    {
      value: "conversation",
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
            value: "launcher",
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

  const viewportOptions = [
    {
      value: "desktop",
      label: (
        <Center style={{ gap: 6 }}>
          <IconDeviceDesktop size={14} />
          <span>{t("widget_studio.viewport_desktop")}</span>
        </Center>
      ),
    },
    {
      value: "mobile",
      label: (
        <Center style={{ gap: 6 }}>
          <IconDeviceMobile size={14} />
          <span>{t("widget_studio.viewport_mobile")}</span>
        </Center>
      ),
    },
  ]

  return (
    <Stack gap="sm" className={classes.previewColumn}>
      <Group justify="space-between" gap="xs" wrap="wrap">
        <SegmentedControl size="xs" value={scene} onChange={(v) => setScene(v as PreviewScene)} data={sceneOptions} />
        <SegmentedControl size="xs" value={viewport} onChange={handleViewportChange} data={viewportOptions} />
      </Group>

      <Box className={classes.stage}>
        <Box className={viewport === "mobile" ? classes.mobileShell : classes.desktopShell}>
          <iframe
            ref={iframeRef}
            src="/widget/preview"
            title={t("widget_studio.preview_title")}
            className={classes.frame}
          />
        </Box>
      </Box>

      <Text size="xs" c="dimmed" ta="center">
        {t("widget_studio.preview_hint")}
      </Text>
    </Stack>
  )
}
