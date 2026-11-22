import { createFileRoute } from "@tanstack/react-router"
import { Page, useT } from "@go-tenon/ui"
import { StatusControl } from "../../../../../status"

// 在线状态：我在这个工作区中是在线还是离开，离开时是否把我的会话重新分配。每个工作区分开设置，修改后立即保存。
// 个人菜单中也能切换。
export const Route = createFileRoute("/t/$slug/settings/_personal/status")({
  staticData: { title: "nav.status", icon: "clock", order: 2 },
  component: Status,
})

function Status() {
  const t = useT()
  return (
    <Page title={t("nav.status")} description={t("status_page.description")} width="narrow">
      <StatusControl description />
    </Page>
  )
}
