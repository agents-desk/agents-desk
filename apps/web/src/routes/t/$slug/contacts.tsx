import { createFileRoute, Outlet } from "@tanstack/react-router"
import { ContactsNav } from "../../../contacts/Nav"

// 联系人模块的布局路由：二级导航是全部、用户、线索（nav 插槽），右侧显示列表页（contacts/）或资料页（contacts/$id）。
export const Route = createFileRoute("/t/$slug/contacts")({
  staticData: { title: "nav.contacts", icon: "contacts", order: 2, permission: "contacts:read" },
  component: ContactsLayout,
})

function ContactsLayout() {
  return (
    <>
      <ContactsNav />
      <Outlet />
    </>
  )
}
