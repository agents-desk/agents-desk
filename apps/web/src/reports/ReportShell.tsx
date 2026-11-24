// 报表工作台统一外壳：与收件箱、联系人一致的 Pane + 52px PaneHeader + 吸顶紧凑 PaneToolbar + 独立滚动 PaneBody。

import type { ReportPeriodMeta } from "@agents-desk/client/tenant"
import { Alert, Stack, Text } from "@mantine/core"
import { IconAlertCircle } from "@tabler/icons-react"
import { Pane, PaneBody, PaneHeader, PaneToolbar, type PeriodState, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { ReportFilterBar } from "./FilterBar"
import type { ReportFiltersState } from "./search"

export interface ReportShellProps {
  /** 顶栏标题（与左侧二级导航对应）。 */
  title: ReactNode
  /** 时间窗口状态。 */
  period: PeriodState
  /** 支持团队与渠道筛选的报表传入 filters。 */
  filters?: ReportFiltersState
  /** 后端返回的生效统计周期元信息，展示在顶栏右侧。 */
  meta?: ReportPeriodMeta
  /** 报表接口报错时在正文顶部提示。 */
  error?: unknown
  /** 切换筛选条件拉取新数据且保留旧数据展示时为 true，给正文叠一层轻过渡态。 */
  updating?: boolean
  /** 工具条右侧可选扩展区。 */
  toolbarRight?: ReactNode
  children: ReactNode
}

/** 统计模块各子页面共用的工作台外壳。 */
export function ReportShell({
  title,
  period,
  filters,
  meta,
  error,
  updating,
  toolbarRight,
  children,
}: ReportShellProps): ReactNode {
  const t = useT()
  return (
    <Pane>
      <PaneHeader
        title={title}
        showNavToggle
        actions={
          meta && (
            <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>
              {t("reports.period_range", { from: meta.from, to: meta.to, tz: meta.time_zone })}
            </Text>
          )
        }
      />
      <PaneToolbar>
        <ReportFilterBar period={period} filters={filters} right={toolbarRight} />
      </PaneToolbar>
      <PaneBody loading={Boolean(updating)}>
        <div className="rp-body">
          <Stack gap="lg">
            {Boolean(error) && (
              <Alert color="red" variant="light" icon={<IconAlertCircle size={16} />}>
                {t("reports.load_failed")}
              </Alert>
            )}
            {children}
          </Stack>
        </div>
      </PaneBody>
    </Pane>
  )
}
