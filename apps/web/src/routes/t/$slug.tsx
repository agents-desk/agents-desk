import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconSettings, IconSwitchHorizontal } from "@tabler/icons-react"
import { enterTenant, Fill, RailLayout, requireAuth, TimeZoneProvider, UserMenu, useMenuBadge, useTenant, useT } from "@go-tenon/ui"
import { kit, tenantApi } from "../../api"
import { logout, me } from "../../auth"
import { RealtimeContext, useRealtime } from "../../realtime"
import { StatusControl } from "../../status"

// 工作区的布局路由：地址中的 slug 就是当前工作区。进入时会话的身份切换到这个工作区，再读取工作区和成员的权限：
// 不是成员（包括 slug 不存在）时是 404，工作区停用时是 403，由 RouteError 显示。它没有 title，下面的页面才是菜单项。
export const Route = createFileRoute("/t/$slug")({
  beforeLoad: async (ctx) => {
    await requireAuth(ctx)
    enterTenant(ctx.context.app, ctx.params.slug)
  },
  loader: () => tenantApi.tenant.current(),
  component: TenantLayout,
})

function TenantLayout() {
  const tenant = Route.useLoaderData()
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  const { tenants } = useTenant()
  const t = useT()
  // 实时推送：会话有变化时刷新收件箱的数量和会话；图标栏中收件箱的角标是“我的”会话中等待回复的数量。
  // 同一条连接交给页面（RealtimeContext），收件箱用它加入打开着的会话的频道
  const centrifuge = useRealtime(tenant.slug, tenant.id, user.data?.id)
  const inbox = useQuery(kit.queries.conversation.inbox())
  useMenuBadge("/t/$slug/inbox", inbox.data?.mine.waiting)
  // 工作区设置不进图标栏，从个人菜单进入；属于多个工作区时，个人菜单中还可以回到选择工作区的页面。
  const settings = `/t/${tenant.slug}/settings`
  const items = [
    { label: t("nav.settings"), icon: <IconSettings size={16} />, to: settings },
    ...(tenants.length > 1 ? [{ label: t("nav.switch_tenant"), icon: <IconSwitchHorizontal size={16} />, to: "/" }] : []),
  ]
  // 页面中的时间按成员在偏好设置中选的时区显示，没有选时按浏览器的时区。
  // 二级导航 200px：收件箱列表、设置项都是短的中文，给收件箱、聊天这类铺满的模块留出更多宽度。
  return (
    <TimeZoneProvider zone={user.data?.time_zone}>
      <RealtimeContext.Provider value={centrifuge}>
        <RailLayout
          title={tenant.name}
          settings={settings}
          navWidth={200}
          user={
            <UserMenu
              name={user.data?.name}
              email={user.data?.email}
              avatar={user.data?.avatar_url || undefined}
              position="right-end"
              items={items}
              logout={logout}
            />
          }
        />
        {/* 个人菜单中切换我在这个工作区的在线状态 */}
        <Fill name="user-menu">
          <div className="tn-menu-section">
            <StatusControl />
          </div>
        </Fill>
      </RealtimeContext.Provider>
    </TimeZoneProvider>
  )
}
