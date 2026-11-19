import { createFileRoute, redirect } from "@tanstack/react-router"

// 管理员端的首页就是工作区列表。没有 staticData，不出现在菜单中。
export const Route = createFileRoute("/_app/")({
  beforeLoad: () => {
    throw redirect({ to: "/tenants" })
  },
})
