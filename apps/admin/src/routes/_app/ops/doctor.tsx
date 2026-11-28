import { createFileRoute } from "@tanstack/react-router"
import { DoctorPanel, useT } from "@go-tenon/ui"
import { api } from "../../../api"

// 运行环境自检页面：调用 /admin/api/system/doctor，按依赖分组展示检查结果和修复指引。
export const Route = createFileRoute("/_app/ops/doctor")({
  staticData: { title: "nav.doctor", icon: "task", order: 10, permission: "system:read" },
  component: Doctor,
})

function Doctor() {
  const t = useT()
  return <DoctorPanel api={api.system} title={t("nav.doctor")} />
}
