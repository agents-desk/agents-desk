import { createFileRoute } from "@tanstack/react-router"
import { preloadQueries } from "@go-tenon/ui"
import { tenantApi } from "../../../../../../api"
import { WeComStudio } from "../../../../../../features/wecom/wecom-studio"

// 企业微信智能机器人配置工作台：长连接凭据、欢迎语、AI 接待占位与团队路由。权限继承自上级的 internal-channels 布局。
export const Route = createFileRoute("/$slug/settings/_service/internal-channels/wecom/$id")({
  staticData: { title: "wecom_studio.title" },
  loader: ({ context, params }) =>
    preloadQueries(context.app.queryClient, [{ queryKey: ["wecom", params.id], queryFn: () => tenantApi.wecom.get(params.id) }]),
  component: WeComDetailRoute,
})

function WeComDetailRoute() {
  const { slug, id } = Route.useParams()
  return <WeComStudio slug={slug} id={id} />
}
