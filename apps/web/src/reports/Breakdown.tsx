// 报表拆解表共用的实时积压状态条、行尾收件箱下钻按钮、行内占比细条与图表过渡配置。

import { Skeleton, Text, Tooltip } from "@mantine/core"
import { IconArrowUpRight } from "@tabler/icons-react"
import { Link } from "@tanstack/react-router"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import type { InboxSearch } from "../inbox/search"

/** LiveBacklogItem 描述报表顶部实时积压条中的单个指标项。 */
export interface LiveBacklogItem {
  key: string
  label: string
  value: ReactNode
  warn?: boolean
  tip?: string
  link?: {
    to: string
    params: Record<string, string>
    search?: Record<string, unknown>
  }
}

/** LiveBacklogStrip 展示报表顶部不受时间窗口影响的此刻实时状态条，支持权限门控与点击下钻。 */
export function LiveBacklogStrip({ loading, items }: { loading: boolean; items: LiveBacklogItem[] }): ReactNode {
  const t = useT()
  return (
    <div className="rp-live-strip">
      <span className="rp-live-badge">
        <span className="rp-live-dot" aria-hidden="true" />
        {t("reports.live_label")}
      </span>
      <div className="rp-live-items">
        {items.map((item) => {
          const inner = (
            <>
              <Text span size="xs" c="dimmed">
                {item.label}
              </Text>
              {loading ? (
                <Skeleton height={16} width={28} radius="xs" />
              ) : (
                <Text span className="rp-live-value" c={item.warn ? "deep-orange" : undefined}>
                  {item.value}
                </Text>
              )}
              {item.link && <IconArrowUpRight size={14} stroke={1.6} className="rp-live-arrow" />}
            </>
          )
          if (!item.link) {
            return (
              <span key={item.key} className="rp-live-item">
                {inner}
              </span>
            )
          }
          const linked = (
            <Link
              key={item.key}
              to={item.link.to as never}
              params={item.link.params as never}
              search={item.link.search as never}
              className="rp-live-item"
              data-clickable=""
            >
              {inner}
            </Link>
          )
          return item.tip ? (
            <Tooltip key={item.key} label={item.tip} openDelay={250}>
              {linked}
            </Tooltip>
          ) : (
            linked
          )
        })}
      </div>
    </div>
  )
}

/** 面积图平滑过渡配置：覆盖 Mantine Charts 默认关闭动画的设置。 */
export const REPORT_AREA_PROPS = {
  isAnimationActive: true,
  animationDuration: 260,
  animationEasing: "ease-out" as const,
}

/** 柱状图平滑过渡与顶角圆角配置。 */
export const REPORT_BAR_PROPS = {
  radius: [4, 4, 0, 0] as [number, number, number, number],
  isAnimationActive: true,
  animationDuration: 260,
  animationEasing: "ease-out" as const,
}

/** 行尾悬停露出的轻量“在收件箱查看”链接按钮，点击事件在按钮处停止冒泡，整行的报表聚焦保持原样。 */
export function InboxDrillButton({
  slug,
  search,
  label,
}: {
  slug: string
  search: InboxSearch
  label: string
}): ReactNode {
  return (
    <Tooltip label={label} openDelay={250}>
      <Link
        to="/$slug/inbox"
        params={{ slug }}
        search={search}
        className="rp-row-action rp-row-action-btn"
        aria-label={label}
        onClick={(e) => e.stopPropagation()}
      >
        <IconArrowUpRight size={14} stroke={1.6} />
      </Link>
    </Tooltip>
  )
}

/** 表格单元格下方用于辅助视觉比较的极细占比条。 */
export function InlineShareBar({ ratio }: { ratio: number }): ReactNode {
  const clamped = Math.max(0, Math.min(100, ratio))
  return (
    <div className="rp-inline-bar-track" aria-hidden="true">
      <div className="rp-inline-bar-fill" style={{ width: `${clamped}%` }} />
    </div>
  )
}
