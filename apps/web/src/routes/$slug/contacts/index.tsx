import { createFileRoute } from "@tanstack/react-router"

// 联系人名录的默认子路由：没有选中联系人时右侧分栏收起，列表由父布局 contacts.tsx 占满全部宽度。
export const Route = createFileRoute("/$slug/contacts/")({
  component: () => null,
})
