import { createFileRoute } from "@tanstack/react-router"
import { preloadQueries } from "@go-tenon/ui"
import { kit } from "../../../../../../api"
import { BusinessLinkStudio } from "../../../../../../features/business/business-link-studio"

// 个人网页商务沟通链接配置工作台：配置与实时预览、短链分享与二维码。权限来自上级的 business-channels 布局。
export const Route = createFileRoute("/$slug/settings/_personal/business-channels/links/$id")({
  staticData: { title: "biz_studio.title" },
  loader: ({ context, params }) =>
    preloadQueries(context.app.queryClient, [
      kit.queries.business.linkGet(params.id),
      kit.queries.business.policyGet(),
      kit.queries.tag.list(),
    ]),
  component: BusinessLinkDetailRoute,
})

function BusinessLinkDetailRoute() {
  const { slug, id } = Route.useParams()
  return <BusinessLinkStudio slug={slug} id={id} />
}
