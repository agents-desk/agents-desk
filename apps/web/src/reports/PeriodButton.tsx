// 紧凑时间窗口胶囊（PeriodButton）与趋势图内分桶粒度切换（GranularityControl）。
// 将快捷预设与自定义日历合并到同一个 Popover 中，给工具条减负；分桶粒度贴近趋势图右上角放置。

import { Button, Popover, SegmentedControl } from "@mantine/core"
import { DatePicker } from "@mantine/dates"
import { IconCalendar, IconCheck, IconChevronDown } from "@tabler/icons-react"
import { type PeriodGranularity, type PeriodPreset, type PeriodState, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"

const PRESETS: readonly Exclude<PeriodPreset, "custom">[] = ["today", "7d", "30d", "this_month", "last_month"]

/** 缩短自定义区间展示：同年省略年份，否则完整显示 YYYY-MM-DD。 */
function formatShortRange(from: string, to: string): string {
  if (from === to) return from
  if (from.slice(0, 4) === to.slice(0, 4)) {
    return `${from.slice(5)} – ${to.slice(5)}`
  }
  return `${from} – ${to}`
}

/** 合并预设与自定义日期区间的紧凑时间胶囊按钮。 */
export function PeriodButton({ period }: { period: PeriodState }): ReactNode {
  const t = useT()
  const [opened, setOpened] = useState(false)
  const [draft, setDraft] = useState<[string | null, string | null] | null>(null)

  const presetLabel = (p: Exclude<PeriodPreset, "custom">): string => {
    switch (p) {
      case "today":
        return t("reports.period_today")
      case "7d":
        return t("reports.period_7d")
      case "30d":
        return t("reports.period_30d")
      case "this_month":
        return t("reports.period_this_month")
      case "last_month":
        return t("reports.period_last_month")
    }
  }

  const buttonLabel =
    period.preset === "custom" ? formatShortRange(period.from, period.to) : presetLabel(period.preset)
  const rangeValue: [string | null, string | null] = draft ?? [period.from, period.to]

  return (
    <Popover
      opened={opened}
      onChange={(next) => {
        setOpened(next)
        if (!next) setDraft(null)
      }}
      position="bottom-start"
      shadow="md"
      withinPortal
    >
      <Popover.Target>
        <Button
          size="sm"
          variant="default"
          leftSection={<IconCalendar size={14} stroke={1.6} />}
          rightSection={<IconChevronDown size={13} stroke={1.8} />}
          onClick={() => setOpened((v) => !v)}
          style={{ fontWeight: 500, fontVariantNumeric: "tabular-nums" }}
        >
          {buttonLabel}
        </Button>
      </Popover.Target>
      <Popover.Dropdown p={0}>
        <div className="rp-period-popover">
          <div className="rp-period-presets">
            {PRESETS.map((p) => {
              const active = period.preset === p
              return (
                <button
                  key={p}
                  type="button"
                  className="rp-period-preset-btn"
                  data-active={active || undefined}
                  onClick={() => {
                    setDraft(null)
                    period.setPreset(p)
                    setOpened(false)
                  }}
                >
                  <span>{presetLabel(p)}</span>
                  {active && <IconCheck size={13} stroke={2} />}
                </button>
              )
            })}
          </div>
          <div className="rp-period-calendar">
            <DatePicker
              type="range"
              size="xs"
              allowSingleDateInRange
              value={rangeValue}
              onChange={(next) => {
                const [s, e] = next
                if (s && e) {
                  setDraft(null)
                  period.setRange(s, e)
                  setOpened(false)
                } else {
                  setDraft([s, e])
                }
              }}
            />
          </div>
        </div>
      </Popover.Dropdown>
    </Popover>
  )
}

/** 趋势图标题栏右侧的分桶粒度切换（自动 / 按天 / 按周 / 按月）。 */
export function GranularityControl({ period }: { period: PeriodState }): ReactNode {
  const t = useT()
  return (
    <SegmentedControl
      size="xs"
      value={period.granularity ?? "auto"}
      onChange={(v) => period.setGranularity(v === "auto" ? undefined : (v as PeriodGranularity))}
      data={[
        { value: "auto", label: t("reports.gran_auto") },
        { value: "day", label: t("reports.gran_day") },
        { value: "week", label: t("reports.gran_week") },
        { value: "month", label: t("reports.gran_month") },
      ]}
    />
  )
}
