import { createFileRoute } from "@tanstack/react-router"
import { Widget } from "../chat/Widget"

// 聊天窗口：/widget/?channel=<聊天插件>。嵌入网页时 loader.js 加上 embed=1；App 用 WebView 打开时可以加 hide_close=1，
// 由 App 自己的导航栏关闭。
export const Route = createFileRoute("/")({ component: Widget })
