import { createFileRoute, redirect } from "@tanstack/react-router"
import { requireAuth } from "@go-tenon/ui"
import { api } from "../api"
import { readLastWorkspace } from "../workspace"

// 工作台智能入口 /app/：
// 1. 优先进入上次访问且仍处于活跃状态的工作区；
// 2. 否则若当前账号仅有一个活跃工作区，直接进入；
// 3. 其余情况（多个活跃工作区且无记录，或暂无活跃工作区）进入工作区总览选择页 /app/workspaces。
export const Route = createFileRoute("/")({
  beforeLoad: requireAuth,
  loader: async ({ context }) => {
    const mine = await api.tenant.mine()
    const active = mine.filter((tn) => tn.status === "active")
    const accountId = context.app.session.identity()?.account
    const lastSlug = readLastWorkspace(context.app, accountId)
    if (lastSlug && active.some((tn) => tn.slug === lastSlug)) {
      throw redirect({ to: "/$slug", params: { slug: lastSlug } })
    }
    if (active.length === 1) {
      throw redirect({ to: "/$slug", params: { slug: active[0].slug } })
    }
    throw redirect({ to: "/workspaces" })
  },
  component: () => null,
})
