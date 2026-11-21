import { Anchor } from "@mantine/core"
import type { ReactNode } from "react"
import { openLink } from "../host"

/** LINK 匹配消息中的网址：http:// 或 https:// 开头，到空白为止，去掉结尾的标点。 */
const LINK = /https?:\/\/[^\s<>\x22]+[^\s<>\x22\x27.,;:!?)\]}。，；：！？）】」』]/g

/** Linkify 把消息文字 text 中的网址显示成链接：App 中交给 App 用系统浏览器打开，其他时候在新的标签页中打开。 */
export function Linkify({ text }: { text: string }): ReactNode {
  const parts: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(LINK)) {
    const at = m.index ?? 0
    if (at > last) parts.push(text.slice(last, at))
    const url = m[0]
    parts.push(
      <Anchor
        key={at}
        href={url}
        c="inherit"
        underline="always"
        onClick={(e) => {
          e.preventDefault()
          openLink(url)
        }}
      >
        {url}
      </Anchor>,
    )
    last = at + url.length
  }
  if (last < text.length) parts.push(text.slice(last))
  return parts
}
