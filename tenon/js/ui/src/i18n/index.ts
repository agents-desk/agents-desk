// @go-tenon/ui/i18n 是轻量国际化入口：供应用与 messages.gen.ts 扩展 I18nRegister，同时兼容 @go-tenon/ui 与 @go-tenon/ui/core。
export {
  I18nProvider,
  type I18nRegister,
  type MessageId,
  type UiMessageId,
  uiMessages,
  useI18n,
  useLanguage,
  useT,
} from "./i18n"
