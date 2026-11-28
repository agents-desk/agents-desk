// 报表工作台外壳：Pane + 52px PaneHeader + 吸顶紧凑 PaneToolbar + 独立滚动 PaneBody。

import type { ReportPeriodMeta } from "@agents-desk/client/tenant"
import { Alert, Button, Group, Menu, Stack, Text } from "@mantine/core"
import { IconAlertCircle, IconDownload } from "@tabler/icons-react"
import { useParams } from "@tanstack/react-router"
import { Pane, PaneBody, PaneHeader, PaneToolbar, type PeriodState, useCan, useT } from "@go-tenon/ui"
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
  /** 时间选择器右侧的自定义维度筛选控件。 */
  toolbarExtra?: ReactNode
  /** 后端返回的生效统计周期元信息，展示在顶栏右侧。 */
  meta?: ReportPeriodMeta
  /** 报表接口报错时在正文顶部提示。 */
  error?: unknown
  /** 切换筛选条件拉取新数据且保留旧数据展示时为 true，给正文叠一层轻过渡态。 */
  updating?: boolean
  /** 工具条右侧可选扩展区。 */
  toolbarRight?: ReactNode
  /** 可选导出配置：开启后在顶栏右侧提供 Excel/CSV 导出按钮。 */
  exportConfig?: {
    report: string
    params: object
  }
  children: ReactNode
}

/** 构造报表同步导出链接。 */
function buildReportExportUrl(slug: string, report: string, format: "xlsx" | "csv", params: object): string {
  const sp = new URLSearchParams({ report, format })
  for (const [k, v] of Object.entries(params)) {
    if (typeof v === "string" && v) sp.set(k, v)
  }
  return `/t/${encodeURIComponent(slug)}/api/reports/export?${sp.toString()}`
}

/** 统计模块各子页面共用的工作台外壳。 */
export function ReportShell({
  title,
  period,
  filters,
  toolbarExtra,
  meta,
  error,
  updating,
  toolbarRight,
  exportConfig,
  children,
}: ReportShellProps): ReactNode {
  const t = useT()
  const can = useCan()
  const { slug } = useParams({ from: "/$slug" })
  const showExport = Boolean(exportConfig && can("reports:read"))
  return (
    <Pane>
      <PaneHeader
        title={title}
        showNavToggle
        actions={
          (meta || showExport) && (
            <Group gap="sm" wrap="nowrap">
              {meta && (
                <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>
                  {t("reports.period_range", { from: meta.from, to: meta.to, tz: meta.time_zone })}
                </Text>
              )}
              {showExport && exportConfig && (
                <Menu position="bottom-end" withinPortal>
                  <Menu.Target>
                    <Button size="compact-xs" variant="light" leftSection={<IconDownload size={14} />}>
                      {t("reports.export")}
                    </Button>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item
                      component="a"
                      href={buildReportExportUrl(slug, exportConfig.report, "xlsx", exportConfig.params)}
                    >
                      {t("reports.export_xlsx")}
                    </Menu.Item>
                    <Menu.Item
                      component="a"
                      href={buildReportExportUrl(slug, exportConfig.report, "csv", exportConfig.params)}
                    >
                      {t("reports.export_csv")}
                    </Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              )}
            </Group>
          )
        }
      />
      <PaneToolbar>
        <ReportFilterBar period={period} filters={filters} extra={toolbarExtra} right={toolbarRight} />
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
