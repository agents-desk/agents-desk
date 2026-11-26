import type { ConversationDTO, ConversationItemDTO } from "@agents-desk/client/tenant"
import { ActionIcon, Anchor, Box, Button, CopyButton, Divider, Group, Loader, Paper, Skeleton, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconArrowDown, IconCheck, IconCopy, IconLanguage, IconLock } from "@tabler/icons-react"
import { ImageLightbox, notifyError, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName, sameLanguage } from "../languages"
import { type LightboxImage, MessageAttachments } from "./Attachments"
import { ChannelChip } from "./channel-badge"
import type { TypingNow } from "./channel"
import { useMe, useMembers, useTeams, useTimeline } from "./data"
import { type FillWatcher, useFillVisible } from "./fill"
import { describeEvent, whoDid } from "./events"
import { clock, dayLabel, fullDateTime, sameDay } from "./format"
import { HighlightQuery } from "./Highlight"
import { contactName } from "./List"
import { promptText } from "./prompts"
import type { MatchField } from "./search"

/** GROUP_WINDOW_MS 是同一发送者连续消息合并为同一组的间隔上限（3 分钟）。 */
const GROUP_WINDOW_MS = 3 * 60 * 1000

/** canGroup 判断相邻两条时间线项是否可合并为同人同渠道连续消息组。 */
function canGroup(prev: ConversationItemDTO | undefined, cur: ConversationItemDTO, firstNew: ConversationItemDTO | undefined): boolean {
  if (!prev || prev.kind !== "message" || cur.kind !== "message") return false
  if (cur === firstNew) return false
  if (prev.actor.type !== cur.actor.type || prev.actor.id !== cur.actor.id) return false
  if (prev.channel?.id !== cur.channel?.id) return false
  if (!sameDay(prev.created_at, cur.created_at)) return false
  return Math.abs(new Date(cur.created_at).getTime() - new Date(prev.created_at).getTime()) <= GROUP_WINDOW_MS
}

/** deliveryColor 是送达状态的颜色：已读是进行中的颜色，失败是红色，其余是灰色。 */
const deliveryColor = { sending: "dimmed", delivered: "dimmed", read: "bright-blue", failed: "red" } as const

/** translationOf 返回对方的消息在成员的语言 lang 中的译文；没有（还没翻译好，或者和成员是同一种语言）时为空。 */
export function translationOf(it: ConversationItemDTO, lang: string | undefined): string | undefined {
  const all = it.derived.translations as Record<string, string> | undefined
  return lang ? all?.[lang] : undefined
}

/** hasTranslation 返回时间线中是否有非本人发出的消息或内部备注带着成员语言 lang 的译文：有时会话顶部显示“译文 | 原文”的切换。 */
export function hasTranslation(items: ConversationItemDTO[] | undefined, lang: string | undefined, myId?: string): boolean {
  return !!items?.some(
    (it) =>
      (it.actor.type === "contact" || (it.actor.type === "member" && it.actor.id !== myId)) &&
      Boolean(translationOf(it, lang)),
  )
}

/**
 * Timeline 是会话的时间线：按天分隔；对方的消息在左侧，成员和系统发的在右侧并显示送达状态（失败时可以重发）；
 * 系统事件居中、灰色；内部备注占满一行、黄色底。打开时读到的位置 readSeq 之后显示“新消息”分隔线。
 * 新的内容出现时滚到底部；最下面是正在输入的人。
 * 对方的消息显示成员语言的译文，original 为真时（顶部选了“原文”）显示原文，每条也可以单独切换；
 * 成员翻译后发出的消息显示成员写的原文，可以展开发给对方的译文。
 */
export function Timeline({
  c,
  readSeq,
  typing,
  original,
  highlight,
  focusSeq,
  focusField,
}: {
  c: ConversationDTO
  readSeq: number
  typing: TypingNow[]
  original: boolean
  highlight?: string
  focusSeq?: number
  focusField?: MatchField
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
  const scroller = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true) // 视口距离底部 <= 80px 时认为在底部，允许自动跟随新消息
  const initialized = useRef(false)
  const prevCount = useRef(0)
  const focusedRef = useRef<number | undefined>(undefined)
  const [pulseSeq, setPulseSeq] = useState<number | undefined>(undefined)
  const [showScrollDown, setShowScrollDown] = useState(false)
  const [unseenCount, setUnseenCount] = useState(0)
  const [lightbox, setLightbox] = useState<LightboxImage | null>(null)
  const list = items.data ?? []
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
    initialized.current = true
    prevCount.current = list.length
    target.scrollIntoView({ block: "center", behavior: "smooth" })
    setPulseSeq(focusSeq)
    const timer = setTimeout(() => setPulseSeq((cur) => (cur === focusSeq ? undefined : cur)), 2500)
    return () => clearTimeout(timer)
  }, [focusSeq, items.isPending, list])

  // 首次加载完成直接落到底部（若本次带有 focusSeq 则让位给定位滚动）；后续新增消息仅在底部附近或自己刚发送时自动跟随
  useEffect(() => {
    if (items.isPending) return
    const el = scroller.current
    if (!el) return
    if (!initialized.current) {
      if (focusSeq && list.some((it) => it.seq === focusSeq)) return
      initialized.current = true
      prevCount.current = list.length
      el.scrollTop = el.scrollHeight
      nearBottom.current = true
      return
    }
    if (list.length > prevCount.current) {
      const added = list.slice(prevCount.current)
      prevCount.current = list.length
      const hasOwn = added.some((it) => it.actor.type === "member" && it.actor.id === me.data?.id)
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
  }, [focusSeq, items.isPending, list, me.data?.id])

  // 正在输入提示出现时，仅在用户停留在底部附近时跟随滚动
  useEffect(() => {
    if (!initialized.current || typing.length === 0 || !nearBottom.current) return
    const el = scroller.current
    el?.scrollTo({ top: el.scrollHeight, behavior: "smooth" })
  }, [typing.length])

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

  if (items.isPending) {
    return (
      <Box className="inbox-timeline" px="var(--tn-pane-px)" py="md">
        <Stack gap="md">
          <Box style={{ alignSelf: "flex-start", width: "54%", maxWidth: 360 }}>
            <Skeleton h={10} w={88} mb={6} radius="sm" />
            <Skeleton h={52} radius="md" />
          </Box>
          <Box style={{ alignSelf: "flex-end", width: "46%", maxWidth: 320 }}>
            <Skeleton h={10} w={72} ml="auto" mb={6} radius="sm" />
            <Skeleton h={42} radius="md" />
          </Box>
          <Box style={{ alignSelf: "flex-start", width: "60%", maxWidth: 400 }}>
            <Skeleton h={10} w={96} mb={6} radius="sm" />
            <Skeleton h={64} radius="md" />
          </Box>
          <Box style={{ alignSelf: "flex-end", width: "38%", maxWidth: 260 }}>
            <Skeleton h={10} w={64} ml="auto" mb={6} radius="sm" />
            <Skeleton h={38} radius="md" />
          </Box>
        </Stack>
      </Box>
    )
  }
  const firstNew = list.find((it) => it.seq > readSeq && !(it.actor.type === "member" && it.actor.id === me.data?.id))
  return (
    <Box className="inbox-timeline-wrap">
      <Box ref={scroller} className="inbox-timeline" onScroll={onScroll}>
        <Stack gap={0} px="var(--tn-pane-px)" py="md">
          {list.map((it, i) => {
            const prev = list[i - 1]
            const next = list[i + 1]
            const showDay = i === 0 || !sameDay(prev.created_at, it.created_at)
            const showNew = readSeq > 0 && it === firstNew
            const groupedWithPrev = !showDay && !showNew && canGroup(prev, it, firstNew)
            const groupedWithNext = Boolean(next) && canGroup(it, next, firstNew)
            const gapTop = i === 0 ? 0 : groupedWithPrev ? 4 : 16
            return (
              <Fragment key={it.id}>
                {showDay && <Divider my={i === 0 ? 4 : 12} label={dayLabel(it.created_at, language, t, zone)} labelPosition="center" />}
                {showNew && <Divider my={12} color="red.3" label={<Text size="xs" c="red">{t("inbox.new_messages")}</Text>} labelPosition="center" />}
                {it.kind === "event" ? (
                  <Text size="xs" c="dimmed" ta="center" mt={gapTop} className="inbox-event">
                    {describeEvent(it, byId, names, t, language)} · {clock(it.created_at, language, zone)}
                  </Text>
                ) : it.kind === "note" ? (
                  <Note
                    conversationId={c.id}
                    it={it}
                    author={names.member(it.actor.id)}
                    isOwn={it.actor.type === "member" && it.actor.id === me.data?.id}
                    time={clock(it.created_at, language, zone)}
                    fullTime={fullDateTime(it.created_at, language, zone)}
                    mt={gapTop}
                    lang={me.data?.language}
                    original={original}
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
                    mine={it.actor.type !== "contact"}
                    isOwn={it.actor.type === "member" && it.actor.id === me.data?.id}
                    author={it.actor.type === "contact" ? contactName(c, t) : it.actor.type === "member" ? names.member(it.actor.id) : whoDid(it, byId, t)}
                    time={clock(it.created_at, language, zone)}
                    fullTime={fullDateTime(it.created_at, language, zone)}
                    first={!groupedWithPrev}
                    last={!groupedWithNext}
                    mt={gapTop}
                    lang={me.data?.language}
                    original={original}
                    translating={enabled}
                    watch={watch}
                    highlight={highlight}
                    focused={pulseSeq === it.seq}
                    preferOriginal={focusSeq === it.seq && focusField === "original"}
                    onPreview={setLightbox}
                  />
                )}
              </Fragment>
            )
          })}
          {typing.length > 0 && (
            <Box mt={10} className="inbox-typing">
              <span className="inbox-typing-dots" aria-hidden="true">
                <span />
                <span />
                <span />
              </span>
              <Text size="xs" c="dimmed">
                {t("inbox.typing", { who: typing.map((x) => (x.contact ? contactName(c, t) : (x.name ?? ""))).join(", ") })}
              </Text>
            </Box>
          )}
        </Stack>
      </Box>
      {showScrollDown && (
        <Button
          size="compact-xs"
          radius="xl"
          variant={unseenCount > 0 ? "filled" : "default"}
          leftSection={<IconArrowDown size={13} />}
          className="inbox-scroll-bottom"
          onClick={scrollToBottom}
        >
          {unseenCount > 0 ? t("inbox.new_message_jump", { n: unseenCount }) : t("inbox.scroll_to_bottom")}
        </Button>
      )}
      <ImageLightbox src={lightbox?.src ?? null} filename={lightbox?.filename} onClose={() => setLightbox(null)} />
    </Box>
  )
}

/** MessageProps 是一条消息的显示设置。 */
interface MessageProps {
  it: ConversationItemDTO
  c: ConversationDTO
  mine: boolean // 成员和系统发的，在右侧
  isOwn: boolean // 当前登录成员自己发出的消息
  author: string
  time: string
  fullTime: string // 完整日期时间，悬停提示用
  first: boolean // 连续同人消息组的第一条：显示作者和时间头部
  last: boolean // 连续同人消息组的最后一条：己方消息在底部显示送达状态
  mt: number // 顶部间距（组内紧凑、跨组舒展）
  lang: string | undefined // 成员的语言：对方的消息显示这种语言的译文
  original: boolean // 顶部选了“原文”：对方的消息都显示原文
  translating: boolean // 翻译可用：还缺我的语言的译文的消息显示“翻译中”，出现在页面上时请求补译
  watch: FillWatcher // 挂在还缺译文的消息上，见 useFillVisible
  highlight?: string // 当前搜索词：高亮气泡内命中的文本
  focused?: boolean // 从搜索结果跳转命中的当前消息，施加短暂脉冲高亮
  preferOriginal?: boolean // 命中客户消息原文时默认切到原文展示
  onPreview: (image: LightboxImage) => void // 点击图片缩略图打开原图灯箱
}

/**
 * Message 是一条消息：对方的在左侧，成员和系统发的在右侧，旁边是悬浮复制按钮，底部是译文切换与送达状态（失败时可以重发）。
 * 连续同人消息合并为组：组首显示作者与时间，组内消息省略重复头部并用方向圆角自然衔接。
 */
function Message({ it, c, mine, isOwn, author, time, fullTime, first, last, mt, lang, original, translating, watch, highlight, focused, preferOriginal, onPreview }: MessageProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const [prevOriginal, setPrevOriginal] = useState(original)
  const [flip, setFlip] = useState(() => Boolean(preferOriginal && !original)) // 这一条和顶部的“显示原文”相反；顶部切换时重置
  if (prevOriginal !== original) {
    setPrevOriginal(original)
    setFlip(false)
  }
  const [prevPrefer, setPrevPrefer] = useState(preferOriginal)
  if (prevPrefer !== preferOriginal) {
    setPrevPrefer(preferOriginal)
    if (preferOriginal && !original) setFlip(true)
  }
  const [sent, setSent] = useState(false) // 展开发给对方的译文
  const queryClient = useQueryClient()
  const retry = useMutation({
    mutationFn: () => tenantApi.conversation.retry(c.id, it.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
    onError: (err) => notifyError(err),
  })

  // 对方或其他同事的消息：有当前成员语言的译文时显示译文；成员翻译后发出的消息：原文来自 content.original，content.text 是发给对方的译文；
  // 系统发出的结构化提示：按成员的界面语言和时区渲染
  const text = it.content.prompt ? promptText(it.content.prompt, it.content.text ?? "", ui, t, zone) : (it.content.text ?? "")
  const translatableForViewer = it.actor.type === "contact" || (it.actor.type === "member" && !isOwn)
  const translated = translatableForViewer ? translationOf(it, lang) : undefined
  const showOriginal = original !== flip
  const sentAs = mine && it.content.original ? it.content.language : undefined
  const shown = translated && !showOriginal ? translated : sentAs ? (it.content.original ?? "") : text
  const from = it.derived.language as string | undefined
  const needFill = translatableForViewer && translating && it.untranslated
  let caption: ReactNode = null
  if (translated) {
    // 始终只保留单个译文/原文切换按钮；单条消息语言与会话主语言不同时附带语言名。
    const showFromLang = Boolean(from && (!sameLanguage(from, c.language) || mine))
    caption = (
      <>
        <IconLanguage size={12} className="inbox-caption-icon" />
        {showFromLang && from && (
          <>
            <Text size="xs" c="dimmed">
              {languageName(from, ui)}
            </Text>
            <Text size="xs" c="dimmed">
              ·
            </Text>
          </>
        )}
        <CaptionToggle label={t(showOriginal ? "inbox.view_translation" : "inbox.view_original")} onClick={() => setFlip((f) => !f)} />
      </>
    )
  } else if (needFill) {
    caption = (
      <>
        <Loader size={10} />
        <Text size="xs" c="dimmed">
          {t("inbox.translating")}
        </Text>
      </>
    )
  } else if (sentAs) {
    const showTargetLang = !sameLanguage(sentAs, c.language)
    caption = (
      <>
        {showTargetLang && (
          <>
            <Text size="xs" c="dimmed">
              {t("inbox.sent_as", { lang: languageName(sentAs, ui) })}
            </Text>
            <Text size="xs" c="dimmed">
              ·
            </Text>
          </>
        )}
        <CaptionToggle label={t(sent ? "inbox.hide_translation" : "inbox.view_translation")} onClick={() => setSent((v) => !v)} />
      </>
    )
  }

  // 己方消息在组尾展示送达状态；发送中或发送失败时无论是否组尾都立即展示
  const showDelivery = Boolean(mine && it.delivery && (last || it.delivery === "failed" || it.delivery === "sending"))
  const deliveryNode = showDelivery && it.delivery && (
    <>
      <Text span size="xs" c={deliveryColor[it.delivery]}>
        {t(`inbox.delivery_${it.delivery}` as const)}
      </Text>
      {it.delivery === "failed" && (
        <>
          <Text span size="xs" c="dimmed">
            ·
          </Text>
          <UnstyledButton fz="xs" c="red" td="underline" onClick={() => retry.mutate()} disabled={retry.isPending}>
            {t("inbox.retry")}
          </UnstyledButton>
        </>
      )}
    </>
  )

  // 跨渠道会话（或该条消息的渠道与会话来源渠道不同）在消息头部展示渠道胶囊，便于识别每条消息的来去管道
  const showChannelChip = Boolean(it.channel && ((c.channels?.length ?? 0) > 1 || (c.channel && it.channel.id !== c.channel.id)))

  return (
    <Box
      mt={mt}
      data-seq={it.seq}
      className="inbox-msg"
      style={{ alignSelf: mine ? "flex-end" : "flex-start", maxWidth: "min(72%, 620px)" }}
      ref={needFill ? watch(it.id) : undefined}
    >
      {first && (
        <Group gap={5} mb={3} px={4} justify={mine ? "flex-end" : "flex-start"} wrap="nowrap">
          <Text size="xs" c="dimmed">
            {author}
          </Text>
          {showChannelChip && it.channel && <ChannelChip channel={it.channel} />}
          <Text size="xs" c="dimmed">
            ·
          </Text>
          <Tooltip label={fullTime} openDelay={300}>
            <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
              {time}
            </Text>
          </Tooltip>
        </Group>
      )}
      <Group gap={6} wrap="nowrap" align="center" style={{ flexDirection: mine ? "row-reverse" : "row" }}>
        <Paper
          px={shown || sent ? 12 : 6}
          py={shown || sent ? 9 : 6}
          withBorder={!mine}
          bg={mine ? "var(--mantine-primary-color-light)" : undefined}
          className={focused ? "inbox-bubble inbox-focus-pulse" : "inbox-bubble"}
          data-mine={mine || undefined}
          data-incoming={!mine || undefined}
          data-first={first || undefined}
          data-last={last || undefined}
          data-original={(translated && showOriginal) || undefined}
          style={{ minWidth: 0 }}
        >
          {shown && <Linkified text={shown} highlight={highlight} />}
          <MessageAttachments conversationId={c.id} attachments={it.attachments} hasText={Boolean(shown)} onPreview={onPreview} />
          {sentAs && sent && (
            <Box mt={6} pt={6} className="inbox-sent-translation">
              <Text size="xs" c="dimmed">
                {t("inbox.sent_translation", { lang: languageName(sentAs, ui) })}
              </Text>
              <Linkified text={it.content.text ?? ""} highlight={highlight} />
            </Box>
          )}
        </Paper>
        <Group gap={4} wrap="nowrap" className="inbox-msg-actions" style={{ flex: "none" }}>
          {!first && (
            <Tooltip label={fullTime} openDelay={300}>
              <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                {time}
              </Text>
            </Tooltip>
          )}
          {shown && <CopyAction value={shown} />}
        </Group>
      </Group>
      {(caption || deliveryNode) && (
        <Group gap={4} mt={3} px={6} justify={mine ? "flex-end" : "flex-start"} wrap="wrap">
          {caption}
          {caption && deliveryNode && (
            <Text size="xs" c="dimmed">
              ·
            </Text>
          )}
          {deliveryNode}
        </Group>
      )}
    </Box>
  )
}

/** CopyAction 是消息或备注旁的轻量一键复制按钮，复制后短暂显示绿色对勾。 */
function CopyAction({ value }: { value: string }): ReactNode {
  const t = useT()
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={t(copied ? "inbox.copied" : "inbox.copy_message")} openDelay={250}>
          <ActionIcon
            variant="subtle"
            color={copied ? "green" : "gray"}
            size={22}
            onClick={copy}
            aria-label={t("inbox.copy_message")}
          >
            {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  )
}

/** CaptionToggle 是消息下方的轻量译文切换按钮，默认柔和灰字，悬停时浮现品牌色。 */
function CaptionToggle({ label, onClick }: { label: string; onClick: () => void }): ReactNode {
  return (
    <UnstyledButton className="inbox-caption-btn" onClick={onClick}>
      {label}
    </UnstyledButton>
  )
}

/** Note 是内部备注：占满一行、黄色底加左侧强调条与锁图标，@ 到的同事加粗。只有成员可见，支持跨语言译文与原文切换。 */
function Note({
  conversationId,
  it,
  author,
  isOwn,
  time,
  fullTime,
  mt,
  lang,
  original,
  translating,
  watch,
  highlight,
  focused,
  onPreview,
}: {
  conversationId: string
  it: ConversationItemDTO
  author: string
  isOwn: boolean
  time: string
  fullTime: string
  mt: number
  lang: string | undefined
  original: boolean
  translating: boolean
  watch: FillWatcher
  highlight?: string
  focused?: boolean
  onPreview: (image: LightboxImage) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [prevOriginal, setPrevOriginal] = useState(original)
  const [flip, setFlip] = useState(false)
  if (prevOriginal !== original) {
    setPrevOriginal(original)
    setFlip(false)
  }
  const raw = it.content.text ?? ""
  const translated = isOwn ? undefined : translationOf(it, lang)
  const showOriginal = original !== flip
  const shown = translated && !showOriginal ? translated : raw
  const from = it.derived.language as string | undefined
  const needFill = !isOwn && translating && it.untranslated
  return (
    <Paper
      ref={needFill ? watch(it.id) : undefined}
      mt={mt}
      px="sm"
      py={8}
      radius="md"
      bg="var(--mantine-color-yellow-light)"
      data-seq={it.seq}
      className={focused ? "inbox-note inbox-focus-pulse" : "inbox-note"}
    >
      <Group gap={6} mb={3} wrap="nowrap">
        <IconLock size={12} className="inbox-note-accent" style={{ flex: "none" }} />
        <Text size="xs" fw={600} className="inbox-note-accent">
          {t("inbox.note_by", { who: author })}
        </Text>
        <Text size="xs" className="inbox-note-accent" opacity={0.75}>
          ·
        </Text>
        <Tooltip label={fullTime} openDelay={300}>
          <Text size="xs" className="inbox-note-accent" opacity={0.85} style={{ fontVariantNumeric: "tabular-nums" }}>
            {time}
          </Text>
        </Tooltip>
        {shown && (
          <Box ml="auto" className="inbox-msg-actions">
            <CopyAction value={shown} />
          </Box>
        )}
      </Group>
      {shown && (
        <Text size="md" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
          {shown.split(/(@\S+)/).map((part, i) =>
            part.startsWith("@") ? (
              // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的片段没有别的标识，顺序固定
              <Text key={i} span fw={700} className="inbox-note-accent">
                <HighlightQuery text={part} query={highlight} />
              </Text>
            ) : (
              // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的片段没有别的标识，顺序固定
              <Fragment key={i}>
                <HighlightQuery text={part} query={highlight} />
              </Fragment>
            ),
          )}
        </Text>
      )}
      <MessageAttachments conversationId={conversationId} attachments={it.attachments} hasText={Boolean(shown)} onPreview={onPreview} />
      {(translated || needFill) && (
        <Group gap={4} mt={4} wrap="wrap">
          {translated ? (
            <>
              <IconLanguage size={12} className="inbox-caption-icon" />
              {from && (
                <>
                  <Text size="xs" c="dimmed">
                    {languageName(from, ui)}
                  </Text>
                  <Text size="xs" c="dimmed">
                    ·
                  </Text>
                </>
              )}
              <CaptionToggle label={t(showOriginal ? "inbox.view_translation" : "inbox.view_original")} onClick={() => setFlip((f) => !f)} />
            </>
          ) : (
            <>
              <Loader size={10} />
              <Text size="xs" c="dimmed">
                {t("inbox.translating")}
              </Text>
            </>
          )}
        </Group>
      )}
    </Paper>
  )
}

/** LINK 匹配消息中的网址：http:// 或 https:// 开头，到空白为止，去掉结尾的标点。 */
const LINK = /https?:\/\/[^\s<>\x22]+[^\s<>\x22\x27.,;:!?)\]}。，；：！？）】」』]/g

/** Linkified 显示消息文字，其中的网址是在新标签页中打开的链接，同时保留搜索词高亮。 */
function Linkified({ text, highlight }: { text: string; highlight?: string }): ReactNode {
  const parts: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(LINK)) {
    const at = m.index ?? 0
    if (at > last) {
      parts.push(
        <Fragment key={`t:${last}`}>
          <HighlightQuery text={text.slice(last, at)} query={highlight} />
        </Fragment>,
      )
    }
    parts.push(
      <Anchor key={`a:${at}`} href={m[0]} target="_blank" rel="noopener noreferrer" size="md">
        <HighlightQuery text={m[0]} query={highlight} />
      </Anchor>,
    )
    last = at + m[0].length
  }
  if (last < text.length) {
    parts.push(
      <Fragment key={`t:${last}`}>
        <HighlightQuery text={text.slice(last)} query={highlight} />
      </Fragment>,
    )
  }
  return (
    <Text size="md" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
      {parts}
    </Text>
  )
}
