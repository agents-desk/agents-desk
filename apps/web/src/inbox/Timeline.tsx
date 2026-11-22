import type { ConversationDTO, ConversationItemDTO } from "@agents-desk/client/tenant"
import { Anchor, Box, Center, Divider, Group, Loader, Paper, Stack, Text, UnstyledButton } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { notifyError, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName } from "../languages"
import type { TypingNow } from "./channel"
import { useMe, useMembers, useTeams, useTimeline } from "./data"
import { type FillWatcher, useFillVisible } from "./fill"
import { describeEvent, whoDid } from "./events"
import { clock, dayLabel, sameDay } from "./format"
import { contactName } from "./List"
import { promptText } from "./prompts"

/** deliveryColor 是送达状态的颜色：已读是进行中的颜色，失败是红色，其余是灰色。 */
const deliveryColor = { sending: "dimmed", delivered: "dimmed", read: "bright-blue", failed: "red" } as const

/** translationOf 返回对方的消息在成员的语言 lang 中的译文；没有（还没翻译好，或者和成员是同一种语言）时为空。 */
export function translationOf(it: ConversationItemDTO, lang: string | undefined): string | undefined {
  const all = it.derived.translations as Record<string, string> | undefined
  return lang ? all?.[lang] : undefined
}

/** hasTranslation 返回时间线中是否有对方的消息带着成员语言 lang 的译文：有时会话顶部显示“译文 | 原文”的切换。 */
export function hasTranslation(items: ConversationItemDTO[] | undefined, lang: string | undefined): boolean {
  return !!items?.some((it) => it.actor.type === "contact" && translationOf(it, lang))
}

/**
 * Timeline 是会话的时间线：按天分隔；对方的消息在左侧，成员和系统发的在右侧并显示送达状态（失败时可以重发）；
 * 系统事件居中、灰色；内部备注占满一行、黄色底。打开时读到的位置 readSeq 之后显示“新消息”分隔线。
 * 新的内容出现时滚到底部；最下面是正在输入的人。
 * 对方的消息显示成员语言的译文，original 为真时（顶部选了“原文”）显示原文，每条也可以单独切换；
 * 成员翻译后发出的消息显示成员写的原文，可以展开发给对方的译文。
 */
export function Timeline({ c, readSeq, typing, original }: { c: ConversationDTO; readSeq: number; typing: TypingNow[]; original: boolean }): ReactNode {
  const t = useT()
  const language = useLanguage()
  const zone = useTimeZone()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const items = useTimeline(c.id)
  const translation = useQuery(kit.queries.translation.settings())
  const enabled = translation.data?.enabled ?? false
  const watch = useFillVisible(enabled, me.data?.language)
  const bottom = useRef<HTMLDivElement>(null)
  const list = items.data ?? []
  const names = {
    member: (id: string) => (id === me.data?.id ? t("inbox.you") : (byId.get(id)?.name ?? t("event.someone"))),
    team: (id: string) => teams.find((x) => x.id === id)?.name ?? "",
  }

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" })
  }, [list.length, typing.length])

  if (items.isPending) {
    return (
      <Center style={{ flex: 1 }}>
        <Loader size="sm" />
      </Center>
    )
  }
  const firstNew = list.find((it) => it.seq > readSeq && !(it.actor.type === "member" && it.actor.id === me.data?.id))
  return (
    <Box className="inbox-timeline">
      <Stack gap={10} p="md">
        {list.map((it, i) => (
          <Fragment key={it.id}>
            {(i === 0 || !sameDay(list[i - 1].created_at, it.created_at)) && (
              <Divider label={dayLabel(it.created_at, language, t, zone)} labelPosition="center" />
            )}
            {readSeq > 0 && it === firstNew && <Divider color="red.3" label={<Text size="xs" c="red">{t("inbox.new_messages")}</Text>} labelPosition="center" />}
            {it.kind === "event" ? (
              <Text size="xs" c="dimmed" ta="center" className="inbox-event">
                {describeEvent(it, byId, names, t, language)} · {clock(it.created_at, language, zone)}
              </Text>
            ) : it.kind === "note" ? (
              <Note it={it} author={names.member(it.actor.id)} time={clock(it.created_at, language, zone)} />
            ) : (
              <Message
                it={it}
                c={c}
                mine={it.actor.type !== "contact"}
                author={it.actor.type === "contact" ? contactName(c, t) : it.actor.type === "member" ? names.member(it.actor.id) : whoDid(it, byId, t)}
                time={clock(it.created_at, language, zone)}
                lang={me.data?.language}
                original={original}
                translating={enabled}
                watch={watch}
              />
            )}
          </Fragment>
        ))}
        {typing.length > 0 && (
          <Text size="xs" c="dimmed" fs="italic">
            {t("inbox.typing", { who: typing.map((x) => (x.contact ? contactName(c, t) : (x.name ?? ""))).join(", ") })}
          </Text>
        )}
        <div ref={bottom} />
      </Stack>
    </Box>
  )
}

/** MessageProps 是一条消息的显示设置。 */
interface MessageProps {
  it: ConversationItemDTO
  c: ConversationDTO
  mine: boolean // 成员和系统发的，在右侧
  author: string
  time: string
  lang: string | undefined // 成员的语言：对方的消息显示这种语言的译文
  original: boolean // 顶部选了“原文”：对方的消息都显示原文
  translating: boolean // 翻译可用：还缺我的语言的译文的消息显示“翻译中”，出现在页面上时请求补译
  watch: FillWatcher // 挂在还缺译文的消息上，见 useFillVisible
}

/**
 * Message 是一条消息：对方的在左侧，成员和系统发的在右侧，旁边是送达状态，失败时可以重发。
 * 对方的消息有译文时显示译文，下面一行“译文 · 原文”切换（顶部全局切换时重置单条状态）；成员翻译后发出的消息显示原文，下面一行展开译文。
 */
function Message({ it, c, mine, author, time, lang, original, translating, watch }: MessageProps): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const [prevOriginal, setPrevOriginal] = useState(original)
  const [flip, setFlip] = useState(false) // 这一条和顶部的“显示原文”相反；顶部切换时重置
  if (prevOriginal !== original) {
    setPrevOriginal(original)
    setFlip(false)
  }
  const [sent, setSent] = useState(false) // 展开发给对方的译文
  const queryClient = useQueryClient()
  const retry = useMutation({
    mutationFn: () => tenantApi.conversation.retry(c.id, it.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
    onError: (err) => notifyError(err),
  })

  // 对方的消息：有成员语言的译文时显示译文；成员翻译后发出的消息：显示原文，content.text 是发给对方的译文；
  // 系统发出的结构化提示：按成员的界面语言和时区渲染
  const text = it.content.prompt ? promptText(it.content.prompt, it.content.text ?? "", ui, t, zone) : (it.content.text ?? "")
  const translated = mine ? undefined : translationOf(it, lang)
  const showOriginal = original !== flip
  const sentAs = mine && it.content.original ? it.content.language : undefined
  const shown = translated && !showOriginal ? translated : sentAs ? (it.content.original ?? "") : text
  const from = it.derived.language as string | undefined
  let caption: ReactNode = null
  if (translated) {
    // “译文 · 原文”两个词总在这一行的最左边，切换时只互换哪个是链接，文字和位置都不变
    const toggle = () => setFlip((f) => !f)
    caption = (
      <>
        {showOriginal ? <Toggle label={t("inbox.translated")} onClick={toggle} /> : <Current label={t("inbox.translated")} />}
        <Text size="xs" c="dimmed">
          ·
        </Text>
        {showOriginal ? <Current label={t("inbox.original")} /> : <Toggle label={t("inbox.original")} onClick={toggle} />}
        {from && (
          <Text size="xs" c="dimmed" ml={6}>
            {languageName(from, ui)}
          </Text>
        )}
      </>
    )
  } else if (!mine && translating && it.untranslated) {
    caption = (
      <>
        <Loader size={10} />
        <Text size="xs" c="dimmed">
          {t("inbox.translating")}
        </Text>
      </>
    )
  } else if (sentAs) {
    caption = (
      <>
        <Text size="xs" c="dimmed">
          {t("inbox.sent_as", { lang: languageName(sentAs, ui) })} ·
        </Text>
        <Toggle label={t(sent ? "inbox.hide_translation" : "inbox.view_translation")} onClick={() => setSent((v) => !v)} />
      </>
    )
  }

  return (
    <Box
      style={{ alignSelf: mine ? "flex-end" : "flex-start", maxWidth: "72%" }}
      ref={!mine && translating && it.untranslated ? watch(it.id) : undefined}
    >
      <Text size="xs" c="dimmed" mb={2} ta={mine ? "right" : "left"} px={4}>
        {author} · {time}
        {mine && it.delivery && (
          <>
            {" · "}
            <Text span size="xs" c={deliveryColor[it.delivery]}>
              {t(`inbox.delivery_${it.delivery}` as const)}
            </Text>
            {it.delivery === "failed" && (
              <>
                {" · "}
                <UnstyledButton fz="xs" c="red" td="underline" onClick={() => retry.mutate()} disabled={retry.isPending}>
                  {t("inbox.retry")}
                </UnstyledButton>
              </>
            )}
          </>
        )}
      </Text>
      <Paper
        px="sm"
        py={8}
        radius="md"
        withBorder={!mine}
        bg={mine ? "var(--mantine-primary-color-light)" : undefined}
        className="inbox-bubble"
        data-incoming={!mine || undefined}
        data-original={(translated && showOriginal) || undefined}
      >
        <Linkified text={shown} />
        {sentAs && sent && (
          <Box mt={6} pt={6} className="inbox-sent-translation">
            <Text size="xs" c="dimmed">
              {t("inbox.sent_translation", { lang: languageName(sentAs, ui) })}
            </Text>
            <Linkified text={it.content.text ?? ""} />
          </Box>
        )}
      </Paper>
      {caption && (
        <Group gap={4} mt={2} px={6} justify={mine ? "flex-end" : "flex-start"}>
          {caption}
        </Group>
      )}
    </Box>
  )
}

/** Current 是“译文 · 原文”中正在显示的那个：和链接同样大小，不能点。 */
function Current({ label }: { label: string }): ReactNode {
  return (
    <Text size="xs" span>
      {label}
    </Text>
  )
}

/** Toggle 是消息下面一行中的切换链接。 */
function Toggle({ label, onClick }: { label: string; onClick: () => void }): ReactNode {
  return (
    <UnstyledButton fz="xs" c="brand" onClick={onClick}>
      {label}
    </UnstyledButton>
  )
}

/** Note 是内部备注：占满一行、黄色底，@ 到的同事加粗。只有成员可见。 */
function Note({ it, author, time }: { it: ConversationItemDTO; author: string; time: string }): ReactNode {
  const t = useT()
  return (
    <Paper px="sm" py={8} radius="md" bg="var(--mantine-color-yellow-light)" className="inbox-note">
      <Text size="xs" fw={600} className="inbox-note-accent" mb={2}>
        {t("inbox.note_by", { who: author })} · {time}
      </Text>
      <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
        {(it.content.text ?? "").split(/(@\S+)/).map((part, i) =>
          part.startsWith("@") ? (
            // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的片段没有别的标识，顺序固定
            <Text key={i} span fw={700} className="inbox-note-accent">
              {part}
            </Text>
          ) : (
            part
          ),
        )}
      </Text>
    </Paper>
  )
}

/** LINK 匹配消息中的网址：http:// 或 https:// 开头，到空白为止，去掉结尾的标点。 */
const LINK = /https?:\/\/[^\s<>\x22]+[^\s<>\x22\x27.,;:!?)\]}。，；：！？）】」』]/g

/** Linkified 显示消息文字，其中的网址是在新标签页中打开的链接。 */
function Linkified({ text }: { text: string }): ReactNode {
  const parts: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(LINK)) {
    const at = m.index ?? 0
    if (at > last) parts.push(text.slice(last, at))
    parts.push(
      <Anchor key={at} href={m[0]} target="_blank" rel="noopener noreferrer" size="sm">
        {m[0]}
      </Anchor>,
    )
    last = at + m[0].length
  }
  if (last < text.length) parts.push(text.slice(last))
  return (
    <Text size="sm" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
      {parts}
    </Text>
  )
}
