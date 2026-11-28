// 消息列表/时间线贴底跟随钩子：打开时首次滚动到底部，之后仅当本人发送或停留在底部附近时跟随，
// 用户上翻阅读历史时累计未读条数，并提供“回到底部”按钮所需状态与跳转方法。

import { type RefObject, useCallback, useEffect, useRef, useState } from "react"

/** useStickToBottom 的选项。 */
export interface StickToBottomOptions<T> {
  /** 数据是否已就绪；首次变为 true 时若未 hold 则直接滚动到底部。 */
  ready: boolean
  /** 按时间从旧到新排列的列表。 */
  items: readonly T[]
  /** 判断新增条目是否来自当前操作人本人；为 true 时无论当前滚动位置如何均滚到底部。 */
  isOwn?: (item: T) => boolean
  /** 判断新增非本人条目是否计入未读提示（例如排除纯系统事件）；省略时全部计入。 */
  countItem?: (item: T) => boolean
  /** 用于触发平滑贴底跟随的辅助变化值（例如正在输入状态数量）。 */
  followSignal?: number
  /** 为 true 时延后首次滚到底部（例如搜索定位需要先滚到命中条目）。 */
  hold?: boolean
  /** 距离底部的阈值像素，默认 80。 */
  threshold?: number
}

/** useStickToBottom 返回的状态与容器绑定属性。 */
export interface StickToBottom {
  scroller: RefObject<HTMLDivElement | null>
  onScroll: () => void
  showScrollDown: boolean
  unseenCount: number
  scrollToBottom: () => void
  /** 通知钩子首屏定位已完成，后续仅按增量列表执行跟随。 */
  settle: () => void
}

/** 泛型消息列表贴底跟随钩子。 */
export function useStickToBottom<T>({
  ready,
  items,
  isOwn,
  countItem,
  followSignal = 0,
  hold = false,
  threshold = 80,
}: StickToBottomOptions<T>): StickToBottom {
  const scroller = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true)
  const initialized = useRef(false)
  const prevCount = useRef(0)
  const isOwnRef = useRef(isOwn)
  isOwnRef.current = isOwn
  const countItemRef = useRef(countItem)
  countItemRef.current = countItem
  const [showScrollDown, setShowScrollDown] = useState(false)
  const [unseenCount, setUnseenCount] = useState(0)

  useEffect(() => {
    if (!ready) return
    const el = scroller.current
    if (!el) return
    if (!initialized.current) {
      if (hold) return
      initialized.current = true
      prevCount.current = items.length
      el.scrollTop = el.scrollHeight
      nearBottom.current = true
      return
    }
    if (items.length > prevCount.current) {
      const added = items.slice(prevCount.current)
      prevCount.current = items.length
      const hasOwn = isOwnRef.current ? added.some((it) => isOwnRef.current?.(it)) : false
      if (hasOwn || nearBottom.current) {
        el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
        nearBottom.current = true
        setShowScrollDown(false)
        setUnseenCount(0)
      } else {
        const incoming = countItemRef.current ? added.filter((it) => countItemRef.current?.(it)).length : added.length
        if (incoming > 0) {
          setUnseenCount((n) => n + incoming)
          setShowScrollDown(true)
        }
      }
      return
    }
    prevCount.current = items.length
  }, [ready, hold, items])

  useEffect(() => {
    if (!initialized.current || followSignal === 0 || !nearBottom.current) return
    const el = scroller.current
    el?.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
  }, [followSignal])

  // 监听列表内部高度变化（如异步图片加载完成），停留在底部时继续贴底。
  useEffect(() => {
    if (!ready || typeof ResizeObserver === "undefined") return
    const el = scroller.current
    const target = el?.firstElementChild
    if (!el || !target) return
    const observer = new ResizeObserver(() => {
      if (!initialized.current || !nearBottom.current) return
      el.scrollTop = el.scrollHeight
    })
    observer.observe(target)
    return () => observer.disconnect()
  }, [ready])

  const onScroll = useCallback(() => {
    const el = scroller.current
    if (!el) return
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight
    nearBottom.current = gap <= threshold
    if (gap <= threshold) {
      setShowScrollDown(false)
      setUnseenCount(0)
    } else if (gap > threshold + 40) {
      setShowScrollDown(true)
    }
  }, [threshold])

  const scrollToBottom = useCallback(() => {
    const el = scroller.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
    nearBottom.current = true
    setShowScrollDown(false)
    setUnseenCount(0)
  }, [])

  const settle = useCallback(() => {
    if (!initialized.current) {
      initialized.current = true
      prevCount.current = items.length
    }
    const el = scroller.current
    if (!el) return
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight
    nearBottom.current = gap <= threshold
  }, [items.length, threshold])

  return { scroller, onScroll, showScrollDown, unseenCount, scrollToBottom, settle }
}
