import { type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { useT } from "@go-tenon/ui"

/** NARROW_QUERY 是工作台端统一的窄屏断点（小于 900px）：二级导航收进抽屉，列表与详情轮流占满。 */
export const NARROW_QUERY = "(max-width: 899px)"

/**
 * useResizableWidth 管理可拖拽调整并保存在本地存储中的栏宽（像素）。
 * side 表示拖拽手柄位于面板的左侧还是右侧，双击手柄可恢复默认宽度。
 */
export function useResizableWidth(
  storageKey: string,
  initial: number,
  min: number,
  max: number,
  side: "left" | "right",
): {
  width: number
  dragging: boolean
  onPointerDown: (e: ReactPointerEvent<HTMLDivElement>) => void
  onDoubleClick: () => void
} {
  const clamp = useCallback((v: number) => Math.min(max, Math.max(min, Math.round(v))), [min, max])
  const [width, setWidth] = useState<number>(() => {
    try {
      const saved = Number(localStorage.getItem(storageKey))
      return Number.isFinite(saved) && saved >= min && saved <= max ? saved : initial
    } catch {
      return initial
    }
  })
  const [dragging, setDragging] = useState(false)
  const latest = useRef(width)
  latest.current = width

  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      if (e.button !== 0) return
      e.preventDefault()
      const startX = e.clientX
      const startW = latest.current
      setDragging(true)
      document.body.style.cursor = "col-resize"
      document.body.style.userSelect = "none"

      const onMove = (ev: PointerEvent) => {
        const delta = side === "right" ? ev.clientX - startX : startX - ev.clientX
        setWidth(clamp(startW + delta))
      }
      const onUp = () => {
        setDragging(false)
        document.body.style.cursor = ""
        document.body.style.userSelect = ""
        window.removeEventListener("pointermove", onMove)
        window.removeEventListener("pointerup", onUp)
        try {
          localStorage.setItem(storageKey, String(latest.current))
        } catch {
          // 浏览器禁用存储时仅在当前会话生效
        }
      }
      window.addEventListener("pointermove", onMove)
      window.addEventListener("pointerup", onUp)
    },
    [clamp, side, storageKey],
  )

  const onDoubleClick = useCallback(() => {
    setWidth(initial)
    try {
      localStorage.removeItem(storageKey)
    } catch {
      // 忽略存储异常
    }
  }, [initial, storageKey])

  // 卸载时兜底清理 body 样式
  useEffect(
    () => () => {
      document.body.style.cursor = ""
      document.body.style.userSelect = ""
    },
    [],
  )

  return { width, dragging, onPointerDown, onDoubleClick }
}

/** ResizeHandle 是贴在分栏边缘的可拖拽分隔手柄，悬停或拖动时显出品牌色细线，双击恢复默认宽度。 */
export function ResizeHandle({
  side,
  dragging,
  onPointerDown,
  onDoubleClick,
}: {
  side: "left" | "right"
  dragging: boolean
  onPointerDown: (e: ReactPointerEvent<HTMLDivElement>) => void
  onDoubleClick: () => void
}): ReactNode {
  const t = useT()
  const pos: CSSProperties = side === "right" ? { right: -3 } : { left: -3 }
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t("layout.resize")}
      className="desk-resize-handle"
      data-dragging={dragging || undefined}
      style={pos}
      onPointerDown={onPointerDown}
      onDoubleClick={onDoubleClick}
    />
  )
}
