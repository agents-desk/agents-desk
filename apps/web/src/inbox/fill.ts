import { useCallback, useEffect, useRef } from "react"
import { tenantApi } from "../api"

// 看到时补译：收到的消息只预先翻译成工作区的主要语言，其他语言的成员看到消息时补上。时间线中标为 untranslated 的消息、
// 会话列表中 preview_untranslated 的行挂上 useFillVisible 返回的 ref，滚进可见区域时攒一批调用补译接口，
// 后端判断哪些真的要补，译好后推送过来，页面重新读取。

/** FILL_BATCH 是一次请求补译的消息数上限，和接口一致；FILL_DELAY 是攒一批可见消息再请求的等待时间（毫秒）。 */
const FILL_BATCH = 100
const FILL_DELAY = 300

/** FILL_COOLDOWN 是同一语言下同一条消息两次补译请求的最小间隔（毫秒）：间隔内的重复请求合并为一次；后台重试全部失败后，消息再次出现在视口中时重新补译。 */
const FILL_COOLDOWN = 60_000

/** requested 记录“语言:消息ID”最近一次请求补译的时间；请求失败时立刻移除。 */
const requested = new Map<string, number>()

/** pending 是等着一起请求的消息，键是“语言:消息ID”，值是消息 ID。 */
const pending = new Map<string, string>()

/** timer 是攒一批的计时。 */
let timer: ReturnType<typeof setTimeout> | undefined

/** flush 把攒下的消息分批交给补译接口。 */
function flush(): void {
  const batch = [...pending.entries()].slice(0, FILL_BATCH)
  for (const [key] of batch) pending.delete(key)
  if (pending.size > 0) timer = setTimeout(flush, FILL_DELAY)
  if (batch.length === 0) return
  const ids = [...new Set(batch.map(([, id]) => id))]
  tenantApi.translation.fill({ item_ids: ids }).catch(() => {
    for (const [key] of batch) requested.delete(key)
  })
}

/** enqueue 把语言 lang 下的消息 id 放进下一批补译请求；冷却期内跳过。 */
function enqueue(lang: string, id: string): void {
  const key = `${lang}:${id}`
  const now = Date.now()
  const prev = requested.get(key)
  if (prev !== undefined && now - prev < FILL_COOLDOWN) return
  requested.set(key, now)
  pending.set(key, id)
  clearTimeout(timer)
  timer = setTimeout(flush, FILL_DELAY)
}

/** FillRef 是挂在消息元素上的 ref 回调。 */
export type FillRef = (el: HTMLElement | null) => (() => void) | undefined

/** FillWatcher 按消息 ID 返回稳定的 ref 回调：同一条消息复用回调，消息 ID 变化时重新观测。 */
export type FillWatcher = (id: string) => FillRef

/**
 * useFillVisible 返回按消息 ID 获取 ref 回调的函数：挂在还缺译文的消息元素上，滚进可见区域时请求补译。
 * 按语言去重（切换偏好语言后立刻对新语言重新生效），enabled 为假或 language 未就绪时什么也不做。
 */
export function useFillVisible(enabled: boolean, language: string | undefined): FillWatcher {
  const elements = useRef(new Map<HTMLElement, string>()) // 观测中的元素对应的消息 ID
  const observer = useRef<IntersectionObserver | null>(null)
  const refs = useRef(new Map<string, FillRef>()) // 按消息 ID 缓存 ref 回调，避免无关渲染重复 unobserve/observe

  useEffect(() => {
    if (!enabled || !language) return
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        const id = elements.current.get(e.target as HTMLElement)
        if (id) enqueue(language, id)
      }
    })
    observer.current = io
    for (const el of elements.current.keys()) io.observe(el)
    return () => {
      io.disconnect()
      observer.current = null
    }
  }, [enabled, language])

  return useCallback((id: string): FillRef => {
    let ref = refs.current.get(id)
    if (!ref) {
      ref = (el: HTMLElement | null) => {
        if (!el) return undefined
        elements.current.set(el, id)
        observer.current?.observe(el)
        return () => {
          elements.current.delete(el)
          observer.current?.unobserve(el)
          refs.current.delete(id)
        }
      }
      refs.current.set(id, ref)
    }
    return ref
  }, [])
}
