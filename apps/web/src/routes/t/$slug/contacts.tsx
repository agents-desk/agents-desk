import { Box } from "@mantine/core"
import { useMediaQuery } from "@mantine/hooks"
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router"
import { useCollapseNav, useNarrow } from "@go-tenon/ui"
import "../../../contacts/contacts.css"
import { ContactDirectory } from "../../../contacts/List"
import { ContactsNav } from "../../../contacts/Nav"
import { type ContactsSearch, validateContactsSearch } from "../../../contacts/search"

// 联系人模块：与收件箱一致的分栏工作台布局（fullBleed）。二级导航是全部/用户/线索（nav 插槽），
// 左侧常驻联系人名录，选中某位联系人后右侧展开 440px 档案分栏（子路由 contacts/$id），中屏自动收起二级导航，窄屏轮流占满。
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
  const narrow = useNarrow()
  const wide = useMediaQuery("(min-width: 1440px)")

  // 中屏展开右侧档案分栏时自动收起二级导航，关闭后恢复（与收件箱详情栏一致）
  useCollapseNav(Boolean(id) && !wide && !narrow)

  // go 更新筛选或页码：已打开的右侧档案保持打开
  const go = (patch: Partial<ContactsSearch>) => {
    const next = { ...search, ...patch }
    void (id
      ? navigate({ to: "/t/$slug/contacts/$id", params: { slug, id }, search: next, replace: true })
      : navigate({ to: "/t/$slug/contacts", params: { slug }, search: next, replace: true }))
  }

  return (
    <Box style={{ display: "flex", height: "100%" }}>
      <ContactsNav search={search} go={go} />
      {(!narrow || !id) && (
        <Box style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
          <ContactDirectory search={search} openId={id} go={go} />
        </Box>
      )}
      {id && (
        <Box
          w={narrow ? "100%" : 440}
          style={{ flex: "none", minWidth: 0, display: "flex" }}
          className={narrow ? undefined : "ct-aside"}
        >
          <Outlet />
        </Box>
      )}
    </Box>
  )
}
