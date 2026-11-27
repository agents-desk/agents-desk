import { createFileRoute, redirect } from "@tanstack/react-router"

// 打开 /app/{slug}/settings 时进入第一个设置页：个人设置。
export const Route = createFileRoute("/$slug/settings/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/$slug/settings/profile", params })
  },
})
