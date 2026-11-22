import { createFileRoute, redirect } from "@tanstack/react-router"

// 打开 /t/{slug}/reports 时默认进入第一张报表：会话。
export const Route = createFileRoute("/t/$slug/reports/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/t/$slug/reports/conversations", params })
  },
})
