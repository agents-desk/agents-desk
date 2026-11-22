import { type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { useT } from "@go-tenon/ui"

/** NARROW_QUERY 是工作台端统一的窄屏断点（小于 900px）：二级导航收进抽屉，列表与详情轮流占满。 */
export const NARROW_QUERY = "(max-width: 899px)"

/**
 * isTypingTarget 报告键盘事件是否落在输入框、下拉选择、富文本或弹窗中：
 * 为真时工作台单键快捷键（如 J/K/I/R/E）不触发，避免打断正常输入。
 */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true
  return Boolean(target.closest("[role='dialog'], [role='menu'], [role='listbox'], [contenteditable='true']"))
}

/**
 * useResizableWidth 管理可拖拽与键盘微调并保存在本地存储中的栏宽（像素）。
 * side 表示拖拽手柄位于面板的左侧还是右侧，双击手柄或按 Home 可恢复默认宽度。
 */
export function useResizableWidth(
  storageKey: string,
  initial: number,
  min: number,
  max: number,
  side: "left" | "right",
): {
  width: number
  min: number
  max: number
  dragging: boolean
  onPointerDown: (e: ReactPointerEvent<HTMLDivElement>) => void
  onDoubleClick: () => void
  onKeyDown: (e: ReactKeyboardEvent<HTMLDivElement>) => void
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

  const persist = useCallback(
    (next: number) => {
      try {
        localStorage.setItem(storageKey, String(next))
      } catch {
        // 浏览器禁用存储时仅在当前会话生效
      }
    },
    [storageKey],
  )

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
        persist(latest.current)
      }
      window.addEventListener("pointermove", onMove)
      window.addEventListener("pointerup", onUp)
    },
    [clamp, persist, side],
  )

  const onDoubleClick = useCallback(() => {
    setWidth(initial)
    try {
      localStorage.removeItem(storageKey)
    } catch {
      // 忽略存储异常
    }
  }, [initial, storageKey])

  const onKeyDown = useCallback(
    (e: ReactKeyboardEvent<HTMLDivElement>) => {
      if (e.key === "Home") {
        e.preventDefault()
        onDoubleClick()
        return
      }
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return
      e.preventDefault()
      const step = e.shiftKey ? 48 : 16
      const dir = e.key === "ArrowRight" ? 1 : -1
      const delta = side === "right" ? dir * step : -dir * step
      const next = clamp(latest.current + delta)
      setWidth(next)
      persist(next)
    },
    [clamp, onDoubleClick, persist, side],
  )

  // 卸载时兜底清理 body 样式
  useEffect(
    () => () => {
      document.body.style.cursor = ""
      document.body.style.userSelect = ""
    },
    [],
  )

  return { width, min, max, dragging, onPointerDown, onDoubleClick, onKeyDown }
}

/** ResizeHandle 是贴在分栏边缘的可拖拽分隔手柄，支持鼠标拖拽与键盘方向键微调，双击或按 Home 恢复默认宽度。 */
export function ResizeHandle({
  side,
  dragging,
  width,
  min,
  max,
  onPointerDown,
  onDoubleClick,
  onKeyDown,
}: {
  side: "left" | "right"
  dragging: boolean
  width?: number
  min?: number
  max?: number
  onPointerDown: (e: ReactPointerEvent<HTMLDivElement>) => void
  onDoubleClick: () => void
  onKeyDown?: (e: ReactKeyboardEvent<HTMLDivElement>) => void
}): ReactNode {
  const t = useT()
  const pos: CSSProperties = side === "right" ? { right: -3 } : { left: -3 }
  return (
    <div
      role="separator"
      tabIndex={0}
      aria-orientation="vertical"
      aria-label={t("layout.resize")}
      aria-valuenow={width}
      aria-valuemin={min}
      aria-valuemax={max}
      className="desk-resize-handle"
      data-dragging={dragging || undefined}
      style={pos}
      onPointerDown={onPointerDown}
      onDoubleClick={onDoubleClick}
      onKeyDown={onKeyDown}
    />
  )
}
