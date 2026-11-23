import type { WidgetAppearance, WidgetBehavior } from "@agents-desk/client/tenant"

/** PREVIEW_PREFIX 是配置工作台与 /widget/preview 之间 postMessage 的消息前缀。 */
export const PREVIEW_PREFIX = "agents-desk:"

/** PreviewScene 是预览窗口展示的场景：首页、对话中、收起态启动气泡。 */
export type PreviewScene = "home" | "conversation" | "launcher"

/** PreviewViewport 是预览窗口模拟的视口形态：桌面浮窗或手机全屏。 */
export type PreviewViewport = "desktop" | "mobile"

/** PreviewMessage 是工作台推送给 /widget/preview 的实时草稿消息。 */
export interface PreviewMessage {
  type: `${typeof PREVIEW_PREFIX}preview`
  name: string
  appearance: WidgetAppearance
  behavior: WidgetBehavior
  scene: PreviewScene
  viewport: PreviewViewport
}
