import { createFileRoute, redirect } from "@tanstack/react-router"

// 打开 /t/{slug}/settings 时进入第一个设置页。
export const Route = createFileRoute("/t/$slug/settings/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/t/$slug/settings/general", params })
  },
})
