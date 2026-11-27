import { createFileRoute } from "@tanstack/react-router"
import { Page, useT } from "@go-tenon/ui"
import { NotificationsForm } from "../../../../notifications"

// 通知设置：新会话指派给我、我负责的会话有新消息、有人 @ 我时的浏览器通知与提示音开关。账号级设置，在所有工作区生效。
export const Route = createFileRoute("/$slug/settings/_personal/notifications")({
  staticData: { title: "nav.notifications", icon: "bell", order: 2 },
  component: Notifications,
})

function Notifications() {
  const t = useT()
  return (
    <Page title={t("nav.notifications")} description={t("notifications_page.description")}>
      <NotificationsForm />
    </Page>
  )
}
