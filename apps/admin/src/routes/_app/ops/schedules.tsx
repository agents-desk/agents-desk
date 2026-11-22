import { createFileRoute } from "@tanstack/react-router"
import { SchedulePanel } from "@go-tenon/ui"
import { api } from "../../../api"

// 定时任务面板：展示全部已注册定时任务、支持立即执行并查看执行历史。
export const Route = createFileRoute("/_app/ops/schedules")({
  staticData: { title: "nav.schedules", icon: "clock", order: 2, permission: "schedules:manage" },
  component: () => <SchedulePanel api={api.schedule} />,
})
