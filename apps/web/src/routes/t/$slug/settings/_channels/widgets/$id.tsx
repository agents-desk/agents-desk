import { createFileRoute } from "@tanstack/react-router"
import { WidgetStudio } from "../../../../../../features/widget/widget-studio"

// 网页聊天插件配置工作台：配置与实时预览、安装与接入。权限来自上级的 widgets 布局，菜单中“网页聊天插件”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_channels/widgets/$id")({
  staticData: { title: "widget_install.title" },
  component: WidgetDetailRoute,
})

function WidgetDetailRoute() {
  const { slug, id } = Route.useParams()
  return <WidgetStudio slug={slug} id={id} />
}
