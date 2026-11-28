import { createFileRoute } from "@tanstack/react-router"
import { preloadQueries } from "@go-tenon/ui"
import { kit } from "../../../../../../api"
import { TelegramAccountStudio } from "../../../../../../features/business/telegram-account-studio"

// Telegram 个人号商务渠道配置工作台：管理连接健康、同步策略、文件夹/白名单会话与通用商务策略。
export const Route = createFileRoute("/$slug/settings/_personal/business-channels/telegram/$id")({
  staticData: { title: "tg_studio.title" },
  loader: ({ context, params }) =>
    preloadQueries(context.app.queryClient, [
      kit.queries.tgaccount.get(params.id),
      kit.queries.tgaccount.list(),
      kit.queries.tag.list({ per_page: 100 }),
    ]),
  component: TelegramAccountDetailPage,
})

function TelegramAccountDetailPage() {
  const { slug, id } = Route.useParams()
  return <TelegramAccountStudio slug={slug} id={id} />
}
