import type { ConversationAttachmentDTO, ConversationAttachmentInput, ConversationContactItemDTO } from "@agents-desk/client/widget"
import { Box, Button, Divider, Stack, Text, UnstyledButton } from "@mantine/core"
import { IconArrowDown } from "@tabler/icons-react"
import { useQueryClient } from "@tanstack/react-query"
import { ImageLightbox, useLanguage, useT } from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { authed, widgetApi } from "../api"
import { useIdentity } from "../realtime"
import { type LightboxImage, WidgetMessageAttachments } from "./Attachments"
import { Composer } from "./Composer"
import { useBrandForeground } from "./contrast"
import { clock, dayLabel, sameDay } from "./format"
import { Linkify } from "./linkify"
import { CollectEmailCard, promptText } from "./Prompt"
import { useConversation, useItems } from "./queries"
import { RatingCard } from "./Rating"

/** GROUP_WINDOW_MS 是同一发送者连续消息合并为同一组的间隔上限（3 分钟）。 */
const GROUP_WINDOW_MS = 3 * 60 * 1000

/** canGroup 判断相邻两条时间线消息是否属于同人连续发送的同一组。 */
function canGroup(prev: ConversationContactItemDTO | undefined, cur: ConversationContactItemDTO): boolean {
  if (!prev || prev.prompt?.kind === "collect_contact" || cur.prompt?.kind === "collect_contact") return false
  if (prev.author.type !== cur.author.type || prev.author.name !== cur.author.name) return false
  if (!sameDay(prev.created_at, cur.created_at)) return false
  return Math.abs(new Date(cur.created_at).getTime() - new Date(prev.created_at).getTime()) <= GROUP_WINDOW_MS
}

/** Pending 是客户刚发、还没写进时间线的消息：发送中或者发送失败。 */
interface Pending {
  clientId: string // 重试时带同一个编号，消息只记录一次
  text: string
  inputs: ConversationAttachmentInput[]
  attachments: ConversationAttachmentDTO[]
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
  onBack?: () => void
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
  const scroller = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true)
  const initialized = useRef(false)
  const prevCount = useRef(0)
  const [showScrollDown, setShowScrollDown] = useState(false)
  const [unseenCount, setUnseenCount] = useState(0)
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)
  const lastRead = useRef(0)

  const list: ConversationContactItemDTO[] = id ? (items.data ?? []) : []
  const closed = conversation.data?.status === "closed"

  // 首次加载直接落底；客户发消息或在底部附近时自动跟随，翻看历史时不强行拉回底部
  useEffect(() => {
    if (id && items.isPending) return
    const el = scroller.current
    if (!el) return
    if (!initialized.current) {
      initialized.current = true
      prevCount.current = list.length
      el.scrollTop = el.scrollHeight
      nearBottom.current = true
      return
    }
    if (list.length > prevCount.current) {
      const added = list.slice(prevCount.current)
      prevCount.current = list.length
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
      return
    }
    prevCount.current = list.length
  }, [id, items.isPending, list])

  // 自己刚发出新消息或会话刚结束出现评价卡时（在底部附近）滚到底部
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
  }

  const scrollToBottom = () => {
    const el = scroller.current
    if (!el) return
    nearBottom.current = true
    setShowScrollDown(false)
    setUnseenCount(0)
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
  }

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
      const body = {
        text: p.text || undefined,
        attachments: p.inputs.length > 0 ? p.inputs : undefined,
        client_id: p.clientId,
      }
      const sent = await authed(() => (id ? widgetApi.conversation.send(id, body) : widgetApi.conversation.start(body)))
      await queryClient.invalidateQueries({ queryKey: ["widget", contact?.id] })
      setPending((all) => all.filter((x) => x.clientId !== p.clientId))
      if (sent.conversation_id !== id) onStarted(sent.conversation_id)
    } catch {
      setPending((all) => all.map((x) => (x.clientId === p.clientId ? { ...x, failed: true } : x)))
    }
  }

  // 客户在输入时告诉成员，每 3 秒最多一次；新会话还没有会话可以告诉
  const lastTyping = useRef(0)
  const typing = () => {
    if (!id || Date.now() - lastTyping.current < 3000) return
    lastTyping.current = Date.now()
    // “正在输入”是尽力而为的提示：发不出去时客户照常聊天，不提示错误
    void authed(() => widgetApi.conversation.typing(id)).catch(() => {})
  }

  // 手机上从左边缘向右滑返回首页（仅在多会话模式下启用）
  const touchStart = useRef<{ x: number; y: number } | null>(null)
  const swipe =
    touch && onBack
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
      <Box className="w-scroll-wrap">
        <Box ref={scroller} className="w-scroll" onScroll={onScroll} {...swipe}>
          <Stack gap={0} p="md" justify={!id ? "flex-end" : undefined} mih="100%">
            {!id && pending.length === 0 && intro}
            {list.map((it, i) => {
              const prev = list[i - 1]
              const next = list[i + 1]
              const showDay = i === 0 || !sameDay(prev.created_at, it.created_at)
              const groupedWithPrev = !showDay && canGroup(prev, it)
              const groupedWithNext = Boolean(next) && canGroup(it, next)
              const gapTop = i === 0 ? 0 : groupedWithPrev ? 4 : 10
              return (
                <Fragment key={it.id}>
                  {showDay && (
                    <Text size="xs" c="dimmed" ta="center" my={i === 0 ? 4 : 8}>
                      {dayLabel(it.created_at, language, t)}
                    </Text>
                  )}
                  {it.prompt?.kind === "collect_contact" ? (
                    <Box mt={gapTop}>
                      <CollectEmailCard conversationId={id} color={color} />
                    </Box>
                  ) : (
                    <Bubble
                      mine={it.author.type === "contact"}
                      author={!groupedWithPrev ? (it.author.type === "member" ? it.author.name : it.author.type === "system" ? name : undefined) : undefined}
                      text={promptText(it.prompt, it.text, language, t)}
                      attachments={it.attachments}
                      meta={!groupedWithNext ? clock(it.created_at, language) : undefined}
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
            {closed && conversation.data && (
              <>
                <Divider label={t("chat.ended")} labelPosition="center" my="sm" />
                {(conversation.data.rating_requested || conversation.data.rating) && (
                  <RatingCard conversation={conversation.data} color={color} />
                )}
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
      <Composer
        color={color}
        touch={touch}
        placeholder={t(closed ? "composer.reopen_placeholder" : "composer.placeholder")}
        onSend={(text, inputs, attachments) => void send({ clientId: newClientId(), text, inputs, attachments, failed: false })}
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
  meta?: ReactNode
  color: string
  first?: boolean
  last?: boolean
  mt?: number
  state?: "sending" | "failed"
  onPreview: (image: LightboxImage) => void
}): ReactNode {
  const fg = useBrandForeground()
  const mediaOnly = !text && Boolean(attachments?.length) && attachments!.every((a) => a.kind === "image")
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
      </Box>
      {meta && (
        <Text size="xs" c={state === "failed" ? "red" : "dimmed"} mx={4} mt={2} component="div" style={{ fontVariantNumeric: "tabular-nums" }}>
          {meta}
        </Text>
      )}
    </Box>
  )
}
