import "@fontsource-variable/inter/index.css"
import "@go-tenon/ui/base.css"
import { telemetryMeta } from "@go-tenon/start"
import { CoreUIProvider, type RouterContext } from "@go-tenon/ui/core"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { i18n } from "../i18n"
import { theme } from "../theme"

// 根路由：整个 HTML 文档。页面在 <Outlet /> 中。
export const Route = createRootRouteWithContext<RouterContext>()({
  head: () => {
    const telemetry = telemetryMeta()
    return {
      meta: [
        { charSet: "utf-8" },
        { name: "viewport", content: "width=device-width, initial-scale=1" },
        { title: i18n.t("app.site") },
        ...(telemetry ? [{ name: "tenon-telemetry", content: telemetry }] : []),
      ],
      links: [{ rel: "icon", type: "image/svg+xml", href: "/favicon.svg" }],
    }
  },
  shellComponent: Document,
  component: Outlet,
})

function Document({ children }: { children: ReactNode }) {
  // nonce 由 Go 为每个请求生成，内联脚本带上它才能通过内容安全策略。
  const { ssr, context } = useRouter().options
  const nonce = ssr?.nonce
  // 配色保存在这个前端自己的键中，同一个域名下的其他前端（例如管理员端）各记各的。
  const colorScheme = context.app.storage.key("color-scheme")
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} localStorageKey={colorScheme} />
        <HeadContent />
      </head>
      <body>
        <CoreUIProvider theme={theme} colorSchemeKey={colorScheme}>{children}</CoreUIProvider>
        <Scripts />
      </body>
    </html>
  )
}
