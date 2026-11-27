import type { ConversationDTO, ConversationItemDTO } from "@agents-desk/client/tenant"
import { ActionIcon, Box, Divider, Group, Loader, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconCornerUpLeft, IconPencil } from "@tabler/icons-react"
import { ImageLightbox, notifyError, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import type { LightboxImage } from "./Attachments"
import { ChannelChip } from "./channel-badge"
import type { TypingNow } from "./channel"
import { useMe, useMembers, useTeams, useTimeline } from "./data"
import { describeEvent, whoDid } from "./events"
import { type FillWatcher, useFillVisible } from "./fill"
import { clock, dayLabel, fullDateTime, sameDay } from "./format"
import { contactName } from "./List"
import {
  ActorAvatar,
  canGroupItems,
  deliveryColor,
  MessageBubble,
  TimelineNote,
  TranslationCaption,
  translationOf,
  TypingIndicator,
} from "./MessagePrimitives"
import { promptText } from "./prompts"
import type { MatchField } from "./search"
import { firstUnread, NewMessagesDivider, ScrollToBottomButton, useStickToBottom } from "./stick-to-bottom"
import { TimelineSkeleton } from "../components/detail-skeleton"

/**
 * Timeline 是会话的时间线：按天分隔；对方的消息在左侧，成员和系统发的在右侧并显示送达状态（失败时可以重发）；
 * 系统事件居中、灰色；内部备注占满一行、黄色底。打开时读到的位置 readSeq 之后显示“新消息”分隔线。
 * 新的内容出现时滚到底部；最下面是正在输入的人。
 * 非本人消息默认展示当前成员语言的译文，每条消息或备注可单独切换查看原文；
 * 成员翻译后发出的消息显示成员写的原文，可以展开发给对方的译文。
 */
/** isExternalActor 判断时间线项是否由外部对方（联系人或外部群发言人）发出。 */
const isExternalActor = (type: string) => type === "contact" || type === "external_user"

export function Timeline({
  c,
  readSeq,
  typing,
  highlight,
  focusSeq,
  focusField,
  onReplyTo,
  onCorrect,
}: {
  c: ConversationDTO
  readSeq: number
  typing: TypingNow[]
  highlight?: string
  focusSeq?: number
  focusField?: MatchField
  onReplyTo?: (item: ConversationItemDTO) => void
  onCorrect?: (item: ConversationItemDTO) => void
}): ReactNode {
  const t = useT()
  const language = useLanguage()
  const zone = useTimeZone()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const items = useTimeline(c.id, focusSeq)
  const translation = useQuery(kit.queries.translation.settings())
  const enabled = translation.data?.enabled ?? false
  const watch = useFillVisible(enabled, me.data?.language)
  const focusedRef = useRef<number | undefined>(undefined)
  const [pulseSeq, setPulseSeq] = useState<number | undefined>(undefined)
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)
  const list = items.data ?? []
  // 打开时落到底部；带 focusSeq（从搜索结果跳转）时让位给下面的定位滚动
  const stick = useStickToBottom({
    ready: !items.isPending,
    list,
    myId: me.data?.id,
    typingCount: typing.length,
    hold: Boolean(focusSeq && list.some((it) => it.seq === focusSeq)),
  })
  const scroller = stick.scroller
  const names = {
    member: (id: string) => (id === me.data?.id ? t("inbox.you") : (byId.get(id)?.name ?? t("event.someone"))),
    team: (id: string) => teams.find((x) => x.id === id)?.name ?? "",
  }

  // 从搜索结果跳转时，自动滚动定位到对应序号的消息气泡并施加短暂脉冲高亮
  useEffect(() => {
    if (!focusSeq || items.isPending || focusedRef.current === focusSeq) return
    const el = scroller.current
    if (!el) return
    const target = el.querySelector<HTMLElement>(`[data-seq="${focusSeq}"]`)
    if (!target) return
    focusedRef.current = focusSeq
    stick.settle()
    target.scrollIntoView({ block: "center", behavior: "smooth" })
    setPulseSeq(focusSeq)
    const timer = setTimeout(() => setPulseSeq((cur) => (cur === focusSeq ? undefined : cur)), 2500)
    return () => clearTimeout(timer)
  }, [focusSeq, items.isPending, list])

  if (items.isPending) {
    return (
      <Box className="inbox-timeline" px="var(--tn-pane-px)" py="md">
        <TimelineSkeleton />
      </Box>
    )
  }

  const firstNew = firstUnread(list, readSeq, me.data?.id)
  const byItemId = new Map(list.map((item) => [item.id, item]))
  const jumpToItem = (targetId: string) => {
    const target = byItemId.get(targetId)
    const el = scroller.current?.querySelector<HTMLElement>(`[data-item-id="${targetId}"]`)
    if (!target || !el) return
    el.scrollIntoView({ block: "center", behavior: "smooth" })
    setPulseSeq(target.seq)
    setTimeout(() => setPulseSeq((cur) => (cur === target.seq ? undefined : cur)), 2500)
  }
  const sameChannelMsg = (a: ConversationItemDTO, b: ConversationItemDTO) =>
    a.kind === "message" && b.kind === "message" && a.channel?.id === b.channel?.id
  const memberOf = (memberId: string) => byId.get(memberId) ?? (memberId === me.data?.id ? me.data : undefined)
  const authorOf = (item: ConversationItemDTO) => {
    if (item.actor.type === "contact") return contactName(c, t)
    if (item.actor.type === "external_user") return item.actor.name || item.actor.external_id || t("inbox.visitor")
    if (item.actor.type === "member") return names.member(item.actor.id)
    return whoDid(item, byId, t)
  }

  return (
    <Box className="inbox-timeline-wrap">
      <Box ref={scroller} className="inbox-timeline" onScroll={stick.onScroll}>
        <Stack gap={0} px="var(--tn-pane-px)" py="md">
          {list.map((it, i) => {
            const prev = list[i - 1]
            const next = list[i + 1]
            const showDay = i === 0 || !sameDay(prev.created_at, it.created_at)
            const showNew = it === firstNew
            const groupedWithPrev = !showDay && !showNew && canGroupItems(prev, it, firstNew, sameChannelMsg)
            const groupedWithNext = Boolean(next) && canGroupItems(it, next, firstNew, sameChannelMsg)
            const gapTop = i === 0 ? 0 : groupedWithPrev ? 4 : 16
            const msgAuthor = authorOf(it)
            const quotedItem = it.reply_to_item_id ? byItemId.get(it.reply_to_item_id) : undefined
            const quote = quotedItem
              ? {
                  author: authorOf(quotedItem),
                  text: quotedItem.content.text || quotedItem.content.original || "…",
                }
              : undefined
            const correctedOrig = it.corrects_item_id ? byItemId.get(it.corrects_item_id) : undefined
            const correction = it.corrects_item_id
              ? {
                  text: correctedOrig ? (correctedOrig.content.original || correctedOrig.content.text || "…") : undefined,
                  onJump: correctedOrig ? () => jumpToItem(correctedOrig.id) : undefined,
                }
              : undefined
            const onViewCorrection =
              it.corrected_by && byItemId.has(it.corrected_by) ? () => jumpToItem(it.corrected_by!) : undefined
            return (
              <Fragment key={it.id}>
                {showDay && <Divider my={i === 0 ? 4 : 12} label={dayLabel(it.created_at, language, t, zone)} labelPosition="center" />}
                {showNew && <NewMessagesDivider />}
                {it.kind === "event" ? (
                  <Text size="xs" c="dimmed" ta="center" mt={gapTop} className="inbox-event">
                    {describeEvent(it, byId, names, t, language)} · {clock(it.created_at, language, zone)}
                  </Text>
                ) : it.kind === "note" ? (
                  <TimelineNote
                    conversationId={c.id}
                    it={it}
                    author={names.member(it.actor.id)}
                    avatar={<ActorAvatar actor={it.actor} size={18} member={memberOf(it.actor.id)} fallbackName={names.member(it.actor.id)} />}
                    isOwn={it.actor.type === "member" && it.actor.id === me.data?.id}
                    time={clock(it.created_at, language, zone)}
                    fullTime={fullDateTime(it.created_at, language, zone)}
                    mt={gapTop}
                    lang={me.data?.language}
                    translating={enabled}
                    watch={watch}
                    highlight={highlight}
                    focused={pulseSeq === it.seq}
                    onPreview={setLightbox}
                  />
                ) : (
                  <Message
                    it={it}
                    c={c}
                    mine={!isExternalActor(it.actor.type)}
                    isOwn={it.actor.type === "member" && it.actor.id === me.data?.id}
                    author={msgAuthor}
                    avatar={
                      <ActorAvatar
                        actor={it.actor}
                        size={32}
                        member={it.actor.type === "member" ? memberOf(it.actor.id) : undefined}
                        contact={c.contact}
                        fallbackName={msgAuthor}
                      />
                    }
                    time={clock(it.created_at, language, zone)}
                    fullTime={fullDateTime(it.created_at, language, zone)}
                    first={!groupedWithPrev}
                    last={!groupedWithNext}
                    mt={gapTop}
                    lang={me.data?.language}
                    translating={enabled}
                    watch={watch}
                    highlight={highlight}
                    focused={pulseSeq === it.seq}
                    preferOriginal={focusSeq === it.seq && focusField === "original"}
                    quote={quote}
                    correction={correction}
                    onViewCorrection={onViewCorrection}
                    onReplyTo={onReplyTo}
                    onCorrect={onCorrect}
                    onPreview={setLightbox}
                  />
                )}
              </Fragment>
            )
          })}
          <TypingIndicator names={typing.map((x) => (x.contact ? contactName(c, t) : (x.name ?? ""))).filter(Boolean)} />
        </Stack>
      </Box>
      <ScrollToBottomButton stick={stick} />
      <ImageLightbox src={lightbox?.src ?? null} filename={lightbox?.filename} onClose={() => setLightbox(null)} />
    </Box>
  )
}

/** MessageProps 是一条服务型会话消息的显示设置。 */
interface MessageProps {
  it: ConversationItemDTO
  c: ConversationDTO
  mine: boolean // 成员和系统发的，在右侧
  isOwn: boolean // 当前登录成员自己发出的消息
  author: string
  avatar: ReactNode
  time: string
  fullTime: string // 完整日期时间，悬停提示用
  first: boolean // 连续同人消息组的第一条：显示作者和时间头部
  last: boolean // 连续同人消息组的最后一条：己方消息在底部显示送达状态
  mt: number // 顶部间距（组内紧凑、跨组舒展）
  lang: string | undefined // 成员的语言：对方的消息显示这种语言的译文
  translating: boolean // 翻译可用：还缺我的语言的译文的消息显示“翻译中”，出现在页面上时请求补译
  watch: FillWatcher // 挂在还缺译文的消息上，见 useFillVisible
  highlight?: string // 当前搜索词：高亮气泡内命中的文本
  focused?: boolean // 从搜索结果跳转命中的当前消息，施加短暂脉冲高亮
  preferOriginal?: boolean // 命中客户消息原文时默认切到原文展示
  quote?: { author: string; text: string }
  correction?: { text?: string; onJump?: () => void }
  onViewCorrection?: () => void
  onReplyTo?: (item: ConversationItemDTO) => void
  onCorrect?: (item: ConversationItemDTO) => void
  onPreview: (image: LightboxImage) => void // 点击图片缩略图打开原图灯箱
}

/**
 * Message 渲染服务型会话中的单条消息气泡，支持跨渠道标识、译文切换、已发送译文展开以及送达状态与失败重发。
 */
function Message({
  it,
  c,
  mine,
  isOwn,
  author,
  avatar,
  time,
  fullTime,
  first,
  last,
  mt,
  lang,
  translating,
  watch,
  highlight,
  focused,
  preferOriginal,
  quote,
  correction,
  onViewCorrection,
  onReplyTo,
  onCorrect,
  onPreview,
}: MessageProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const [showOriginal, setShowOriginal] = useState(() => Boolean(preferOriginal))
  const [prevPrefer, setPrevPrefer] = useState(preferOriginal)
  if (prevPrefer !== preferOriginal) {
    setPrevPrefer(preferOriginal)
    if (preferOriginal) setShowOriginal(true)
  }
  const [sent, setSent] = useState(false)
  const queryClient = useQueryClient()
  const retry = useMutation({
    mutationFn: ({ skipTranslation }: { skipTranslation?: boolean } = {}) =>
      tenantApi.conversation.retry(c.id, it.id, skipTranslation ? { skip_translation: true } : {}),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
    onError: (err) => notifyError(err),
  })

  // 对方或其他同事的消息：有当前成员语言的译文时显示译文；成员翻译后发出的消息：原文来自 content.original，content.text 是发给对方的译文；
  // 系统发出的结构化提示：按成员的界面语言和时区渲染
  const text = it.content.prompt ? promptText(it.content.prompt, it.content.text ?? "", ui, t, zone) : (it.content.text ?? "")
  const translatableForViewer = isExternalActor(it.actor.type) || (it.actor.type === "member" && !isOwn)
  const translated = translatableForViewer ? translationOf(it, lang) : undefined
  const sentAs = mine && it.content.original ? it.content.language : undefined
  const shown = translated && !showOriginal ? translated : sentAs ? (it.content.original ?? "") : text
  const from = it.derived.language as string | undefined
  const needFill = translatableForViewer && translating && it.untranslated

  const hasCaption = Boolean(translated || needFill || sentAs)
  const caption = hasCaption ? (
    <TranslationCaption
      translated={translated}
      needFill={needFill}
      fromLang={from}
      showOriginal={showOriginal}
      onToggleOriginal={() => setShowOriginal((v) => !v)}
      sentAs={sentAs}
      showSent={sent}
      onToggleSent={() => setSent((v) => !v)}
    />
  ) : undefined

  // 己方消息在组尾展示送达状态；发送中或发送失败时无论是否组尾都立即展示
  const pendingAutoTranslate = Boolean(it.delivery === "sending" && it.derived?.pending_translation)
  const translationFailed = Boolean(it.delivery === "failed" && it.derived?.translation_failed)
  const showDelivery = Boolean(mine && it.delivery && (last || it.delivery === "failed" || it.delivery === "sending"))
  const failedLabel = (
    <Text span size="xs" c={it.delivery ? deliveryColor[it.delivery] : undefined}>
      {translationFailed ? t("inbox.delivery_translation_failed") : it.delivery ? t(`inbox.delivery_${it.delivery}` as const) : ""}
    </Text>
  )
  const deliveryNode =
    showDelivery && it.delivery ? (
      <>
        {pendingAutoTranslate && <Loader size={10} />}
        {it.delivery === "failed" && it.delivery_error ? (
          <Tooltip label={it.delivery_error} multiline maw={280} openDelay={200}>
            {failedLabel}
          </Tooltip>
        ) : (
          <Text span size="xs" c={deliveryColor[it.delivery]}>
            {pendingAutoTranslate ? t("inbox.delivery_translating") : t(`inbox.delivery_${it.delivery}` as const)}
          </Text>
        )}
        {it.delivery === "failed" && translationFailed ? (
          <>
            <Text span size="xs" c="dimmed">
              ·
            </Text>
            <UnstyledButton fz="xs" c="red" td="underline" onClick={() => retry.mutate({})} disabled={retry.isPending}>
              {t("inbox.retry_translate")}
            </UnstyledButton>
            <Text span size="xs" c="dimmed">
              ·
            </Text>
            <UnstyledButton fz="xs" c="red" td="underline" onClick={() => retry.mutate({ skipTranslation: true })} disabled={retry.isPending}>
              {t("inbox.retry_original")}
            </UnstyledButton>
          </>
        ) : it.delivery === "failed" ? (
          <>
            <Text span size="xs" c="dimmed">
              ·
            </Text>
            <UnstyledButton fz="xs" c="red" td="underline" onClick={() => retry.mutate({})} disabled={retry.isPending}>
              {t("inbox.retry")}
            </UnstyledButton>
          </>
        ) : null}
      </>
    ) : undefined

  // 跨渠道会话（或该条消息的渠道与会话来源渠道不同）在消息头部展示渠道胶囊，便于识别每条消息的来去管道
  const showChannelChip = Boolean(it.channel && ((c.channels?.length ?? 0) > 1 || (c.channel && it.channel.id !== c.channel.id)))

  // 发给对方的成员或机器人消息支持发送更正，不修改原消息
  const canCorrect = Boolean(onCorrect && (it.actor.type === "member" || it.actor.type === "bot"))

  return (
    <MessageBubble
      conversationId={c.id}
      itemId={it.id}
      attachments={it.attachments}
      seq={it.seq}
      watchRef={needFill ? watch(it.id) : undefined}
      mine={mine}
      first={first}
      last={last}
      mt={mt}
      author={author}
      avatar={avatar}
      headerExtra={showChannelChip && it.channel ? <ChannelChip channel={it.channel} /> : undefined}
      corrected={Boolean(it.corrected_by)}
      onViewCorrection={onViewCorrection}
      correction={correction}
      time={time}
      fullTime={fullTime}
      text={shown || undefined}
      highlight={highlight}
      focused={focused}
      original={Boolean(translated && showOriginal)}
      expandedTranslation={sentAs && sent ? { language: sentAs, text: it.content.text ?? "" } : undefined}
      quote={quote}
      extraActions={
        onReplyTo || canCorrect ? (
          <Group gap={2} wrap="nowrap">
            {canCorrect && onCorrect && (
              <Tooltip label={t("inbox.message_correct")} openDelay={250}>
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size={22}
                  onClick={() => onCorrect(it)}
                  aria-label={t("inbox.message_correct")}
                >
                  <IconPencil size={14} />
                </ActionIcon>
              </Tooltip>
            )}
            {onReplyTo && (
              <Tooltip label={t("inbox.reply_to_message")} openDelay={250}>
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size={22}
                  onClick={() => onReplyTo(it)}
                  aria-label={t("inbox.reply_to_message")}
                >
                  <IconCornerUpLeft size={14} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
        ) : undefined
      }
      caption={caption}
      footerExtra={deliveryNode}
      onPreview={onPreview}
    />
  )
}
