import { useCallback, useEffect, useRef } from "react"
import { tenantApi } from "../api"

// 看到时补译：收到的消息只预先翻译成工作区的主要语言，其他语言的成员看到消息时补上。时间线中标为 untranslated 的消息、
// 会话列表中 preview_untranslated 的行挂上 useFillVisible 返回的 ref，滚进可见区域时攒一批调用补译接口，
// 后端判断哪些真的要补，译好后推送过来，页面重新读取。

/** FILL_BATCH 是一次请求补译的消息数上限，和接口一致；FILL_DELAY 是攒一批可见消息再请求的等待时间（毫秒）。 */
const FILL_BATCH = 100
const FILL_DELAY = 300

/** requested 是这个页面已经请求过补译的消息：每条只请求一次，失败时去掉，下次可见再请求。 */
const requested = new Set<string>()

/** pending 是等着一起请求的消息。 */
const pending = new Set<string>()

/** timer 是攒一批的计时。 */
let timer: ReturnType<typeof setTimeout> | undefined

/** flush 把攒下的消息分批交给补译接口。 */
function flush(): void {
  const ids = [...pending].slice(0, FILL_BATCH)
  for (const id of ids) pending.delete(id)
  if (pending.size > 0) timer = setTimeout(flush, FILL_DELAY)
  if (ids.length === 0) return
  tenantApi.translation.fill({ item_ids: ids }).catch(() => {
    for (const id of ids) requested.delete(id)
  })
}

/** FillRef 是挂在消息元素上的 ref 回调，元素的 data-item-id 是要补译的消息。 */
export type FillRef = (el: HTMLElement | null) => (() => void) | undefined

/**
 * useFillVisible 返回挂在还缺译文的消息上的 ref 回调：元素（带 data-item-id）滚进可见区域时请求补译。
 * enabled 为假（翻译不可用）时什么也不做。
 */
export function useFillVisible(enabled: boolean): FillRef {
  const elements = useRef(new Set<HTMLElement>()) // 挂着 ref 的元素
  const observer = useRef<IntersectionObserver | null>(null)

  useEffect(() => {
    if (!enabled) return
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) {
        const id = (e.target as HTMLElement).dataset.itemId
        if (!e.isIntersecting || !id || requested.has(id)) continue
        requested.add(id)
        pending.add(id)
      }
      clearTimeout(timer)
      timer = setTimeout(flush, FILL_DELAY)
    })
    observer.current = io
    for (const el of elements.current) io.observe(el)
    return () => {
      io.disconnect()
      observer.current = null
    }
  }, [enabled])

  return useCallback((el: HTMLElement | null) => {
    if (!el) return undefined
    elements.current.add(el)
    observer.current?.observe(el)
    return () => {
      elements.current.delete(el)
      observer.current?.unobserve(el)
    }
  }, [])
}
