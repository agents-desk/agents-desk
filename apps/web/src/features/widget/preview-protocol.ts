import type { WidgetAppearance, WidgetBusinessIntakeConfig } from "@agents-desk/client/tenant"

/** PREVIEW_PREFIX 是配置工作台与 /widget/preview 之间 postMessage 的消息前缀。 */
export const PREVIEW_PREFIX = "agents-desk:"

/** PreviewMode 区分公共网页聊天插件与个人网页商务沟通链接。 */
export type PreviewMode = "widget" | "business_link"

/** PreviewScene 是预览窗口展示的场景：对话中、收起态启动气泡、开场留资或口令校验。 */
export type PreviewScene = "conversation" | "launcher" | "intake" | "passcode"

/** PreviewViewport 是预览窗口模拟的视口形态：桌面或手机全屏。 */
export type PreviewViewport = "desktop" | "mobile"

/** PreviewBusinessOwner 是预览中展示的商务名片归属人信息。 */
export interface PreviewBusinessOwner {
  name: string
  title?: string
  company?: string
  bio?: string
  avatar_url?: string
  status: "online" | "away"
}

/** PreviewBusinessPayload 是商务沟通链接工作台推送给预览窗口的实时草稿。 */
export interface PreviewBusinessPayload {
  owner?: PreviewBusinessOwner
  away_message?: string
  intake?: WidgetBusinessIntakeConfig
  starters?: string[]
  requires_passcode?: boolean
}

/** PreviewMessage 是工作台推送给 /widget/preview 的实时草稿消息。 */
export interface PreviewMessage {
  type: `${typeof PREVIEW_PREFIX}preview`
  mode?: PreviewMode
  lang?: string
  name: string
  appearance: WidgetAppearance
  scene: PreviewScene
  viewport: PreviewViewport
  business?: PreviewBusinessPayload
}
