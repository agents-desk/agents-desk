// 指标卡片（StatCard）、同比/环比趋势徽章（StatDeltaBadge）与等分指标带（MetricStrip）。
// 用于仪表盘和报表页的 KPI 概览：显示标题、可选口径说明（Tooltip）、等宽数字主值、辅助说明以及环比变化。

import { ActionIcon, Badge, Box, Group, Paper, Skeleton, Stack, Text, Tooltip } from "@mantine/core"
import { IconArrowDownRight, IconArrowUpRight, IconInfoCircle, IconMinus } from "@tabler/icons-react"
import type { CSSProperties, ReactNode } from "react"

/** 指标相对上一周期的变化。 */
export interface StatDelta {
  /** 变化量（正数表示上升，负数表示下降，0 表示持平；null/undefined 时不渲染）。 */
  value: number | null | undefined
  /** 展示的文字，例如 "+12.4%"、"-1 分 20 秒"；省略时按 value 自动格式化。 */
  label?: string
  /** 为 true 时表示指标越低越好（如响应时长、失败数）：下降显示 green，上升显示 deep-orange。 */
  invert?: boolean
}

/** StatDeltaBadge 的选项。 */
export interface StatDeltaBadgeProps {
  delta?: StatDelta
  /** 徽章尺寸，默认 "sm"。 */
  size?: "xs" | "sm"
}

/** 按变化方向与 invert 语义渲染环比徽章；delta 为空或非有限数时返回 null。 */
export function StatDeltaBadge({ delta, size = "sm" }: StatDeltaBadgeProps): ReactNode {
  if (!delta || delta.value === null || delta.value === undefined || !Number.isFinite(delta.value)) return null
  const v = delta.value
  const label = delta.label ?? (v > 0 ? `+${v}` : String(v))
  if (v === 0) {
    return (
      <Badge
        size={size}
        variant="light"
        color="gray"
        leftSection={<IconMinus size={12} stroke={2} />}
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {label}
      </Badge>
    )
  }
  const positive = v > 0
  const good = delta.invert ? !positive : positive
  const color = good ? "green" : "deep-orange"
  const arrow = positive ? <IconArrowUpRight size={12} stroke={2} /> : <IconArrowDownRight size={12} stroke={2} />
  return (
    <Badge size={size} variant="light" color={color} leftSection={arrow} style={{ fontVariantNumeric: "tabular-nums" }}>
      {label}
    </Badge>
  )
}

/** StatCard 的选项。 */
export interface StatCardProps {
  /** 指标标题。 */
  label: ReactNode
  /** 指标口径说明，悬停在标题旁的信息图标上显示。 */
  description?: ReactNode
  /** 指标主值；加载中时显示骨架屏。 */
  value: ReactNode
  /** 主值下方的补充信息，例如“均值 4 分 · P90 12 分”或样本数。 */
  secondary?: ReactNode
  /** 相对上一周期的环比变化。 */
  delta?: StatDelta
  /** 右上角的可选图标。 */
  icon?: ReactNode
  /** 加载状态：主值与辅助行显示骨架屏。 */
  loading?: boolean
}

/** 指标卡片：用于展示单个关键统计指标及其环比趋势。 */
export function StatCard({ label, description, value, secondary, delta, icon, loading }: StatCardProps): ReactNode {
  return (
    <Paper withBorder p="md" className="tn-stat-card">
      <Stack gap={6}>
        <Group justify="space-between" align="center" gap="xs" wrap="nowrap">
          <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
            <Text size="xs" fw={500} c="dimmed" truncate>
              {label}
            </Text>
            {description && (
              <Tooltip label={description} multiline maw={260}>
                <ActionIcon
                  variant="transparent"
                  color="gray"
                  size="xs"
                  aria-label={typeof label === "string" ? label : undefined}
                >
                  <IconInfoCircle size={14} stroke={1.6} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
          {icon && <Box className="tn-stat-card-icon">{icon}</Box>}
        </Group>

        {loading ? (
          <Stack gap={6} pt={2}>
            <Skeleton height={28} width="55%" radius="sm" />
            <Skeleton height={14} width="75%" radius="sm" />
          </Stack>
        ) : (
          <>
            <Group align="baseline" gap="xs" wrap="wrap">
              <Text className="tn-stat-card-value">{value}</Text>
              <StatDeltaBadge delta={delta} />
            </Group>
            {secondary && (
              <Text size="xs" c="dimmed" className="tn-stat-card-secondary">
                {secondary}
              </Text>
            )}
          </>
        )}
      </Stack>
    </Paper>
  )
}

/** MetricStrip 中的单个指标项。 */
export interface MetricItem {
  key: string
  label: ReactNode
  /** 指标口径说明，悬停在标题旁的信息图标上显示。 */
  description?: ReactNode
  value: ReactNode
  delta?: StatDelta
  secondary?: ReactNode
  /** 是否处于激活态（例如驱动下方趋势图切换）。 */
  active?: boolean
  /** 传入后该指标格变为可点击切换按钮。 */
  onClick?: () => void
}

/** MetricStrip 的选项。 */
export interface MetricStripProps {
  items: readonly MetricItem[]
  loading?: boolean
}

/** 统一等分指标带：以细竖线分隔展示核心指标，支持点击指标格联动趋势图。 */
export function MetricStrip({ items, loading }: MetricStripProps): ReactNode {
  const vars = { "--tn-metric-cols": String(items.length || 1) } as CSSProperties
  return (
    <div className="tn-metric-strip" style={vars}>
      {items.map((item) => {
        const content = (
          <>
            <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
              <Text size="xs" fw={500} c="dimmed" truncate>
                {item.label}
              </Text>
              {item.description && (
                <Tooltip label={item.description} multiline maw={260}>
                  <ActionIcon
                    component="span"
                    tabIndex={0}
                    variant="transparent"
                    color="gray"
                    size="xs"
                    aria-label={typeof item.label === "string" ? item.label : undefined}
                    onClick={(e) => e.stopPropagation()}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") e.stopPropagation()
                    }}
                  >
                    <IconInfoCircle size={14} stroke={1.6} />
                  </ActionIcon>
                </Tooltip>
              )}
            </Group>

            {loading ? (
              <Stack gap={6} pt={2}>
                <Skeleton height={26} width="54%" radius="sm" />
                <Skeleton height={13} width="72%" radius="sm" />
              </Stack>
            ) : (
              <>
                <Group align="baseline" gap="xs" wrap="wrap">
                  <Text className="tn-metric-value">{item.value}</Text>
                  <StatDeltaBadge delta={item.delta} size="xs" />
                </Group>
                {item.secondary && (
                  <Text size="xs" c="dimmed" className="tn-metric-secondary" truncate>
                    {item.secondary}
                  </Text>
                )}
              </>
            )}
          </>
        )

        if (item.onClick) {
          return (
            <button
              key={item.key}
              type="button"
              className="tn-metric-cell"
              data-interactive=""
              data-active={item.active || undefined}
              aria-pressed={Boolean(item.active)}
              onClick={item.onClick}
            >
              {content}
            </button>
          )
        }

        return (
          <div key={item.key} className="tn-metric-cell">
            {content}
          </div>
        )
      })}
    </div>
  )
}
