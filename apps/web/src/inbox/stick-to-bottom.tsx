import type { ConversationItemDTO } from "@agents-desk/client/tenant"
import { Button, Divider } from "@mantine/core"
import { IconArrowDown } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import { type ReactNode, type RefObject, useEffect, useRef, useState } from "react"

// 会话、聊天和工单三处时间线共用的滚动行为：打开时落到底部，新消息在底部附近或自己发出时跟随，
// 否则在右下角显示“N 条新消息 / 回到底部”；打开前已读位置之后的第一条别人的消息上方显示“新消息”分隔线。

/** StickToBottom 是 useStickToBottom 的结果，交给时间线的滚动容器和 ScrollToBottomButton。 */
export interface StickToBottom {
  scroller: RefObject<HTMLDivElement | null>
  onScroll: () => void
  showScrollDown: boolean
  unseenCount: number
  scrollToBottom: () => void
  /** settle 告诉它已经由别处（例如搜索定位）完成了首次滚动，之后只跟随新增的消息。 */
  settle: () => void
}

/**
 * useStickToBottom 管理时间线的自动跟随：ready 为 true 后第一次落到底部（hold 为 true 时让位给定位滚动），
 * 之后 list 增加时，自己发的或停在底部附近（80px 内）时跟随到底部，否则累计未看到的消息条数（不含系统事件）。
 * typingCount 变化时，停在底部附近才跟随“正在输入”。
 */
export function useStickToBottom({
  ready,
  list,
  myId,
  typingCount = 0,
  hold = false,
}: {
  ready: boolean
  list: ConversationItemDTO[]
  myId?: string
  typingCount?: number
  hold?: boolean
}): StickToBottom {
  const scroller = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true)
  const initialized = useRef(false)
  const prevCount = useRef(0)
  const [showScrollDown, setShowScrollDown] = useState(false)
  const [unseenCount, setUnseenCount] = useState(0)

  useEffect(() => {
    if (!ready) return
    const el = scroller.current
    if (!el) return
    if (!initialized.current) {
      if (hold) return
      initialized.current = true
      prevCount.current = list.length
      el.scrollTop = el.scrollHeight
      nearBottom.current = true
      return
    }
    if (list.length > prevCount.current) {
      const added = list.slice(prevCount.current)
      prevCount.current = list.length
      const hasOwn = added.some((it) => it.actor.type === "member" && it.actor.id === myId)
      if (hasOwn || nearBottom.current) {
        el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
        nearBottom.current = true
        setShowScrollDown(false)
        setUnseenCount(0)
      } else {
        const incoming = added.filter((it) => it.kind !== "event").length
        if (incoming > 0) {
          setUnseenCount((n) => n + incoming)
          setShowScrollDown(true)
        }
      }
      return
    }
    prevCount.current = list.length
  }, [ready, hold, list, myId])

  useEffect(() => {
    if (!initialized.current || typingCount === 0 || !nearBottom.current) return
    const el = scroller.current
    el?.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
  }, [typingCount])

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight
    nearBottom.current = gap <= 80
    if (gap <= 80) {
      setShowScrollDown(false)
      setUnseenCount(0)
    } else if (gap > 120) {
      setShowScrollDown(true)
    }
  }

  const scrollToBottom = () => {
    const el = scroller.current
    if (!el) return
    nearBottom.current = true
    setShowScrollDown(false)
    setUnseenCount(0)
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
  }

  const settle = () => {
    initialized.current = true
    prevCount.current = list.length
  }

  return { scroller, onScroll, showScrollDown, unseenCount, scrollToBottom, settle }
}

/** ScrollToBottomButton 是时间线右下角的“N 条新消息 / 回到底部”按钮，离开底部时出现。 */
export function ScrollToBottomButton({ stick }: { stick: StickToBottom }): ReactNode {
  const t = useT()
  if (!stick.showScrollDown) return null
  const hasUnseen = stick.unseenCount > 0
  return (
    <Button
      size={hasUnseen ? "xs" : "compact-xs"}
      radius="xl"
      color={hasUnseen ? "red" : undefined}
      variant={hasUnseen ? "filled" : "default"}
      leftSection={<IconArrowDown size={14} />}
      className="inbox-scroll-bottom"
      data-unread={hasUnseen || undefined}
      onClick={stick.scrollToBottom}
    >
      {hasUnseen ? t("inbox.new_message_jump", { n: stick.unseenCount }) : t("inbox.scroll_to_bottom")}
    </Button>
  )
}

/** firstUnread 返回打开前已读位置 readSeq 之后第一条别人发的条目；readSeq 为 0（从没读过）时不标。 */
export function firstUnread(list: ConversationItemDTO[], readSeq: number, myId?: string): ConversationItemDTO | undefined {
  if (readSeq <= 0) return undefined
  return list.find((it) => it.seq > readSeq && !(it.actor.type === "member" && it.actor.id === myId))
}

/** NewMessagesDivider 是“新消息”分隔线，放在 firstUnread 返回的条目上方。 */
export function NewMessagesDivider(): ReactNode {
  const t = useT()
  return (
    <Divider
      my={14}
      className="inbox-new-divider"
      label={<span className="inbox-new-divider-pill">{t("inbox.new_messages")}</span>}
      labelPosition="center"
    />
  )
}
