import type { Part } from "@agents-desk/client/tenant"
import { Fragment, type ReactNode } from "react"

/** HighlightedSnippet 渲染服务端全文搜索返回的结构化摘要片段，命中词以主题语义高亮标记呈现。 */
export function HighlightedSnippet({ parts }: { parts: Part[] }): ReactNode {
  return (
    <>
      {parts.map((part, i) =>
        part.mark ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: 摘要分段顺序固定且无其他唯一键
          <mark key={i} className="inbox-mark">
            {part.text}
          </mark>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: 摘要分段顺序固定且无其他唯一键
          <Fragment key={i}>{part.text}</Fragment>
        ),
      )}
    </>
  )
}

/** escapeRegExp 转义正则表达式中的特殊字符。 */
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}

/**
 * HighlightQuery 在纯文本 text 中高亮与查询词 query 匹配的片段（按空白拆词、不区分大小写）。
 * 用于会话时间线和命令面板中辅助高亮当前搜索关键词。
 */
export function HighlightQuery({ text, query }: { text: string; query?: string }): ReactNode {
  const raw = query?.trim()
  if (!raw || !text) return text
  const terms = [...new Set(raw.split(/\s+/).filter(Boolean))].sort((a, b) => b.length - a.length)
  if (terms.length === 0) return text
  const re = new RegExp(`(${terms.map(escapeRegExp).join("|")})`, "gi")
  const parts = text.split(re)
  if (parts.length === 1) return text
  const lower = new Set(terms.map((t) => t.toLowerCase()))
  return (
    <>
      {parts.map((part, i) =>
        lower.has(part.toLowerCase()) ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: 拆分片段顺序固定且无其他唯一键
          <mark key={i} className="inbox-mark">
            {part}
          </mark>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: 拆分片段顺序固定且无其他唯一键
          <Fragment key={i}>{part}</Fragment>
        ),
      )}
    </>
  )
}
