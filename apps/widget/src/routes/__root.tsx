import "@go-tenon/ui/base.css"
import "../widget.css"
import { telemetryMeta } from "@go-tenon/start"
import type { RouterContext } from "@go-tenon/ui/core"
import { ColorSchemeScript, DirectionProvider, MantineProvider, mantineHtmlProps, useDirection } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import { type ReactNode, useEffect, useMemo, useState, useSyncExternalStore } from "react"
import { directionOf, i18n } from "../i18n"
import { buildWidgetTheme, DEFAULT_WIDGET_COLOR, WidgetBrandContext } from "../theme"

// 根路由：整个 HTML 文档。聊天窗口嵌在第三方网站中，使用 @go-tenon/ui/base.css 和轻量 MantineProvider，
// 配色固定为浅色，品牌色随渠道配置动态生成，排版方向随当前语言自动在 ltr/rtl 间切换；嵌入时页面背景透明（host.ts）。
// viewport-fit=cover 让页面延伸到刘海和底部横条下面，安全区域由 widget.css 留出。
export const Route = createRootRouteWithContext<RouterContext>()({
  head: () => {
    const telemetry = telemetryMeta()
    return {
      meta: [
        { charSet: "utf-8" },
        { name: "viewport", content: "width=device-width, initial-scale=1, viewport-fit=cover" },
        { title: i18n.t("app.widget") },
        ...(telemetry ? [{ name: "tenon-telemetry", content: telemetry }] : []),
      ],
    }
  },
  shellComponent: Document,
  component: Outlet,
})

/** DirectionSync 将当前语言对应的文档方向实时同步到 Mantine DirectionProvider 与 html[dir]。 */
function DirectionSync({ children }: { children: ReactNode }): ReactNode {
  const language = useSyncExternalStore(i18n.subscribe, i18n.language, i18n.language)
  const dir = directionOf(language)
  const { setDirection } = useDirection()
  useEffect(() => {
    setDirection(dir)
    if (typeof document !== "undefined") {
      document.documentElement.lang = language
      document.documentElement.dir = dir
    }
  }, [dir, language, setDirection])
  return children
}

function Document({ children }: { children: ReactNode }) {
  // nonce 由 Go 为每个请求生成，内联脚本带上它才能通过内容安全策略。
  const nonce = useRouter().options.ssr?.nonce
  const [brandColor, setBrandColor] = useState(DEFAULT_WIDGET_COLOR)
  const theme = useMemo(() => buildWidgetTheme(brandColor), [brandColor])
  const initialLang = i18n.language()
  const initialDir = directionOf(initialLang)
  return (
    <html lang={initialLang} dir={initialDir} {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} forceColorScheme="light" />
        <HeadContent />
      </head>
      <body>
        <DirectionProvider initialDirection={initialDir} detectDirection={false}>
          <MantineProvider theme={theme} forceColorScheme="light">
            <DirectionSync>
              <WidgetBrandContext.Provider value={setBrandColor}>{children}</WidgetBrandContext.Provider>
            </DirectionSync>
          </MantineProvider>
        </DirectionProvider>
        <Scripts />
      </body>
    </html>
  )
}
