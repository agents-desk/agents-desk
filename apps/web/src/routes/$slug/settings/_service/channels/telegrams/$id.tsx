import { createFileRoute } from "@tanstack/react-router"
import { preloadQueries } from "@go-tenon/ui"
import { tenantApi } from "../../../../../../api"
import { TelegramStudio } from "../../../../../../features/telegram/telegram-studio"

// Telegram 机器人配置工作台：基本配置与 Webhook 接入。权限继承自上级的 channels 布局。
export const Route = createFileRoute("/$slug/settings/_service/channels/telegrams/$id")({
  staticData: { title: "telegram_install.title" },
  loader: ({ context, params }) =>
    preloadQueries(context.app.queryClient, [
      { queryKey: ["telegram", params.id], queryFn: () => tenantApi.telegram.get(params.id) },
      { queryKey: ["telegram", params.id, "installation"], queryFn: () => tenantApi.telegram.installation(params.id) },
    ]),
  component: TelegramDetailRoute,
})

function TelegramDetailRoute() {
  const { slug, id } = Route.useParams()
  return <TelegramStudio slug={slug} id={id} />
}
