import { createFileRoute } from "@tanstack/react-router"

// 工作区设置：从个人菜单的“设置”进入（RailLayout 的 settings），其下的页面构成二级导航。
// 设置按分组放在无路径布局路由中，分组文件和 order：
//   _personal 个人 0、_workspace 工作区 1、_service 客服与渠道 2。
// 分组在首个设置页加入时建立，以保证路由树生成正常。
export const Route = createFileRoute("/$slug/settings")({
  staticData: { title: "nav.settings", icon: "settings", order: 9 },
})
