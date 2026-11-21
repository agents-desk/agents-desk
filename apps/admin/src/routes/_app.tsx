import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconUser } from "@tabler/icons-react"
import { FrameLayout, requireAuth, UserMenu, useT } from "@go-tenon/ui"
import { logout, me } from "../auth"

// 需要登录的页面都在 _app 之下：没有登录时跳到登录页，登录后回来。
export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })

function Layout() {
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  const t = useT()
  // 每个页面都有的部分是 FrameLayout 的参数；某个页面才有的按钮，由页面用 <Fill name="actions"> 放进顶栏。
  // tabs 开启多标签。
  // 头像菜单中进入个人资料。
  const items = [{ label: t("nav.profile"), icon: <IconUser size={16} />, to: "/profile" }]
  return (
    <FrameLayout
      title={t("app.title")}
      tabs
      user={
        <UserMenu
          name={user.data?.name}
          email={user.data?.email}
          avatar={user.data?.avatar_url || undefined}
          items={items}
          logout={logout}
        />
      }
    />
  )
}
