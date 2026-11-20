import { createFileRoute } from "@tanstack/react-router"

// 工作区设置：一个分组，不进图标栏，从个人菜单的“设置”进入（RailLayout 的 settings）；其下的页面是二级导航。
// 设置按分组放在没有路径的布局路由中，分组和顺序见产品设计的 13.1。分组的文件和 order：
//   _personal 个人 0、_workspace 工作区 1、_inbox 收件箱 2、_channels 渠道 3、_data 数据 4、_translation 翻译 5、_automation 自动化 6。
// 分组在它的第一个设置页加入时建立：没有子页面的分组路由与 /settings 的地址冲突，路由树生成失败。
export const Route = createFileRoute("/t/$slug/settings")({
  staticData: { title: "设置", icon: "settings", order: 9 },
})
