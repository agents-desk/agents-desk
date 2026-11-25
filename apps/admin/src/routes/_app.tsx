import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconUser } from "@tabler/icons-react"
import { FrameLayout, NotificationsMenu, type NotificationsSource, OperationsMenu, requireAuth, UserMenu, useT } from "@go-tenon/ui"
import { useMemo } from "react"
import { api, kit } from "../api"
import { logout, me } from "../auth"

// 需要登录的页面都在 _app 之下：没有登录时跳到登录页，登录后回来。
export const Route = createFileRoute("/_app")({ beforeLoad: requireAuth, component: Layout })

function Layout() {
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  const site = useQuery(kit.queries.site.get()) // 顶栏与浏览器标签页名称来自平台常规设置
  const t = useT()
  const inbox = useMemo<NotificationsSource>(
    () => ({
      list: (before) => api.notification.list({ before }).then((r) => r.items),
      markRead: (ids) => api.notification.read({ ids }),
      markAllRead: () => api.notification.readAll(),
      badge: (o) => api.notification.inbox(o),
    }),
    [],
  )
  // 每个页面都有的部分是 FrameLayout 的参数；某个页面才有的按钮，由页面用 <Fill name="actions"> 放进顶栏。
  // tabs 开启多标签；actions 中放置站内通知铃铛与后台任务进度中心。
  const items = [{ label: t("nav.profile"), icon: <IconUser size={16} />, to: "/profile" }]
  return (
    <FrameLayout
      title={site.data?.site_name ?? ""}
      tabs
      actions={
        <>
          <NotificationsMenu source={inbox} />
          <OperationsMenu />
        </>
      }
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
