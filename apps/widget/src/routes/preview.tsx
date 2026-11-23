import { createFileRoute } from "@tanstack/react-router"
import { Preview } from "../chat/Preview"

// 聊天窗口预览路由：/widget/preview。仅允许同源工作区设置工作台以 iframe 嵌入，实时渲染未保存的外观草稿。
export const Route = createFileRoute("/preview")({ component: Preview })
