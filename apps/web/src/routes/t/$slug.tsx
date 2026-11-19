import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconSettings, IconSwitchHorizontal } from "@tabler/icons-react"
import { enterTenant, RailLayout, requireAuth, TimeZoneProvider, UserMenu, useTenant } from "@go-tenon/ui"
import { tenantApi } from "../../api"
import { logout, me } from "../../auth"

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
  // 工作区设置不进图标栏，从个人菜单进入；属于多个工作区时，个人菜单中还可以回到选择工作区的页面。
  const settings = `/t/${tenant.slug}/settings`
  const items = [
    { label: "设置", icon: <IconSettings size={16} />, to: settings },
    ...(tenants.length > 1 ? [{ label: "切换工作区", icon: <IconSwitchHorizontal size={16} />, to: "/" }] : []),
  ]
  // 页面中的时间按成员在偏好设置中选的时区显示，没有选时按浏览器的时区。
  return (
    <TimeZoneProvider zone={user.data?.time_zone}>
      <RailLayout
        title={tenant.name}
        settings={settings}
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
    </TimeZoneProvider>
  )
}
