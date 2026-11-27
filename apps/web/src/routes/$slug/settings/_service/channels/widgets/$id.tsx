import { createFileRoute } from "@tanstack/react-router"
import { preloadQueries } from "@go-tenon/ui"
import { tenantApi } from "../../../../../../api"
import { WidgetStudio } from "../../../../../../features/widget/widget-studio"

// 网页聊天插件配置工作台：配置与实时预览、安装与接入。权限继承自上级的 channels 布局。
export const Route = createFileRoute("/$slug/settings/_service/channels/widgets/$id")({
  staticData: { title: "widget_install.title" },
  loader: ({ context, params }) =>
    preloadQueries(context.app.queryClient, [{ queryKey: ["widget", params.id], queryFn: () => tenantApi.widget.get(params.id) }]),
  component: WidgetDetailRoute,
})

function WidgetDetailRoute() {
  const { slug, id } = Route.useParams()
  return <WidgetStudio slug={slug} id={id} />
}
