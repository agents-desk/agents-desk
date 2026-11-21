import { createI18n } from "@go-tenon/client"
import { messages } from "./messages.gen"

// i18n 是管理员端的界面文字：locales/admin.<语言>.yaml 生成的 messages.gen.ts。它交给 createApp（页面、菜单和组件的文字）、
// 生成客户端的 headers（服务端的提示）和 createKit（字段名、枚举、验证提示）。
// 语言按浏览器的偏好选择，翻译文件中没有的语言用简体中文。
export const i18n = createI18n({ messages, defaultLanguage: "zh-CN" })
