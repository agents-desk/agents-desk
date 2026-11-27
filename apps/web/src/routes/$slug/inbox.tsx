import { createFileRoute, Outlet } from "@tanstack/react-router"
import "../../inbox/inbox.css"
import { InboxNav } from "../../inbox/Nav"

// 收件箱模块：成员处理所有沟通的地方，铺满内容区（fullBleed），见产品设计的 13.2。
// 二级导航（<InboxNav>，nav 插槽）在这里渲染一次，列出客服会话、团队收件箱、工单、内部聊天、商务沟通和视图；
// 右侧是当前分组的列表和打开的内容，由三个子路由提供：会话 /inbox（_conversations）、工单 /inbox/tickets、聊天 /inbox/chat。
// 它们都在这个模块之下，所以在哪个分组中图标栏上都是收件箱高亮。
export const Route = createFileRoute("/$slug/inbox")({
  staticData: { title: "nav.inbox", icon: "inbox", order: 1, permission: "conversations:read", fullBleed: true },
  component: InboxModule,
})

function InboxModule() {
  return (
    <>
      <InboxNav />
      <Outlet />
    </>
  )
}
