import { createFileRoute } from "@tanstack/react-router"

// 团队收件箱的布局：菜单项和权限写在这里，列表（teams/index.tsx）和团队收件箱的成员（teams/$id.tsx）都在它下面，
// 进入某个团队收件箱时菜单中的“团队收件箱”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_workspace/teams")({
  staticData: { title: "团队收件箱", icon: "team", order: 3, permission: "teams:manage" },
})
