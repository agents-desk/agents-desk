import { createFileRoute } from "@tanstack/react-router"
import { TelegramStudio } from "../../../../../../features/telegram/telegram-studio"

// Telegram 机器人配置工作台：基本配置与 Webhook 接入。权限来自上级的 telegrams 布局，菜单中“Telegram 机器人”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_channels/telegrams/$id")({
  staticData: { title: "telegram_install.title" },
  component: TelegramDetailRoute,
})

function TelegramDetailRoute() {
  const { slug, id } = Route.useParams()
  return <TelegramStudio slug={slug} id={id} />
}
