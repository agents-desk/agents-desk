import "@fontsource-variable/inter/index.css"
import "@go-tenon/ui/styles.css"
import "@mantine/charts/styles.css"
import "../desk.css"
import "../command-palette.css"
import { telemetryMeta } from "@go-tenon/start"
import { type RouterContext, UIProvider } from "@go-tenon/ui"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import {
  IconAddressBook,
  IconAdjustmentsHorizontal,
  IconBolt,
  IconBrandTelegram,
  IconInbox,
  IconLanguage,
  IconMessageBolt,
  IconMessageCircle,
  IconStar,
  IconTicket,
} from "@tabler/icons-react"
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
        { title: i18n.t("app.web") },
        ...(telemetry ? [{ name: "tenon-telemetry", content: telemetry }] : []),
      ],
    }
  },
  shellComponent: Document,
  component: Outlet,
})

// 框架自带的图标之外，图标栏和设置菜单中用到的图标。
const icons = {
  inbox: IconInbox,
  chat: IconMessageCircle,
  contacts: IconAddressBook,
  bolt: IconBolt,
  macro: IconMessageBolt,
  sliders: IconAdjustmentsHorizontal,
  star: IconStar,
  language: IconLanguage,
  telegram: IconBrandTelegram,
  ticket: IconTicket,
}

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
        <UIProvider theme={theme} icons={icons} colorSchemeKey={colorScheme}>
          {children}
        </UIProvider>
        <Scripts />
      </body>
    </html>
  )
}
