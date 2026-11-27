import { createFileRoute } from "@tanstack/react-router"
import { WidgetStudio } from "../../../../../../features/widget/widget-studio"
import { warmRoute } from "../../../../../../preload"
import { tenantApi } from "../../../../../../api"

// 网页聊天插件配置工作台：配置与实时预览、安装与接入。权限来自上级的 widgets 布局，菜单中“网页聊天插件”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_channels/widgets/$id")({
  staticData: { title: "widget_install.title" },
  // 预取页面的数据，最多等一小会儿：切换到这一页时内容一次完整出现，不先空白再出现
  loader: ({ context, params }) =>
    warmRoute(context.app.queryClient, [{ queryKey: ["widget", params.id], queryFn: () => tenantApi.widget.get(params.id) }]),
  component: WidgetDetailRoute,
})

function WidgetDetailRoute() {
  const { slug, id } = Route.useParams()
  return <WidgetStudio slug={slug} id={id} />
}
