import { createFileRoute } from "@tanstack/react-router"

// 网页聊天插件的布局：菜单项和权限写在这里，列表（widgets/index.tsx）和一个聊天插件的安装（widgets/$id.tsx）都在它下面，
// 进入某个聊天插件时菜单中的“网页聊天插件”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_channels/widgets")({
  staticData: { title: "nav.widgets", icon: "message", order: 0, permission: "channels:manage" },
})
