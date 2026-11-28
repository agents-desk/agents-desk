import { createFileRoute } from "@tanstack/react-router"
import { SchedulePanel, useT } from "@go-tenon/ui"
import { api } from "../../../api"

// 定时任务面板：展示全部已注册定时任务、支持立即执行并查看执行历史。
export const Route = createFileRoute("/_app/ops/schedules")({
  staticData: { title: "nav.schedules", icon: "clock", order: 9, permission: "schedules:manage" },
  component: Schedules,
})

function Schedules() {
  const t = useT()
  return <SchedulePanel api={api.schedule} title={t("nav.schedules")} />
}
