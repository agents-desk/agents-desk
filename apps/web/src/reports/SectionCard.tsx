// 报表内容分区容器：拆解区块（SectionCard）与明细表分区（DetailSection），统一加载态与空态。

import { Center, Skeleton, Text } from "@mantine/core"
import { EmptyState, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

export interface SectionCardProps {
  title: ReactNode
  subtitle?: ReactNode
  action?: ReactNode
  loading?: boolean
  empty?: boolean
  emptyText?: ReactNode
  minHeight?: number
  /** 为 true 时去掉内容区内边距，供贴边拆解表格使用。 */
  flush?: boolean
  children: ReactNode
}

/** ReportEmpty 是报表图表和分区的空状态：在 minHeight 高的区域中居中显示 EmptyState，默认文字是“所选时间范围内暂无数据”。 */
export function ReportEmpty({ minHeight = 240, children }: { minHeight?: number; children?: ReactNode }): ReactNode {
  const t = useT()
  return (
    <Center mih={minHeight}>
      <EmptyState title={children ?? t("reports.empty")} />
    </Center>
  )
}

/** 报表拆解图表或表格分区。 */
export function SectionCard({
  title,
  subtitle,
  action,
  loading,
  empty,
  emptyText,
  minHeight = 160,
  flush = false,
  children,
}: SectionCardProps): ReactNode {
  return (
    <section className="rp-section">
      <div className="rp-section-header">
        <div style={{ minWidth: 0 }}>
          <Text size="sm" fw={600} lh={1.3} truncate>
            {title}
          </Text>
          {subtitle && (
            <Text size="xs" c="dimmed" mt={2}>
              {subtitle}
            </Text>
          )}
        </div>
        {action}
      </div>

      <div className="rp-section-content" data-flush={flush && !loading && !empty ? "" : undefined}>
        {loading ? (
          <Skeleton height={minHeight} radius="sm" />
        ) : empty ? (
          <ReportEmpty minHeight={minHeight}>{emptyText}</ReportEmpty>
        ) : (
          children
        )}
      </div>
    </section>
  )
}

export interface DetailSectionProps {
  title: ReactNode
  subtitle?: ReactNode
  action?: ReactNode
  children: ReactNode
}

/** 明细表格分区：展示标题栏与操作区，直接承载 DataTable。 */
export function DetailSection({ title, subtitle, action, children }: DetailSectionProps): ReactNode {
  return (
    <section className="rp-detail-section">
      <div className="rp-detail-header">
        <div style={{ minWidth: 0 }}>
          <Text size="sm" fw={600} lh={1.3}>
            {title}
          </Text>
          {subtitle && (
            <Text size="xs" c="dimmed" mt={2}>
              {subtitle}
            </Text>
          )}
        </div>
        {action}
      </div>
      {children}
    </section>
  )
}
