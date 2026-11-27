import { createFileRoute } from "@tanstack/react-router"
import { TelegramStudio } from "../../../../../../features/telegram/telegram-studio"
import { warmRoute } from "../../../../../../preload"
import { tenantApi } from "../../../../../../api"

// Telegram 机器人配置工作台：基本配置与 Webhook 接入。权限来自上级的 telegrams 布局，菜单中“Telegram 机器人”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_channels/telegrams/$id")({
  staticData: { title: "telegram_install.title" },
  // 预取页面的数据，最多等一小会儿：切换到这一页时内容一次完整出现，不先空白再出现
  loader: ({ context, params }) =>
    warmRoute(context.app.queryClient, [
      { queryKey: ["telegram", params.id], queryFn: () => tenantApi.telegram.get(params.id) },
      { queryKey: ["telegram", params.id, "installation"], queryFn: () => tenantApi.telegram.installation(params.id) },
    ]),
  component: TelegramDetailRoute,
})

function TelegramDetailRoute() {
  const { slug, id } = Route.useParams()
  return <TelegramStudio slug={slug} id={id} />
}
