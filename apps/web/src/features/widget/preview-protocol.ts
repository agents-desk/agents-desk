import type {
  WidgetAppearance,
  WidgetBusinessIntakeConfig,
  WidgetLocalization,
  WidgetLocalizedCopy,
} from "@agents-desk/client/tenant"
import { sameLanguage } from "../../languages"

/** PREVIEW_PREFIX 是配置工作台与 /widget/preview 之间 postMessage 的消息前缀。 */
export const PREVIEW_PREFIX = "agents-desk:"

/** WIDGET_BUILTIN_LANGUAGES 是网页聊天窗口内置的 5 种界面语种，与后端 widget.SupportedLanguages 相同。 */
export const WIDGET_BUILTIN_LANGUAGES = ["en", "zh-CN", "zh-TW", "hi", "hi-Latn"] as const

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
  greeting?: string
  appearance: WidgetAppearance
  language: string
  scene: PreviewScene
  viewport: PreviewViewport
  business?: PreviewBusinessPayload
}

/** lookupCopy 在翻译表中按精确标签或同语言变体查找单语种文案。 */
function lookupCopy(translations: Record<string, WidgetLocalizedCopy> | undefined, target: string): WidgetLocalizedCopy | undefined {
  if (!translations || !target) return undefined
  if (translations[target]) return translations[target]
  for (const key of Object.keys(translations)) {
    if (sameLanguage(key, target)) return translations[key]
  }
  return undefined
}

/** resolvePreviewCopy 在前端复现后端的字段级四级回退规则，用于右侧预览舞台即时渲染指定语言草稿。 */
export function resolvePreviewCopy(loc: WidgetLocalization, requestedLang: string): WidgetLocalizedCopy {
  const base = lookupCopy(loc.translations, loc.default_language || "en") ?? {}
  const hit = lookupCopy(loc.translations, requestedLang)
  if (!hit) return base
  return {
    title: hit.title?.trim() || base.title?.trim() || "",
    greeting: hit.greeting?.trim() || base.greeting?.trim() || "",
    owner_title: hit.owner_title?.trim() || base.owner_title?.trim() || "",
    owner_company: hit.owner_company?.trim() || base.owner_company?.trim() || "",
    owner_bio: hit.owner_bio?.trim() || base.owner_bio?.trim() || "",
    away_message: hit.away_message?.trim() || base.away_message?.trim() || "",
    starters: hit.starters && hit.starters.length > 0 ? hit.starters : (base.starters ?? []),
  }
}

/** computePreviewLanguages 返回工作台预览与定向短链可选的语言列表（内置语种 + 已配置翻译的额外语种）。 */
export function computePreviewLanguages(loc: WidgetLocalization): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  const add = (tag: string) => {
    if (!tag || seen.has(tag)) return
    seen.add(tag)
    out.push(tag)
  }
  for (const tag of WIDGET_BUILTIN_LANGUAGES) add(tag)
  for (const tag of Object.keys(loc.translations ?? {}).sort()) add(tag)
  return out
}
