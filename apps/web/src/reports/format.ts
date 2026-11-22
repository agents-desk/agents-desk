// 报表通用的数值格式化、环比计算和图表色板工具。

import type { StatDelta } from "@go-tenon/ui"

/** 报表图表使用的主题预设色序列（严格使用 Tenon Mantine 主题预设色名）。 */
export const REPORT_CHART_COLORS = [
  "brand.6",
  "bright-blue.6",
  "green.6",
  "yellow.6",
  "deep-orange.6",
  "violet.6",
  "sky-blue.6",
  "gray.6",
] as const

/** 按界面语言格式化整数（千分位）。 */
export function formatInt(value: number | null | undefined, locale?: string): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—"
  return new Intl.NumberFormat(locale).format(Math.round(value))
}

/** 格式化百分比（保留 1 位小数）；空值返回 "—"。 */
export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—"
  return `${value.toFixed(1)}%`
}

/** 格式化 1–5 评分（保留 1 位小数）；空值返回 "—"。 */
export function formatScore(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—"
  return value.toFixed(1)
}

/** 格式化美元费用：小额保留 4 位小数避免显示为 $0.00，较大金额保留 2 位小数。 */
export function formatUSD(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—"
  if (value === 0) return "$0"
  if (value < 0.01) return `$${value.toFixed(4)}`
  if (value < 1) return `$${value.toFixed(3)}`
  return `$${value.toFixed(2)}`
}

/**
 * 计算当前值相对上一对比期的百分比环比；上期为 0 或空值时返回 undefined，避免误导性的无穷大涨幅。
 */
export function pctDelta(
  current: number | null | undefined,
  previous: number | null | undefined,
  invert = false,
): StatDelta | undefined {
  if (
    current === null ||
    current === undefined ||
    previous === null ||
    previous === undefined ||
    !Number.isFinite(current) ||
    !Number.isFinite(previous) ||
    previous <= 0
  ) {
    return undefined
  }
  const pct = Math.round(((current - previous) / previous) * 1000) / 10
  return {
    value: pct,
    label: pct > 0 ? `+${pct}%` : `${pct}%`,
    invert,
  }
}

/**
 * 计算两个绝对数值之差（例如 CSAT 百分点或平均分差值）；任一值为空时返回 undefined。
 */
export function diffDelta(
  current: number | null | undefined,
  previous: number | null | undefined,
  formatLabel: (signed: string) => string,
  invert = false,
): StatDelta | undefined {
  if (
    current === null ||
    current === undefined ||
    previous === null ||
    previous === undefined ||
    !Number.isFinite(current) ||
    !Number.isFinite(previous)
  ) {
    return undefined
  }
  const diff = Math.round((current - previous) * 10) / 10
  const signed = diff > 0 ? `+${diff}` : String(diff)
  return {
    value: diff,
    label: formatLabel(signed),
    invert,
  }
}

/** 返回客户星级对应的 Tenon 语义状态色。 */
export function scoreColor(score: number): string {
  if (score >= 4) return "green"
  if (score === 3) return "yellow"
  if (score === 2) return "deep-orange"
  return "red"
}

/** 返回 CSAT 百分比对应的 Tenon 语义状态色。 */
export function csatColor(csat: number | null | undefined): string {
  if (csat === null || csat === undefined) return "gray"
  if (csat >= 85) return "green"
  if (csat >= 70) return "yellow"
  return "deep-orange"
}
