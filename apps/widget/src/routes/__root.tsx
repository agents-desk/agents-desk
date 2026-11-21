import "@fontsource-variable/inter/index.css"
import "@go-tenon/ui/styles.css"
import "../widget.css"
import type { ReactNode } from "react"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import { type RouterContext, UIProvider } from "@go-tenon/ui"
import { i18n } from "../i18n"
import { theme } from "../theme"

// 根路由：整个 HTML 文档。聊天窗口嵌在第三方网站中，配色固定为浅色，不跟随网站或系统；嵌入时页面背景透明（host.ts）。
// viewport-fit=cover 让页面延伸到刘海和底部横条下面，安全区域由 widget.css 留出。
export const Route = createRootRouteWithContext<RouterContext>()({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1, viewport-fit=cover" },
      { title: i18n.t("app.widget") },
    ],
  }),
  shellComponent: Document,
  component: Outlet,
})

function Document({ children }: { children: ReactNode }) {
  // nonce 由 Go 为每个请求生成，内联脚本带上它才能通过内容安全策略。
  const nonce = useRouter().options.ssr?.nonce
  return (
    <html lang={i18n.language()} {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} forceColorScheme="light" />
        <HeadContent />
      </head>
      <body>
        <UIProvider theme={theme} defaultColorScheme="light">
          {children}
        </UIProvider>
        <Scripts />
      </body>
    </html>
  )
}
