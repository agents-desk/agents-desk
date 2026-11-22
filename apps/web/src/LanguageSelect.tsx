import { Select, type SelectProps } from "@mantine/core"
import { useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { languageName, languageOptions, validLanguage } from "./languages"

/** LanguageSelectProps 是选择语言的设置。 */
interface LanguageSelectProps extends Omit<SelectProps, "data" | "searchValue" | "onSearchChange"> {
  first?: string[] // 排在常用语言前面的语言，例如对方的语言
  before?: { value: string; label: string }[] // 排在最前面的特殊选项，例如“不翻译”“自动识别”
  labels?: Record<string, string> // 替换某些语言的显示文字，例如在对方的语言后面注明“对方的语言”
  languages?: readonly string[] // 候选的常用语言列表；不传时使用 commonLanguages
}

/**
 * LanguageSelect 是选择语言的下拉框：特殊选项、first 中的语言、常用语言，名字按界面语言显示；可以搜索，
 * 搜索的文字是一个语言标签（例如 hi-Latn）时也作为选项。回复框、详情中对方的语言、术语表的译法都用它。
 */
export function LanguageSelect({ first = [], before = [], labels = {}, languages, ...props }: LanguageSelectProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [search, setSearch] = useState("")
  // 当前的值可能是特殊选项（“不翻译”“自动识别”），它不是语言，不再列一次：Mantine 不接受重复的选项
  const special = new Set(before.map((o) => o.value))
  const current = typeof props.value === "string" && !special.has(props.value) ? props.value : ""
  const options = languageOptions(ui, [current, ...first.filter((x) => !special.has(x))], languages)
    .filter((o) => !special.has(o.value))
    .map((o) => ({ ...o, label: labels[o.value] ?? o.label }))
  const typed = validLanguage(search)
  if (typed && !special.has(typed) && !options.some((o) => o.value === typed)) options.unshift({ value: typed, label: `${languageName(typed, ui)} (${typed})` })
  return (
    <Select
      searchable
      searchValue={search}
      onSearchChange={setSearch}
      nothingFoundMessage={t("inbox.language_not_found")}
      comboboxProps={{ withinPortal: true }}
      {...props}
      data={[...before, ...options]}
    />
  )
}
