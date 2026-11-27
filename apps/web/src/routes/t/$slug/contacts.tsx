import { Box } from "@mantine/core"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { AsidePane, Pane } from "@go-tenon/ui"
import "../../../contacts/contacts.css"
import { ContactDirectory, contactListQuery } from "../../../contacts/List"
import { ContactsNav } from "../../../contacts/Nav"
import { type ContactsSearch, validateContactsSearch } from "../../../contacts/search"
import { NARROW_QUERY } from "../../../layout"
import { warmRoute } from "../../../preload"

// 联系人模块：与收件箱一致的分栏工作台布局（fullBleed）。二级导航是全部/用户/线索（nav 插槽），
// 左侧常驻联系人名录，选中某位联系人后由框架 AsidePane 响应式展示档案分栏（子路由 contacts/$id）。
export const Route = createFileRoute("/t/$slug/contacts")({
  staticData: { title: "nav.contacts", icon: "contacts", order: 3, permission: "contacts:read", fullBleed: true },
  validateSearch: validateContactsSearch,
  // 切换分组、视图和筛选时预取联系人名录，最多等一小会儿：数据很快到时新名录一次完整出现，不先闪成骨架
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => warmRoute(context.app.queryClient, [contactListQuery(deps)]),
  component: ContactsLayout,
})

function ContactsLayout() {
  const search = Route.useSearch()
  const { slug } = Route.useParams()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()

  // go 更新筛选或页码：已打开的右侧档案保持打开
  const go = (patch: Partial<ContactsSearch>) => {
    const next = { ...search, ...patch }
    void (id
      ? navigate({ to: "/t/$slug/contacts/$id", params: { slug, id }, search: next, replace: true })
      : navigate({ to: "/t/$slug/contacts", params: { slug }, search: next, replace: true }))
  }

  const closeAside = () => void navigate({ to: "/t/$slug/contacts", params: { slug }, search })

  return (
    <Box style={{ display: "flex", height: "100%" }}>
      <ContactsNav search={search} go={go} />
      <Pane>
        <ContactDirectory search={search} openId={id} go={go} />
      </Pane>
      <AsidePane
        opened={Boolean(id)}
        onClose={closeAside}
        inlineQuery="(min-width: 1200px)"
        keepNavQuery="(min-width: 1440px)"
        compactQuery={NARROW_QUERY}
        resizable={{ storageKey: "contact-aside", defaultWidth: 420, min: 360, max: 540 }}
      >
        <Outlet />
      </AsidePane>
    </Box>
  )
}
