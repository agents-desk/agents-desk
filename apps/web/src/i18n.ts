import { createI18n } from "@go-tenon/client"
import { messages } from "./messages.gen"

// i18n 是工作区端的界面文字：locales/web.<语言>.yaml 生成的 messages.gen.ts。它交给 createApp（页面、菜单和组件的文字）、
// 生成客户端的 headers（服务端的提示）和 createKit（字段名、枚举、验证提示）。
// 语言跟随成员在偏好设置中选的语言：读取当前成员时切换，退出后回到浏览器的语言（见 api.ts 的 session）；翻译文件中没有的语言用简体中文。
export const i18n = createI18n({ messages, defaultLanguage: "zh-CN" })
