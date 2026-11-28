import type { ConversationItemDTO } from "@agents-desk/client/tenant"
import { Button, Divider } from "@mantine/core"
import { IconArrowDown } from "@tabler/icons-react"
import { type StickToBottom, useStickToBottom as useCoreStickToBottom, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

export type { StickToBottom }

// 会话、聊天和工单三处时间线共用的滚动行为：基于 @go-tenon/ui 的 useStickToBottom，
// 补充工作台“N 条新消息 / 回到底部”浮钮与“新消息”分隔线。

/**
 * useStickToBottom 管理时间线的自动跟随：ready 为 true 后第一次落到底部（hold 为 true 时让位给定位滚动），
 * 自己发的消息或停在底部附近时跟随到底部，其余非系统事件计入未读条数；typingCount 变化时仅在贴底态跟随。
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
  return useCoreStickToBottom({
    ready,
    items: list,
    isOwn: (it) => it.actor.type === "member" && it.actor.id === myId,
    countItem: (it) => it.kind !== "event",
    followSignal: typingCount,
    hold,
  })
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
