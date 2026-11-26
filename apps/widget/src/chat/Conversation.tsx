import type {
  ConversationAttachmentDTO,
  ConversationAttachmentInput,
  ConversationContactItemDTO,
  ConversationContactTimelineDTO,
} from "@agents-desk/client/widget"
import { ActionIcon, Box, Button, Center, Divider, Group, Loader, Stack, Text, ThemeIcon, UnstyledButton } from "@mantine/core"
import { IconArrowDown, IconChevronRight, IconMessageCircle, IconTicket, IconX } from "@tabler/icons-react"
import { useQueryClient } from "@tanstack/react-query"
import { ImageLightbox, useLanguage, useT } from "@go-tenon/ui"
import { Fragment, type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"
import { authed, widgetApi } from "../api"
import { channel as currentChannelId } from "../host"
import { useIdentity } from "../realtime"
import { type LightboxImage, WidgetMessageAttachments } from "./Attachments"
import { Composer } from "./Composer"
import { useBrandForeground } from "./contrast"
import { clock, dayLabel, sameDay } from "./format"
import { Linkify } from "./linkify"
import { CollectEmailCard, promptText, ticketPromptTarget } from "./Prompt"
import { useTimeline } from "./queries"
import { RatingCard } from "./Rating"
import type { ReplyTicketContext } from "./TicketDetail"

/** GROUP_WINDOW_MS 是同一发送者连续消息合并为同一组的间隔上限（3 分钟）。 */
const GROUP_WINDOW_MS = 3 * 60 * 1000

/** canGroup 判断相邻两条时间线消息是否属于同轮次、同发送者、同渠道的连续消息组。 */
function canGroup(prev: ConversationContactItemDTO | undefined, cur: ConversationContactItemDTO): boolean {
  if (!prev || prev.prompt?.kind === "collect_contact" || cur.prompt?.kind === "collect_contact") return false
  if (prev.conversation_id !== cur.conversation_id) return false
  if (prev.author.type !== cur.author.type || prev.author.name !== cur.author.name) return false
  if (prev.channel?.id !== cur.channel?.id) return false
  if (!sameDay(prev.created_at, cur.created_at)) return false
  return Math.abs(new Date(cur.created_at).getTime() - new Date(prev.created_at).getTime()) <= GROUP_WINDOW_MS
}

/** Pending 是客户刚发、还没写进时间线的消息：发送中或者发送失败。 */
interface Pending {
  clientId: string // 重试时带同一个编号，消息只记录一次
  text: string
  inputs: ConversationAttachmentInput[]
  attachments: ConversationAttachmentDTO[]
  ticketId?: string
  failed: boolean
}

/** newClientId 返回一条消息的编号，服务端用它去重。 */
const newClientId = () => crypto.randomUUID().replace(/-/g, "")

/** FirstMessage 是客户还没有历史消息时，时间线底部贴近输入框的引导文案。 */
function FirstMessage({ color }: { color: string }): ReactNode {
  const t = useT()
  return (
    <Stack gap={6} align="center" py="lg" px="md">
      <ThemeIcon size={44} radius="xl" variant="light" color={color}>
        <IconMessageCircle size={22} />
      </ThemeIcon>
      <Text size="sm" fw={600} ta="center">
        {t("home.first_title")}
      </Text>
      <Text size="xs" c="dimmed" ta="center">
        {t("home.first_hint")}
      </Text>
    </Stack>
  )
}

/** ConversationProps 是连续对话流视图的参数。 */
interface ConversationProps {
  color: string
  name: string // 聊天插件的名称：系统发的消息显示它
  touch: boolean // 手机和 App
  visible: boolean // 聊天窗口展开着并且页面在前台：这时读到的消息记为已读
  replyTicket: ReplyTicketContext | null // 从工单详情带回的补充上下文
  onClearReplyTicket: () => void
  onOpenTicket: (id: string) => void
}

/**
 * ConversationView 是客户侧统一的跨会话连续对话流：
 *   - 向上滚动自动加载更早的历史消息并保持滚动位置；
 *   - 不同服务轮次之间显示轻量分隔线与历史评价摘要；
 *   - 工单进度消息卡片可直接跳转到工单详情，从工单详情返回时展示关联上下文条；
 *   - 最近一轮已关闭时在底部展示结束分隔线与评价卡片，客户可直接继续发消息开启或恢复服务。
 */
export function ConversationView({
  color,
  name,
  touch,
  visible,
  replyTicket,
  onClearReplyTicket,
  onOpenTicket,
}: ConversationProps): ReactNode {
  const t = useT()
  const language = useLanguage()
  const queryClient = useQueryClient()
  const { contact } = useIdentity()
  const { data, isPending, loadOlder, loadingOlder } = useTimeline()
  const [pending, setPending] = useState<Pending[]>([])
  const scroller = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true)
  const initialized = useRef(false)
  const prevTailId = useRef<string | undefined>(undefined)
  const prevCount = useRef(0)
  const prependAnchor = useRef<{ scrollHeight: number; scrollTop: number } | null>(null)
  const lastMarkedId = useRef<string | undefined>(undefined)
  const [showScrollDown, setShowScrollDown] = useState(false)
  const [unseenCount, setUnseenCount] = useState(0)
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)

  const list = data?.items ?? []
  const conversations = data?.conversations ?? {}
  const latest = data?.latest_conversation
  const unread = data?.unread ?? 0
  const hasMore = data?.has_more ?? false
  const closed = latest?.status === "closed"
  const lastItemId = list.at(-1)?.id

  // triggerLoadOlder 记录当前滚动锚点后拉取更早一页历史消息。
  const triggerLoadOlder = useCallback(async () => {
    const el = scroller.current
    if (!el || !hasMore || loadingOlder) return
    prependAnchor.current = { scrollHeight: el.scrollHeight, scrollTop: el.scrollTop }
    const loaded = await loadOlder()
    if (!loaded) prependAnchor.current = null
  }, [hasMore, loadingOlder, loadOlder])

  // 头部插入历史消息后，在浏览器绘制前补偿滚动高度差，保持客户当前阅读位置不动。
  useLayoutEffect(() => {
    const anchor = prependAnchor.current
    const el = scroller.current
    if (!anchor || !el) return
    const delta = el.scrollHeight - anchor.scrollHeight
    if (delta > 0) {
      el.scrollTop = anchor.scrollTop + delta
    }
    prependAnchor.current = null
    prevCount.current = list.length
    prevTailId.current = list.at(-1)?.id
  }, [list])

  // 首次加载直接落底；尾部追加新消息时，客户自己发的或在底部附近自动跟随，翻看历史时提示新消息数。
  useEffect(() => {
    if (isPending || prependAnchor.current) return
    const el = scroller.current
    if (!el) return
    const tailId = list.at(-1)?.id
    if (!initialized.current) {
      initialized.current = true
      prevCount.current = list.length
      prevTailId.current = tailId
      el.scrollTop = el.scrollHeight
      nearBottom.current = true
      return
    }
    if (tailId && tailId !== prevTailId.current) {
      const prevIndex = prevTailId.current ? list.findIndex((it) => it.id === prevTailId.current) : -1
      const added = prevIndex >= 0 ? list.slice(prevIndex + 1) : list.slice(prevCount.current)
      prevCount.current = list.length
      prevTailId.current = tailId
      if (added.length > 0) {
        const hasOwn = added.some((it) => it.author.type === "contact")
        if (hasOwn || nearBottom.current) {
          el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
          nearBottom.current = true
          setShowScrollDown(false)
          setUnseenCount(0)
        } else {
          setUnseenCount((n) => n + added.length)
          setShowScrollDown(true)
        }
      }
      return
    }
    prevCount.current = list.length
    prevTailId.current = tailId
  }, [isPending, list])

  // 自己刚发出新消息或最新一轮刚结束出现评价卡时（在底部附近）滚到底部。
  useEffect(() => {
    const el = scroller.current
    if (!el) return
    if (pending.length > 0 || (closed && nearBottom.current)) {
      el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
      nearBottom.current = true
      setShowScrollDown(false)
      setUnseenCount(0)
    }
  }, [pending.length, closed])

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
    if (el.scrollTop <= 60 && hasMore && !loadingOlder) {
      void triggerLoadOlder()
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

  // 聊天窗口可见且存在未读客服消息时，按最新一条消息 ID 标记已读并同步清零缓存中的未读数。
  useEffect(() => {
    if (!visible || unread <= 0 || !lastItemId || lastMarkedId.current === lastItemId) return
    lastMarkedId.current = lastItemId
    void authed(() => widgetApi.timeline.read({ up_to_item_id: lastItemId }))
      .then(() => {
        queryClient.setQueryData<ConversationContactTimelineDTO>(["widget", contact?.id, "timeline"], (cur) =>
          cur ? { ...cur, unread: 0 } : cur,
        )
      })
      .catch(() => {
        lastMarkedId.current = undefined
      })
  }, [visible, unread, lastItemId, queryClient, contact])

  // send 向统一消息入口发送消息，附带 client_id 去重与可选的工单上下文。
  const send = async (p: Pending) => {
    setPending((all) => [...all.filter((x) => x.clientId !== p.clientId), { ...p, failed: false }])
    try {
      await authed(() =>
        widgetApi.message.send({
          text: p.text || undefined,
          attachments: p.inputs.length > 0 ? p.inputs : undefined,
          client_id: p.clientId,
          ticket_id: p.ticketId,
        }),
      )
      if (p.ticketId) onClearReplyTicket()
      await queryClient.invalidateQueries({ queryKey: ["widget", contact?.id] })
      setPending((all) => all.filter((x) => x.clientId !== p.clientId))
    } catch {
      setPending((all) => all.map((x) => (x.clientId === p.clientId ? { ...x, failed: true } : x)))
    }
  }

  // 客户在当前活跃轮次输入时通知客服，每 3 秒最多一次。
  const lastTyping = useRef(0)
  const typing = () => {
    if (!latest || latest.status === "closed" || Date.now() - lastTyping.current < 3000) return
    lastTyping.current = Date.now()
    // “正在输入”是尽力而为的提示：网络波动时客户照常聊天。
    void authed(() => widgetApi.timeline.typing()).catch(() => {})
  }

  return (
    <>
      <Box className="w-scroll-wrap">
        <Box ref={scroller} className="w-scroll" onScroll={onScroll}>
          <Stack gap={0} p="md" justify={list.length === 0 && pending.length === 0 ? "flex-end" : undefined} mih="100%">
            {hasMore && (
              <Center mb="sm">
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="gray"
                  loading={loadingOlder}
                  onClick={() => void triggerLoadOlder()}
                >
                  {loadingOlder ? t("chat.loading_older") : t("chat.load_older")}
                </Button>
              </Center>
            )}
            {isPending ? (
              <Center style={{ flex: 1 }}>
                <Loader color={color} size="sm" />
              </Center>
            ) : (
              list.length === 0 && pending.length === 0 && <FirstMessage color={color} />
            )}
            {list.map((it, i) => {
              const prev = list[i - 1]
              const next = list[i + 1]
              const roundChanged = Boolean(prev?.conversation_id && it.conversation_id && prev.conversation_id !== it.conversation_id)
              const prevRound = prev?.conversation_id ? conversations[prev.conversation_id] : undefined
              const roundLabel = prevRound?.rating
                ? `${t("chat.round_ended")} · ${t("chat.rated_summary", { score: prevRound.rating.score })}`
                : t("chat.round_ended")
              const showDay = i === 0 || !sameDay(prev.created_at, it.created_at)
              const groupedWithPrev = !showDay && !roundChanged && canGroup(prev, it)
              const groupedWithNext = Boolean(next) && canGroup(it, next)
              const gapTop = i === 0 || roundChanged || showDay ? 0 : groupedWithPrev ? 4 : 10
              const ticketTarget = ticketPromptTarget(it.prompt)
              return (
                <Fragment key={it.id}>
                  {roundChanged && <Divider label={roundLabel} labelPosition="center" my="sm" />}
                  {showDay && (
                    <Text size="xs" c="dimmed" ta="center" my={i === 0 ? 4 : 8}>
                      {dayLabel(it.created_at, language, t)}
                    </Text>
                  )}
                  {it.prompt?.kind === "collect_contact" ? (
                    <Box mt={gapTop}>
                      <CollectEmailCard conversationId={it.conversation_id ?? latest?.id} color={color} />
                    </Box>
                  ) : (
                    <Bubble
                      mine={it.author.type === "contact"}
                      author={!groupedWithPrev ? (it.author.type === "member" ? it.author.name : it.author.type === "system" ? name : undefined) : undefined}
                      text={promptText(it.prompt, it.text, language, t)}
                      attachments={it.attachments}
                      action={
                        ticketTarget ? (
                          <Button
                            mt={6}
                            size="compact-xs"
                            variant="light"
                            color={color}
                            leftSection={<IconTicket size={13} />}
                            rightSection={<IconChevronRight size={13} />}
                            onClick={() => onOpenTicket(ticketTarget.id)}
                          >
                            {t("tickets.view_ticket", { number: ticketTarget.number })}
                          </Button>
                        ) : undefined
                      }
                      meta={
                        !groupedWithNext
                          ? [
                              it.channel && it.channel.id !== currentChannelId ? t("chat.via_channel", { channel: it.channel.name }) : "",
                              clock(it.created_at, language),
                            ]
                              .filter(Boolean)
                              .join(" · ")
                          : undefined
                      }
                      color={color}
                      first={!groupedWithPrev}
                      last={!groupedWithNext}
                      mt={gapTop}
                      onPreview={setLightbox}
                    />
                  )}
                </Fragment>
              )
            })}
            {pending.map((p, idx) => (
              <Bubble
                key={p.clientId}
                mine
                text={p.text}
                attachments={p.attachments}
                color={color}
                first={idx === 0}
                last={idx === pending.length - 1}
                mt={idx === 0 ? 10 : 4}
                state={p.failed ? "failed" : "sending"}
                onPreview={setLightbox}
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
            {closed && latest && (
              <>
                <Divider label={t("chat.ended")} labelPosition="center" my="sm" />
                {(latest.rating_requested || latest.rating) && <RatingCard conversation={latest} color={color} />}
              </>
            )}
          </Stack>
        </Box>
        {showScrollDown && (
          <Button
            size="compact-xs"
            radius="xl"
            color={unseenCount > 0 ? color : undefined}
            variant={unseenCount > 0 ? "filled" : "default"}
            leftSection={<IconArrowDown size={13} />}
            className="w-scroll-bottom"
            onClick={scrollToBottom}
          >
            {unseenCount > 0 ? t("chat.new_message_jump", { n: unseenCount }) : t("chat.scroll_to_bottom")}
          </Button>
        )}
      </Box>
      {replyTicket && (
        <Group
          gap="xs"
          px="sm"
          py={6}
          wrap="nowrap"
          bg="var(--mantine-color-yellow-light)"
          style={{ borderTop: "1px solid var(--mantine-color-yellow-3)" }}
        >
          <IconTicket size={15} color="var(--mantine-color-yellow-7)" style={{ flex: "none" }} />
          <Text size="xs" fw={500} style={{ flex: 1, minWidth: 0 }} truncate>
            {t("tickets.replying_context", { number: replyTicket.number, title: replyTicket.title })}
          </Text>
          <ActionIcon size="xs" variant="subtle" color="gray" onClick={onClearReplyTicket} aria-label={t("tickets.clear_context")}>
            <IconX size={14} />
          </ActionIcon>
        </Group>
      )}
      <Composer
        color={color}
        touch={touch}
        placeholder={t("composer.placeholder")}
        onSend={(text, inputs, attachments) =>
          void send({
            clientId: newClientId(),
            text,
            inputs,
            attachments,
            ticketId: replyTicket?.id,
            failed: false,
          })
        }
        onTyping={typing}
      />
      <ImageLightbox src={lightbox?.src ?? null} filename={lightbox?.filename} onClose={() => setLightbox(null)} />
    </>
  )
}

/** Bubble 是时间线中的一条消息：客户的在右边、用主题色，别人的在左边、纯白底配轻边框与名字。 */
export function Bubble({
  mine,
  author,
  text,
  attachments,
  action,
  meta,
  color,
  first = true,
  last = true,
  mt = 0,
  state,
  onPreview,
}: {
  mine: boolean
  author?: string
  text: string
  attachments?: ConversationAttachmentDTO[]
  action?: ReactNode
  meta?: ReactNode
  color: string
  first?: boolean
  last?: boolean
  mt?: number
  state?: "sending" | "failed"
  onPreview: (image: LightboxImage) => void
}): ReactNode {
  const fg = useBrandForeground()
  const mediaOnly = !text && !action && Boolean(attachments?.length) && attachments!.every((a) => a.kind === "image")
  return (
    <Box
      mt={mt}
      style={{ alignSelf: mine ? "flex-end" : "flex-start", maxWidth: "82%", display: "flex", flexDirection: "column", alignItems: mine ? "flex-end" : "flex-start" }}
    >
      {author && (
        <Text size="xs" c="dimmed" mx={4} mb={2}>
          {author}
        </Text>
      )}
      <Box
        className="w-bubble"
        data-mine={mine || undefined}
        data-first={first || undefined}
        data-last={last || undefined}
        data-state={state}
        data-media-only={mediaOnly || undefined}
        bg={mine ? (state === "failed" ? "red.1" : `var(--mantine-color-${color}-filled)`) : undefined}
        c={mine ? (state === "failed" ? "red.8" : fg) : undefined}
      >
        {text && <Linkify text={text} />}
        <WidgetMessageAttachments attachments={attachments} hasText={Boolean(text)} mine={mine} onPreview={onPreview} />
        {action && <Box>{action}</Box>}
      </Box>
      {meta && (
        <Text size="xs" c={state === "failed" ? "red" : "dimmed"} mx={4} mt={2} component="div" style={{ fontVariantNumeric: "tabular-nums" }}>
          {meta}
        </Text>
      )}
    </Box>
  )
}
