// 报表拆解表共用的行尾收件箱下钻按钮、行内占比细条与图表过渡配置。

import { Tooltip } from "@mantine/core"
import { IconArrowUpRight } from "@tabler/icons-react"
import { Link } from "@tanstack/react-router"
import type { ReactNode } from "react"
import type { InboxSearch } from "../inbox/search"

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
    <Tooltip label={label} openDelay={250} withArrow>
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
