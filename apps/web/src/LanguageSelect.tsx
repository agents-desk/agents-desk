import { Select, type SelectProps } from "@mantine/core"
import { useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useMemo, useState } from "react"
import { allLanguages, commonLanguages, languageName, languageOptions, languageSearchKeys, useWorkspaceLanguages, validLanguage } from "./languages"

/** LanguageSelectProps 是选择语言的设置。 */
interface LanguageSelectProps extends Omit<SelectProps, "data" | "searchValue" | "onSearchChange"> {
  first?: string[] // 排在工作区活跃语言与默认核心语言前面的语言，例如对方的语言
  before?: { value: string; label: string }[] // 排在最前面的特殊选项，例如“不翻译”“自动识别”
  labels?: Record<string, string> // 替换某些语言的显示文字，例如在对方的语言后面注明“对方的语言”
  languages?: readonly string[] // 显式指定候选语言列表；省略时默认展开 commonLanguages，搜索时扩展到 allLanguages
  includeWorkspace?: boolean // 是否自动把当前工作区活跃语言置顶，默认 true
}

/**
 * LanguageSelect 是选择语言的下拉框：
 * 默认展开只显示特殊选项、first 中的语言、当前工作区活跃语言和精简核心语言（commonLanguages）；
 * 输入搜索词时自动扩展到全球语言目录（allLanguages），支持按界面名、英文名、自称或任意合法 BCP 47 标签检索。
 */
export function LanguageSelect({
  first = [],
  before = [],
  labels = {},
  languages,
  includeWorkspace = true,
  ...props
}: LanguageSelectProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const workspaceLanguages = useWorkspaceLanguages()
  const [search, setSearch] = useState("")
  // 当前的值可能是特殊选项（“不翻译”“自动识别”），它不是语言，不再列一次：Mantine 不接受重复的选项
  const special = new Set(before.map((o) => o.value))
  const current = typeof props.value === "string" && !special.has(props.value) ? props.value : ""
  const priority = [current, ...first.filter((x) => !special.has(x)), ...(includeWorkspace ? workspaceLanguages : [])]
  const defaultOptions = languageOptions(ui, priority, languages ?? commonLanguages)
    .filter((o) => !special.has(o.value))
    .map((o) => ({ ...o, label: labels[o.value] ?? o.label }))
  const selectedLabel = [...before, ...defaultOptions].find((o) => o.value === props.value)?.label ?? ""
  const isSearching = search.trim() !== "" && search !== selectedLabel
  const options =
    isSearching && !languages
      ? languageOptions(ui, priority, allLanguages)
          .filter((o) => !special.has(o.value))
          .map((o) => ({ ...o, label: labels[o.value] ?? o.label }))
      : defaultOptions
  const typed = validLanguage(search)
  if (typed && !special.has(typed) && !options.some((o) => o.value === typed)) {
    options.unshift({ value: typed, label: `${languageName(typed, ui)} (${typed})` })
  }
  const searchIndex = useMemo(() => {
    const idx = new Map<string, string>()
    for (const o of [...before, ...options]) {
      idx.set(o.value, special.has(o.value) ? o.label.toLowerCase() : `${o.label.toLowerCase()} ${languageSearchKeys(o.value, ui)}`)
    }
    return idx
  }, [before, options, ui])
  return (
    <Select
      searchable
      searchValue={search}
      onSearchChange={setSearch}
      filter={({ options: opts, search: q }) => {
        const needle = q.trim().toLowerCase()
        if (!needle) return opts
        return opts.filter((item) => {
          if (!("value" in item)) return false
          const key = searchIndex.get(item.value) ?? `${item.label} ${item.value}`.toLowerCase()
          return key.includes(needle)
        })
      }}
      nothingFoundMessage={t("inbox.language_not_found")}
      comboboxProps={{ withinPortal: true }}
      {...props}
      data={[...before, ...options]}
    />
  )
}
