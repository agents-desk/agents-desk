import { Text } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { firstPage, Page, useApp, useNavigation, useT } from "@go-tenon/ui"
import { useEffect } from "react"

// 工作区的首页 /app/{slug}/：进入图标栏中第一个可以进入的模块，通常是收件箱；没有会话权限的成员进入下一个模块。
// 菜单按权限过滤，权限读取完成之前菜单中只有不要求权限的模块，所以等权限读取完成再决定去哪里。
export const Route = createFileRoute("/$slug/")({
  component: Home,
})

function Home() {
  const app = useApp()
  const t = useT()
  const permissions = useQuery({ ...app.permissionsQuery(), enabled: app.signedIn() })
  const { menu } = useNavigation()
  const navigate = useNavigate()
  const to = permissions.isSuccess ? firstPage(menu) : undefined
  // 只在目标变化时跳转一次：跳转的过程中菜单会重新计算，按渲染跳转会反复触发
  useEffect(() => {
    if (to) void navigate({ to, replace: true })
  }, [to, navigate])
  if (!permissions.isSuccess || to) return null
  return (
    <Page title={t("home.title")}>
      <Text c="dimmed">{t("home.no_pages")}</Text>
    </Page>
  )
}
