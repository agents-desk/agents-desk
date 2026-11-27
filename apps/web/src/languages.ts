// 全球业务语言目录与 BCP 47 比较、规范化工具：
// 名字由浏览器原生 Intl.DisplayNames 按界面语言动态生成，支持搜索界面语言名、英语名、自称与任意合法 BCP 47 标签。
import { useQuery } from "@tanstack/react-query"
import { kit } from "./api"

/** commonLanguages 是语言选择器默认展开的精简核心语言列表（BCP 47）；下拉框会自动将工作区实际活跃语言置顶。 */
export const commonLanguages = [
  "zh-CN",
  "en",
  "hi-Latn",
  "hi",
  "pt-BR",
  "es",
] as const

/** allLanguages 是语言选择器在用户输入搜索词时参与匹配的全球主要业务语言目录（BCP 47）。 */
export const allLanguages = [
  ...commonLanguages,
  "zh-TW",
  "pt-PT",
  "es-419",
  "id",
  "vi",
  "th",
  "ms",
  "ar",
  "ar-Latn",
  "fr",
  "de",
  "ja",
  "ko",
  "ru",
  "tr",
  "it",
  "nl",
  "pl",
  "uk",
  "bn",
  "ta",
  "te",
  "ur",
  "tl",
] as const

/**
 * sameLanguage 判断两个 BCP 47 标签是否指向同一种语言与书写/地区变体（规则与后端 internal/lang.Same 相同）：
 * - 先比较主语言（language）与书写方式（经 maximize 推断默认脚本，如 zh-CN == zh-Hans，hi != hi-Latn）；
 * - 双方都显式指定了不同地区且主语言不是中文时（如 pt-BR vs pt-PT、es-MX vs es-ES）视为不同变体；
 * - 一方未显式指定地区时（如 pt vs pt-BR、en vs en-US）视为兼容相同。
 */
export function sameLanguage(a?: string, b?: string): boolean {
  if (!a || !b) return false
  const rawA = a.trim().replaceAll("_", "-")
  const rawB = b.trim().replaceAll("_", "-")
  if (!rawA || !rawB) return false
  try {
    const la = new Intl.Locale(rawA)
    const lb = new Intl.Locale(rawB)
    const maxA = la.maximize()
    const maxB = lb.maximize()
    if (maxA.language !== maxB.language) return false
    if (maxA.script !== maxB.script) return false
    if (maxA.language !== "zh" && la.region && lb.region && la.region !== lb.region) {
      return false
    }
    return true
  } catch {
    return rawA.toLowerCase() === rawB.toLowerCase()
  }
}

/** displayNames 缓存每种界面语言的 Intl.DisplayNames。 */
const displayNames = new Map<string, Intl.DisplayNames>()

/** languageName 返回语言 tag 在界面语言 ui 中的名字；浏览器不认识这个标签时返回标签本身。 */
export function languageName(tag: string, ui: string): string {
  try {
    let names = displayNames.get(ui)
    if (!names) {
      names = new Intl.DisplayNames([ui], { type: "language" })
      displayNames.set(ui, names)
    }
    return names.of(tag) ?? tag
  } catch {
    return tag
  }
}

/** languageSearchKeys 返回语言 tag 用于本地模糊搜索的关键词集合（界面名、英文名、原生自称与标签代码）。 */
export function languageSearchKeys(tag: string, ui: string): string {
  const parts = [tag, languageName(tag, ui), languageName(tag, "en"), languageName(tag, tag)]
  return parts.join(" ").toLowerCase()
}

/** TAG 是语言标签的样子：两三个字母的主语言，后面是用 - 或 _ 连接的子标签，例如 hi-Latn、pt_BR。 */
const TAG = /^[A-Za-z]{2,3}([-_][A-Za-z0-9]{1,8})*$/

/**
 * validLanguage 返回 tag 是否是浏览器认识的 BCP 47 标签，并给出规范的写法（hi-latn → hi-Latn，pt_br → pt-BR）；不认识时返回空字符串。
 * 只接受两三个字母开头的标签，搜索框中的语言名（例如 English）不会被当成标签。
 */
export function validLanguage(tag: string): string {
  if (!TAG.test(tag.trim())) return ""
  try {
    return Intl.getCanonicalLocales(tag.trim().replaceAll("_", "-"))[0] ?? ""
  } catch {
    return ""
  }
}

/** languageOptions 返回选择语言的选项：先是 first 中的（去重），再是 base 中的常用语言，名字按界面语言 ui。 */
export function languageOptions(
  ui: string,
  first: string[] = [],
  base: readonly string[] = commonLanguages,
): { value: string; label: string }[] {
  const seen = new Set<string>()
  const out: { value: string; label: string }[] = []
  for (const tag of [...first, ...base]) {
    if (!tag || seen.has(tag)) continue
    seen.add(tag)
    out.push({ value: tag, label: languageName(tag, ui) })
  }
  return out
}

/** useWorkspaceLanguages 返回当前工作区中实际出现过的活跃语言标签（成员与联系人语言去重），供选择器和筛选器动态置顶。 */
export function useWorkspaceLanguages(): string[] {
  const q = useQuery({ ...kit.queries.translation.settings(), staleTime: 60_000 })
  return q.data?.workspace_languages ?? []
}
