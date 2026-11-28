// 表格卡片容器：给普通 Mantine Table 提供与 DataTable 一致的外框、表头底色和横向滚动。

import { Table } from "@mantine/core"
import type { ReactNode } from "react"

/** TableCard 的选项。 */
export interface TableCardProps {
  /** 表格内容的最小宽度（像素），窄屏时卡片内自动横向滚动。 */
  minWidth: number
  className?: string
  children: ReactNode
}

/** 轻量表格卡片：统一外框、表头底色与横向滚动容器。 */
export function TableCard({ minWidth, className, children }: TableCardProps): ReactNode {
  return (
    <Table.ScrollContainer
      minWidth={minWidth}
      type="native"
      className={className ? `tn-table-card ${className}` : "tn-table-card"}
    >
      {children}
    </Table.ScrollContainer>
  )
}
