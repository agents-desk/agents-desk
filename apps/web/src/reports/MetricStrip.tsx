// 统一指标带（MetricStrip）：以细竖线等分展示核心指标，支持点击指标格联动下方趋势图。

import { ActionIcon, Badge, Group, Skeleton, Stack, Text, Tooltip } from "@mantine/core"
import { IconArrowDownRight, IconArrowUpRight, IconInfoCircle, IconMinus } from "@tabler/icons-react"
import type { StatDelta } from "@go-tenon/ui"
import type { CSSProperties, ReactNode } from "react"

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

export interface MetricStripProps {
  items: MetricItem[]
  loading?: boolean
}

/** 按变化方向与 invert 语义渲染紧凑环比徽章。 */
function DeltaBadge({ delta }: { delta?: StatDelta }): ReactNode {
  if (!delta || delta.value === null || delta.value === undefined || !Number.isFinite(delta.value)) return null
  const v = delta.value
  const label = delta.label ?? (v > 0 ? `+${v}` : String(v))
  if (v === 0) {
    return (
      <Badge size="xs" variant="light" color="gray" leftSection={<IconMinus size={12} stroke={2} />}>
        {label}
      </Badge>
    )
  }
  const positive = v > 0
  const good = delta.invert ? !positive : positive
  const color = good ? "green" : "deep-orange"
  const arrow = positive ? <IconArrowUpRight size={12} stroke={2} /> : <IconArrowDownRight size={12} stroke={2} />
  return (
    <Badge size="xs" variant="light" color={color} leftSection={arrow}>
      {label}
    </Badge>
  )
}

/** 统一等分指标带。 */
export function MetricStrip({ items, loading }: MetricStripProps): ReactNode {
  const vars = { "--rp-cols": String(items.length) } as CSSProperties
  return (
    <div className="rp-metric-strip" style={vars}>
      {items.map((item) => {
        const content = (
          <>
            <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
              <Text size="xs" fw={500} c="dimmed" truncate>
                {item.label}
              </Text>
              {item.description && (
                <Tooltip label={item.description} multiline maw={260} withArrow>
                  <ActionIcon
                    component="span"
                    variant="transparent"
                    color="gray"
                    size="xs"
                    aria-label={String(item.label)}
                    onClick={(e) => e.stopPropagation()}
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
                  <Text className="rp-metric-value">{item.value}</Text>
                  <DeltaBadge delta={item.delta} />
                </Group>
                {item.secondary && (
                  <Text size="xs" c="dimmed" className="rp-metric-secondary" truncate>
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
              className="rp-metric-cell"
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
          <div key={item.key} className="rp-metric-cell">
            {content}
          </div>
        )
      })}
    </div>
  )
}
