// 聊天窗口的多语言引擎：与后台管理端语言解耦，独立维护面向全球访客的轻量语种目录。
// en.json 静态内联作为类型基准与兜底，其余语种经 Vite import.meta.glob 按需懒加载。
// 严格区分 requestedLanguage（完整 BCP 47 访客意图，用于 Accept-Language、自定义文案解析和会话冷启动先验）
// 与 language（命中的 UI 界面语种包，用于界面固定文案、排版方向与日期格式化）。

import { formatMessage, type Catalog, type I18n, type Message, type MessageData, type MessageTable } from "@go-tenon/client"
import en from "./locales/en.json"

/** WIDGET_LANGUAGES 是聊天窗口内置的界面语种列表，第一项为全局默认回退语言。 */
export const WIDGET_LANGUAGES = ["en", "zh-CN", "zh-TW", "hi", "hi-Latn"] as const

/** WidgetLanguage 是聊天窗口内置的界面语种标签。 */
export type WidgetLanguage = (typeof WIDGET_LANGUAGES)[number]

/** MessageId 是聊天窗口全部界面词条的强类型 ID，由 en.json 推导。 */
export type MessageId = keyof typeof en

/** RTL_BASES 是采用从右到左排版的主语言集合。 */
const RTL_BASES = new Set(["ar", "he", "fa", "ur", "yi", "ps", "sd", "ug"])

/** directionOf 返回给定 BCP 47 语言的文档排版方向（ltr 或 rtl）。 */
export function directionOf(lang: string): "ltr" | "rtl" {
  const base = lang.trim().replaceAll("_", "-").toLowerCase().split("-")[0]
  return RTL_BASES.has(base) ? "rtl" : "ltr"
}

/** normalizeLang 规范化 BCP 47 标签；非合法标签返回空字符串。 */
export function normalizeLang(raw?: string | null): string {
  const s = (raw ?? "").trim().replaceAll("_", "-")
  if (!s || !/^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$/.test(s)) return ""
  try {
    return Intl.getCanonicalLocales(s)[0] ?? ""
  } catch {
    return ""
  }
}

/**
 * matchWidgetLanguage 从候选 BCP 47 列表中按书写方式与主语言匹配最接近的 Widget 内置界面语种；
 * 区分简体中文（zh-Hans/zh-CN/zh-SG）、繁体中文（zh-Hant/zh-TW/zh-HK/zh-MO）、印地语天城文（hi）与拉丁拼写（hi-Latn）。
 */
export function matchWidgetLanguage(
  wanted: readonly (string | null | undefined)[],
  available: readonly string[] = WIDGET_LANGUAGES,
): string | undefined {
  const availSet = new Set(available)
  for (const item of wanted) {
    const canon = normalizeLang(item)
    if (!canon) continue
    if (availSet.has(canon)) return canon
    try {
      const loc = new Intl.Locale(canon).maximize()
      const base = loc.language.toLowerCase()
      const script = loc.script ?? ""
      if (base === "zh") {
        if (script === "Hant" && availSet.has("zh-TW")) return "zh-TW"
        if (availSet.has("zh-CN")) return "zh-CN"
      }
      if (base === "hi") {
        if (script === "Latn" && availSet.has("hi-Latn")) return "hi-Latn"
        if (availSet.has("hi")) return "hi"
      }
      const exactBase = available.find((a) => a.toLowerCase() === base)
      if (exactBase) return exactBase
      const sameBase = available.find((a) => a.toLowerCase().split("-")[0] === base)
      if (sameBase) return sameBase
    } catch {
      const low = canon.toLowerCase()
      const hit = available.find((a) => a.toLowerCase() === low || a.toLowerCase().split("-")[0] === low.split("-")[0])
      if (hit) return hit
    }
  }
  return undefined
}

/** languageEndonym 返回语言的原生自称，供访客端语言切换器使用。 */
export function languageEndonym(tag: string): string {
  const canon = normalizeLang(tag) || tag
  if (canon.toLowerCase() === "hi-latn") return "Hinglish"
  try {
    const name = new Intl.DisplayNames([canon], { type: "language" }).of(canon)
    if (name) return name
  } catch {
    // 浏览器不支持该标签时回退标签本身
  }
  return canon
}

/** localeLoaders 按语种文件名注册 Vite 懒加载 chunk；en.json 已静态内联。 */
const localeLoaders = import.meta.glob<{ default: Record<string, string> }>("./locales/*.json")

/** loaded 保存已加载完成的语种词条表，初始包含静态内联的 en。 */
const loaded = new Map<string, MessageTable>([["en", en]])

/** inflight 合并对同一语种的并发加载请求。 */
const inflight = new Map<string, Promise<void>>()

/** lower 保存经 extend 加入的低优先级词条（如 @go-tenon/ui 内置文字）。 */
const lower: Catalog[] = []

/** browserLanguages 返回浏览器偏好语言列表。 */
function browserLanguages(): string[] {
  if (typeof window === "undefined" || typeof navigator === "undefined") return []
  if (navigator.languages?.length) return [...navigator.languages]
  return navigator.language ? [navigator.language] : []
}

/** LanguageSources 保存按优先级参与语言协商的各类来源信号。 */
interface LanguageSources {
  hostCommand: string // 1. 宿主运行期 setLanguage 命令
  explicitParam: string // 2. URL ?lang= 或 window.agentsDesk.lang
  hostDocLang: string // 3. 宿主页面 <html lang>
  channelDefault: string // 5. 渠道配置的默认语言
}

const sources: LanguageSources = {
  hostCommand: "",
  explicitParam: "",
  hostDocLang: "",
  channelDefault: "",
}

const listeners = new Set<() => void>()
let currentUI: string = "en"
let currentRequested: string = "en"

function notify() {
  for (const l of [...listeners]) l()
}

/** loadLocale 异步加载非 en 语种的 JSON 词条表并在加载完毕后通知订阅者重渲染。 */
export function ensureLanguage(lang: string): Promise<void> {
  if (loaded.has(lang)) return Promise.resolve()
  const existing = inflight.get(lang)
  if (existing) return existing
  const loader = localeLoaders[`./locales/${lang}.json`]
  if (!loader) return Promise.resolve()
  const p = loader()
    .then((mod) => {
      loaded.set(lang, mod.default)
      notify()
    })
    .finally(() => {
      inflight.delete(lang)
    })
  inflight.set(lang, p)
  return p
}

/** recompute 按单一优先级链重算 requestedLanguage 与 uiLanguage，并触发所需语种包加载。 */
function recompute(): Promise<void> {
  const candidates = [
    sources.hostCommand,
    sources.explicitParam,
    sources.hostDocLang,
    ...browserLanguages(),
    sources.channelDefault,
    "en",
  ].filter(Boolean)

  const nextRequested = normalizeLang(candidates[0]) || "en"
  const nextUI =
    matchWidgetLanguage(candidates, WIDGET_LANGUAGES) ??
    matchWidgetLanguage([sources.channelDefault], WIDGET_LANGUAGES) ??
    "en"

  const changed = nextRequested !== currentRequested || nextUI !== currentUI
  currentRequested = nextRequested
  currentUI = nextUI
  if (changed) notify()
  return ensureLanguage(nextUI)
}

/** initWidgetLanguage 在应用初始化时注入 URL/配置显式语言，并立即启动对应语种包加载。 */
export function initWidgetLanguage(opts: { urlLang?: string; configLang?: string }): Promise<void> {
  sources.explicitParam = normalizeLang(opts.urlLang) || normalizeLang(opts.configLang)
  return recompute()
}

/** applyHostInitLanguage 在 loader.js init 握手到达后应用宿主显式语言和宿主文档语言。 */
export function applyHostInitLanguage(opts: { lang?: string; hostLang?: string }): Promise<void> {
  if (opts.lang) sources.explicitParam = normalizeLang(opts.lang) || sources.explicitParam
  if (opts.hostLang) sources.hostDocLang = normalizeLang(opts.hostLang)
  return recompute()
}

/** applyChannelDefaultLanguage 在公开配置到达后设置渠道默认语言。 */
export function applyChannelDefaultLanguage(defaultLang?: string): Promise<void> {
  sources.channelDefault = normalizeLang(defaultLang)
  return recompute()
}

/** applyHostCommandLanguage 响应宿主页面运行期调用的 setLanguage 命令。 */
export function applyHostCommandLanguage(lang: string): Promise<void> {
  const norm = normalizeLang(lang)
  if (!norm) return Promise.resolve()
  sources.hostCommand = norm
  return recompute()
}

/** lookup 在当前语种与扩展目录中查找消息模板。 */
function lookup(lang: string, id: string): Message | undefined {
  const primary = loaded.get(lang)?.[id]
  if (primary !== undefined) return primary
  for (let i = lower.length - 1; i >= 0; i--) {
    const m = lower[i][lang]?.[id]
    if (m !== undefined) return m
  }
  return undefined
}

/** WidgetI18n 在标准 I18n 契约上补充访客真实语言意图与文档排版方向。 */
export interface WidgetI18n extends I18n<MessageId> {
  /** 访客的完整 BCP 47 语言意图（用于 Accept-Language、自定义文案解析与会话先验）。 */
  requestedLanguage(): string
  /** 当前界面语言的文档方向：ltr 或 rtl。 */
  direction(): "ltr" | "rtl"
}

/** i18n 是聊天窗口的全局多语言实例。 */
export const i18n: WidgetI18n = {
  language: () => currentUI,
  requestedLanguage: () => currentRequested,
  direction: () => directionOf(currentUI),
  setLanguage(language) {
    const norm = normalizeLang(language)
    sources.hostCommand = norm
    void recompute()
  },
  languages: () => [...WIDGET_LANGUAGES],
  t(id, data?: MessageData) {
    const msg = lookup(currentUI, id) ?? lookup("en", id)
    if (msg === undefined) return id
    const used = lookup(currentUI, id) !== undefined ? currentUI : "en"
    return formatMessage(msg, used, data)
  },
  has: (id) =>
    id in en ||
    [...loaded.values()].some((table) => table[id] !== undefined) ||
    lower.some((cat) => Object.values(cat).some((table) => table[id] !== undefined)),
  extend(catalog) {
    if (!lower.includes(catalog)) lower.push(catalog)
  },
  headers: () => ({ "Accept-Language": currentRequested }),
  subscribe(listener) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  },
}

// 浏览器环境启动时立即按 URL 参数与浏览器偏好完成初始语言解析与语种包加载。
if (typeof window !== "undefined") {
  const params = new URLSearchParams(window.location.search)
  void initWidgetLanguage({
    urlLang: params.get("lang") ?? undefined,
    configLang: window.agentsDesk?.lang,
  })
}

// 登记消息 ID：@go-tenon/ui 的 useT 只接受 en.json 中的合法 ID。
declare module "@go-tenon/ui" {
  interface I18nRegister {
    messageId: MessageId
  }
}
