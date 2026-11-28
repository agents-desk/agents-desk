// IANA 时区选择器：可搜索全部规范化时区，并显示各时区当前 UTC 偏移或当地时间。

import { Select, type SelectProps } from "@mantine/core"
import { type ReactNode, useMemo } from "react"
import { useText } from "../i18n/i18n"
import { formatTimeZoneOffset, listTimeZones, normalizeTimeZone } from "../theme/timezone"

/** TimeZoneSelect 的选项。 */
export interface TimeZoneSelectProps extends Omit<SelectProps, "data" | "value" | "onChange"> {
  /** 当前选中的 IANA 时区名。 */
  value?: string | null
  /** 变更回调（始终传入规范化后的 IANA 时区名）。 */
  onChange: (value: string | null) => void
  /** 可选额外保留的时区列表（防止服务端保存了旧时区无法回显）。 */
  extraZones?: readonly string[]
}

/** 可搜索的 IANA 时区下拉框，自动规范化浏览器旧名。 */
export function TimeZoneSelect({
  value,
  onChange,
  extraZones,
  searchable = true,
  placeholder,
  ...rest
}: TimeZoneSelectProps): ReactNode {
  const t = useText()
  const options = useMemo(() => {
    const set = new Set(listTimeZones())
    if (value) set.add(normalizeTimeZone(value))
    if (extraZones) {
      for (const z of extraZones) {
        if (z) set.add(normalizeTimeZone(z))
      }
    }
    return [...set].sort().map((zone) => ({ value: zone, label: `${zone} (${formatTimeZoneOffset(zone)})` }))
  }, [extraZones, value])

  return (
    <Select
      {...rest}
      searchable={searchable}
      nothingFoundMessage={t("tenon.timezone.search")}
      placeholder={placeholder ?? t("tenon.timezone.placeholder")}
      data={options}
      value={value ? normalizeTimeZone(value) : null}
      onChange={(next) => onChange(next ? normalizeTimeZone(next) : null)}
    />
  )
}
