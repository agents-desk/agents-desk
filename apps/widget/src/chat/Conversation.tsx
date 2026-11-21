import type { ConversationContactItemDTO } from "@agents-desk/client/widget"
import { Box, Divider, Stack, Text, UnstyledButton } from "@mantine/core"
import { useQueryClient } from "@tanstack/react-query"
import { useLanguage, useT } from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { authed, widgetApi } from "../api"
import { useIdentity } from "../realtime"
import { Composer } from "./Composer"
import { clock, dayLabel, sameDay } from "./format"
import { Linkify } from "./linkify"
import { useConversation, useItems } from "./queries"
import { RatingCard } from "./Rating"

/** Pending 是客户刚发、还没写进时间线的消息：发送中或者发送失败。 */
interface Pending {
  clientId: string // 重试时带同一个编号，消息只记录一次
  text: string
  failed: boolean
}

/** newClientId 返回一条消息的编号，服务端用它去重。 */
const newClientId = () => crypto.randomUUID().replace(/-/g, "")

/** ConversationProps 是会话页的设置。 */
interface ConversationProps {
  id?: string // 会话；为空时是新会话，发出第一条后由 onStarted 换成新建的会话
  intro?: ReactNode // 新会话还没有消息时，时间线中贴着输入框显示的提示
  color: string
  name: string // 聊天插件的名称：系统发的消息显示它
  touch: boolean // 手机和 App
  visible: boolean // 聊天窗口展开着并且页面在前台：这时读到的消息记为已读
  onStarted: (id: string) => void
  onBack: () => void
}

/**
 * ConversationView 是会话页：时间线、关闭后的评价卡片和输入框。
 *   - 客户的消息在右边，成员的在左边并显示名字，按日期分组；
 *   - 发出的消息先显示为发送中，失败时可以重试，重试用同一个 client_id；
 *   - 可见时把最后一项记为已读；
 *   - 手机上从左边缘向右滑返回首页。
 */
export function ConversationView({ id, intro, color, name, touch, visible, onStarted, onBack }: ConversationProps): ReactNode {
  const t = useT()
  const language = useLanguage()
  const queryClient = useQueryClient()
  const { contact } = useIdentity()
  const conversation = useConversation(id ?? "")
  const items = useItems(id ?? "")
  const [pending, setPending] = useState<Pending[]>([])
  const bottom = useRef<HTMLDivElement>(null)
  const lastRead = useRef(0)

  const list: ConversationContactItemDTO[] = id ? (items.data ?? []) : []
  const closed = conversation.data?.status === "closed"

  // 新的内容出现时滚到底部
  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" })
  }, [list.length, pending.length, closed])

  // 可见时把读到的最后一项记为已读，未读数随之清零
  const lastSeq = list.at(-1)?.seq ?? 0
  useEffect(() => {
    if (!id || !visible || lastSeq <= lastRead.current) return
    if (!list.some((it) => it.seq > lastRead.current && it.author.type !== "contact")) {
      lastRead.current = lastSeq
      return
    }
    lastRead.current = lastSeq
    void authed(() => widgetApi.conversation.read(id, { seq: lastSeq })).then(() =>
      queryClient.invalidateQueries({ queryKey: ["widget", contact?.id, "conversations"] }),
    )
  }, [id, visible, lastSeq, list, queryClient, contact])

  // send 发出一条消息：新会话时发起会话，否则发到这个会话；成功后时间线和列表重新读取
  const send = async (p: Pending) => {
    setPending((all) => [...all.filter((x) => x.clientId !== p.clientId), { ...p, failed: false }])
    try {
      const body = { text: p.text, client_id: p.clientId }
      const sent = await authed(() => (id ? widgetApi.conversation.send(id, body) : widgetApi.conversation.start(body)))
      await queryClient.invalidateQueries({ queryKey: ["widget", contact?.id] })
      setPending((all) => all.filter((x) => x.clientId !== p.clientId))
      if (!id) onStarted(sent.conversation_id)
    } catch {
      setPending((all) => all.map((x) => (x.clientId === p.clientId ? { ...x, failed: true } : x)))
    }
  }

  // 手机上从左边缘向右滑返回首页
  const touchStart = useRef<{ x: number; y: number } | null>(null)
  const swipe = touch
    ? {
        onTouchStart: (e: React.TouchEvent) => {
          const p = e.touches[0]
          touchStart.current = p.clientX < 40 ? { x: p.clientX, y: p.clientY } : null
        },
        onTouchEnd: (e: React.TouchEvent) => {
          const s = touchStart.current
          const p = e.changedTouches[0]
          if (s && p.clientX - s.x > 80 && Math.abs(p.clientY - s.y) < 60) onBack()
          touchStart.current = null
        },
      }
    : {}

  return (
    <>
      <Box className="w-scroll" {...swipe}>
        <Stack gap={8} p="md" justify={!id ? "flex-end" : undefined} mih="100%">
          {!id && pending.length === 0 && intro}
          {list.map((it, i) => (
            <Fragment key={it.id}>
              {(i === 0 || !sameDay(list[i - 1].created_at, it.created_at)) && (
                <Text size="xs" c="dimmed" ta="center" my={4}>
                  {dayLabel(it.created_at, language, t)}
                </Text>
              )}
              <Bubble
                mine={it.author.type === "contact"}
                author={it.author.type === "member" ? it.author.name : it.author.type === "system" ? name : undefined}
                text={it.text}
                meta={clock(it.created_at, language)}
                color={color}
              />
            </Fragment>
          ))}
          {pending.map((p) => (
            <Bubble
              key={p.clientId}
              mine
              text={p.text}
              color={color}
              state={p.failed ? "failed" : "sending"}
              meta={
                p.failed ? (
                  <>
                    {t("chat.failed")}
                    {" · "}
                    <UnstyledButton fz="xs" c="red" td="underline" onClick={() => void send(p)}>
                      {t("chat.retry")}
                    </UnstyledButton>
                  </>
                ) : (
                  t("chat.sending")
                )
              }
            />
          ))}
          {closed && conversation.data && (
            <>
              <Divider label={t("chat.ended")} labelPosition="center" my="xs" />
              <RatingCard conversation={conversation.data} color={color} />
            </>
          )}
          <div ref={bottom} />
        </Stack>
      </Box>
      <Composer
        color={color}
        touch={touch}
        placeholder={t(closed ? "composer.reopen_placeholder" : "composer.placeholder")}
        onSend={(text) => void send({ clientId: newClientId(), text, failed: false })}
      />
    </>
  )
}

/** Bubble 是时间线中的一条消息：客户的在右边、用主题色，别人的在左边、上面是名字。 */
function Bubble({
  mine,
  author,
  text,
  meta,
  color,
  state,
}: {
  mine: boolean
  author?: string
  text: string
  meta: ReactNode
  color: string
  state?: "sending" | "failed"
}): ReactNode {
  return (
    <Box style={{ alignSelf: mine ? "flex-end" : "flex-start", maxWidth: "82%", display: "flex", flexDirection: "column", alignItems: mine ? "flex-end" : "flex-start" }}>
      {author && (
        <Text size="xs" c="dimmed" mx={4} mb={2}>
          {author}
        </Text>
      )}
      <Box
        className="w-bubble"
        data-mine={mine || undefined}
        data-state={state}
        bg={mine ? (state === "failed" ? "red.1" : `var(--mantine-color-${color}-filled)`) : "var(--mantine-color-gray-1)"}
        c={mine ? (state === "failed" ? "red.8" : "white") : undefined}
      >
        <Linkify text={text} />
      </Box>
      <Text size="xs" c={state === "failed" ? "red" : "dimmed"} mx={4} mt={2} component="div">
        {meta}
      </Text>
    </Box>
  )
}
