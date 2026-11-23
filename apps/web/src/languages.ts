// 业务语言单一数据源：所有语言选择器（回复框翻译、会话与联系人详情、筛选、术语表）均由此派生。
// 后续新增支持的语种，只需在 commonLanguages 中追加对应 BCP 47 标签即可；筛选与下拉列表自动同步。
// 名字由浏览器的 Intl.DisplayNames 按界面语言动态生成，同时支持输入任意合法 BCP 47 标签。

/** commonLanguages 是当前核心支持的常用业务语言（BCP 47）：中文、英语、拉丁印地语、印地语。 */
export const commonLanguages = [
  "zh-CN",
  "en",
  "hi-Latn",
  "hi",
] as const

/**
 * filterLanguages 是按主语言去重后的筛选语言列表（例如 zh-CN -> zh，hi-Latn / hi -> hi），
 * 供联系人列表与保存筛选按主语言前缀同时命中所有书写变体。
 */
export const filterLanguages: readonly string[] = [
  ...new Set(commonLanguages.map((tag) => tag.split("-")[0])),
]

/** sameLanguage 判断两个 BCP 47 语言标签是否指向同一书写语言（合并 zh-CN/zh-Hans 与 zh-TW/zh-Hant）。 */
export function sameLanguage(a?: string, b?: string): boolean {
  if (!a || !b) return false
  const norm = (s: string) => {
    const k = s.trim().toLowerCase().replaceAll("_", "-")
    if (k === "zh-cn" || k === "zh-sg" || k === "zh-hans") return "zh-hans"
    if (k === "zh-tw" || k === "zh-hk" || k === "zh-mo" || k === "zh-hant") return "zh-hant"
    return k
  }
  return norm(a) === norm(b)
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

/** TAG 是语言标签的样子：两三个字母的主语言，后面是用 - 或 _ 连接的子标签，例如 hi-Latn、pt_BR。 */
const TAG = /^[A-Za-z]{2,3}([-_][A-Za-z0-9]{1,8})*$/

/**
 * validLanguage 返回 tag 是否是浏览器认识的 BCP 47 标签，并给出规范的写法（hi-latn → hi-Latn）；不认识时返回空字符串。
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
