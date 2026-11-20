import "@fontsource-variable/inter/index.css"
import "@go-tenon/ui/styles.css"
import type { ReactNode } from "react"
import { ColorSchemeScript, mantineHtmlProps } from "@mantine/core"
import { createRootRouteWithContext, HeadContent, Outlet, Scripts, useRouter } from "@tanstack/react-router"
import { type RouterContext, UIProvider } from "@go-tenon/ui"
import { IconAddressBook, IconInbox } from "@tabler/icons-react"
import { theme } from "../theme"

// 根路由：整个 HTML 文档。页面在 <Outlet /> 中。
export const Route = createRootRouteWithContext<RouterContext>()({
  head: () => ({
    meta: [{ charSet: "utf-8" }, { name: "viewport", content: "width=device-width, initial-scale=1" }, { title: "agents-desk" }],
  }),
  shellComponent: Document,
  component: Outlet,
})

// 框架自带的图标之外，图标栏中模块用到的图标。
const icons = { inbox: IconInbox, contacts: IconAddressBook }

function Document({ children }: { children: ReactNode }) {
  // nonce 由 Go 为每个请求生成，内联脚本带上它才能通过内容安全策略。
  const nonce = useRouter().options.ssr?.nonce
  return (
    <html lang="zh-CN" {...mantineHtmlProps}>
      <head>
        <ColorSchemeScript nonce={nonce} />
        <HeadContent />
      </head>
      <body>
        <UIProvider theme={theme} icons={icons}>
          {children}
        </UIProvider>
        <Scripts />
      </body>
    </html>
  )
}
