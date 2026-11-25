import "@go-tenon/ui/base.css"
import "../widget.css"
import { telemetryMeta } from "@go-tenon/start"
import type { RouterContext } from "@go-tenon/ui"
import { ColorSchemeScript, MantineProvider, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import { type ReactNode, useMemo, useState } from "react"
import { i18n } from "../i18n"
import { buildWidgetTheme, DEFAULT_WIDGET_COLOR, WidgetBrandContext } from "../theme"

// 根路由：整个 HTML 文档。聊天窗口嵌在第三方网站中，使用 @go-tenon/ui/base.css 和轻量 MantineProvider，
// 配色固定为浅色，品牌色随渠道配置动态生成；嵌入时页面背景透明（host.ts）。
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

function Document({ children }: { children: ReactNode }) {
  // nonce 由 Go 为每个请求生成，内联脚本带上它才能通过内容安全策略。
  const nonce = useRouter().options.ssr?.nonce
  const [brandColor, setBrandColor] = useState(DEFAULT_WIDGET_COLOR)
  const theme = useMemo(() => buildWidgetTheme(brandColor), [brandColor])
  return (
    <html lang={i18n.language()} {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} forceColorScheme="light" />
        <HeadContent />
      </head>
      <body>
        <MantineProvider theme={theme} forceColorScheme="light">
          <WidgetBrandContext.Provider value={setBrandColor}>{children}</WidgetBrandContext.Provider>
        </MantineProvider>
        <Scripts />
      </body>
    </html>
  )
}
