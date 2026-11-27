import { createFileRoute, redirect } from "@tanstack/react-router"

// 报表模块默认入口：重定向到概览报表。
export const Route = createFileRoute("/t/$slug/reports/")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/t/$slug/reports/overview", params: { slug: params.slug } })
  },
})
