import { useEffect, useState } from "react"

/**
 * useViewportHeight 返回可见区域的高度：手机上软键盘弹出时可见区域变矮，聊天窗口跟着变矮，输入框贴在键盘上方。
 * 没有 visualViewport 的浏览器返回 undefined，由 100dvh 决定高度。
 */
export function useViewportHeight(): number | undefined {
  const [height, setHeight] = useState<number | undefined>(() =>
    typeof window === "undefined" ? undefined : window.visualViewport?.height,
  )
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    const update = () => {
      setHeight(vv.height)
      // iOS 弹出键盘时会把页面往上推，推回来，让聊天窗口的顶部留在原处
      window.scrollTo(0, 0)
    }
    vv.addEventListener("resize", update)
    return () => vv.removeEventListener("resize", update)
  }, [])
  return height
}
