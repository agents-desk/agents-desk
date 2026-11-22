// 语言（BCP 47 标签）的显示：名字由浏览器的 Intl.DisplayNames 按界面语言给出，例如 hi-Latn 是“印地语（拉丁字母）”，
// 不写进翻译文件。回复框选择目标语言、术语表选择译法的语言时列出常用的语言，也可以输入任意标签。

/** commonLanguages 是常用的语言：客户常用的在前，成员常用的在后。 */
export const commonLanguages = [
  "hi-Latn",
  "hi",
  "en",
  "bn",
  "ta",
  "te",
  "mr",
  "ur",
  "ar",
  "id",
  "es",
  "pt",
  "fr",
  "ru",
  "zh-CN",
  "zh-TW",
  "ja",
  "ko",
] as const

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
