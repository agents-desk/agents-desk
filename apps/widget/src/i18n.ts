import { createI18n } from "@go-tenon/client"
import { messages } from "./messages.gen"

// i18n 是聊天窗口的界面文字：locales/widget.<语言>.yaml 生成的 messages.gen.ts。
// 语言跟随客户的浏览器或手机系统：偏好中文时用简体中文，其他语言用英文（defaultLanguage）。
export const i18n = createI18n({ messages, defaultLanguage: "en" })
