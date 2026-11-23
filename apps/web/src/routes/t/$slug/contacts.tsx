import { Box, Drawer } from "@mantine/core"
import { useMediaQuery } from "@mantine/hooks"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { Pane, useCollapseNav, useNarrow } from "@go-tenon/ui"
import "../../../contacts/contacts.css"
import { ContactDirectory } from "../../../contacts/List"
import { ContactsNav } from "../../../contacts/Nav"
import { type ContactsSearch, validateContactsSearch } from "../../../contacts/search"
import { NARROW_QUERY } from "../../../layout"

// 联系人模块：与收件箱一致的分栏工作台布局（fullBleed）。二级导航是全部/用户/线索（nav 插槽），
// 左侧常驻联系人名录，选中某位联系人后右侧展开可拖拽调节的档案分栏（子路由 contacts/$id）：
// 1440px 以上三栏并排；1200–1439px 自动收起二级导航；900–1199px 改为右侧浮层抽屉；小于 900px 轮流占满。
export const Route = createFileRoute("/t/$slug/contacts")({
  staticData: { title: "nav.contacts", icon: "contacts", order: 2, permission: "contacts:read", fullBleed: true },
  validateSearch: validateContactsSearch,
  component: ContactsLayout,
})

function ContactsLayout() {
  const search = Route.useSearch()
  const { slug } = Route.useParams()
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const wide = useMediaQuery("(min-width: 1440px)") ?? false
  const canInlineAside = useMediaQuery("(min-width: 1200px)") ?? false

  // 1200–1439px 展开内联档案分栏时自动收起二级导航，关闭后恢复
  useCollapseNav(Boolean(id) && canInlineAside && !wide && !narrow)

  // go 更新筛选或页码：已打开的右侧档案保持打开
  const go = (patch: Partial<ContactsSearch>) => {
    const next = { ...search, ...patch }
    void (id
      ? navigate({ to: "/t/$slug/contacts/$id", params: { slug, id }, search: next, replace: true })
      : navigate({ to: "/t/$slug/contacts", params: { slug }, search: next, replace: true }))
  }

  const closeAside = () => void navigate({ to: "/t/$slug/contacts", params: { slug }, search })
  const showInlineAside = Boolean(id) && (narrow || canInlineAside)
  const showDrawerAside = Boolean(id) && !narrow && !canInlineAside

  return (
    <Box style={{ display: "flex", height: "100%" }}>
      <ContactsNav search={search} go={go} />
      {(!narrow || !id) && (
        <Pane>
          <ContactDirectory search={search} openId={id} go={go} />
        </Pane>
      )}
      {showInlineAside && (
        <Pane
          width={narrow ? "100%" : undefined}
          border={narrow ? "none" : "left"}
          resizable={!narrow && { storageKey: "contact-aside", defaultWidth: 420, min: 360, max: 540, side: "left" }}
        >
          <Outlet />
        </Pane>
      )}
      {!narrow && !canInlineAside && (
        <Drawer opened={showDrawerAside} onClose={closeAside} position="right" size={420} padding={0} withCloseButton={false}>
          <Box h="100dvh" style={{ display: "flex" }}>
            {showDrawerAside && <Outlet />}
          </Box>
        </Drawer>
      )}
    </Box>
  )
}
