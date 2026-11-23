import { createFileRoute } from "@tanstack/react-router"
import { Widget } from "../chat/Widget"

// 聊天窗口：/widget/?channel=<聊天插件>。浮窗嵌入网页时 loader.js 加上 embed=1；H5 页面容器全屏嵌入时加上 embed=1&inline=1；
// 宿主自己有导航栏时可以加 hide_close=1 隐藏内置关闭按钮。
export const Route = createFileRoute("/")({ component: Widget })
