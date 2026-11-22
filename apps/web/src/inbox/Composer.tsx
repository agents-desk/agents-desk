import type { ConversationDTO, ConversationTranslation, MemberTenantMember, TranslationPreviewDTO } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Box, Button, Group, Loader, Menu, Paper, SegmentedControl, Select, Text, Textarea, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconChevronDown, IconLanguage } from "@tabler/icons-react"
import { describeError } from "@go-tenon/client"
import { errorMessage, notifyError, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName, languageOptions, validLanguage } from "../languages"
import { useMe, useMembers } from "./data"
import { snoozeOptions } from "./menus"

/** Mode 是回复框的模式：回复发给对方；内部备注只有成员可见，可以 @ 同事。 */
type Mode = "reply" | "note"

/** After 是发送之后对会话做的事：什么也不做、关闭、稍后处理到某个时间。 */
type After = { kind: "none" } | { kind: "close" } | { kind: "snooze"; until: Date }

/** NO_TRANSLATION 是目标语言中的“不翻译”：发送原文。 */
const NO_TRANSLATION = "none"

/** PAUSE 是停止输入多久之后开始翻译（毫秒）。 */
const PAUSE = 800

/** Preview 是一次翻译预览：为哪段文字、哪个目标语言（空是对方的语言）做的，以及结果。 */
interface Preview {
  text: string
  target: string
  result: TranslationPreviewDTO
}

/**
 * Composer 是会话底部的回复框：回复和内部备注两种模式，内部备注时变成黄色底，输入 @ 选择同事。
 * 回车发送，Shift + 回车换行，用输入法选词时的回车只确认候选词。发送按钮的下拉中有“发送并关闭”“发送并稍后处理”。
 * 输入时调用 onTyping，会话的频道据此告诉别人“正在输入”。
 *
 * 翻译可用时，回复在停止输入片刻后翻译，输入框上方显示发给对方的译文和回译，发送的就是这段译文。目标语言默认是对方的语言，
 * 可以换成别的语言或者“不翻译”。预览之后改了原文、马上按回车时，先翻译并显示新的译文，再按一次回车才发送：发出的总是成员看过的译文。
 * 不需要翻译时（对方和成员同一种语言、还没识别出对方的语言）不显示预览，直接发送原文。内部备注不翻译。
 */
export function Composer({ c, onTyping }: { c: ConversationDTO; onTyping: () => void }): ReactNode {
  const t = useT()
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const [mode, setMode] = useState<Mode>("reply")
  const [text, setText] = useState("")
  const [mentions, setMentions] = useState<MemberTenantMember[]>([])
  const [query, setQuery] = useState<string | null>(null) // 正在输入的 @ 后面的文字；没有在 @ 时为 null
  const [pick, setPick] = useState(0)
  const area = useRef<HTMLTextAreaElement>(null)
  const value = text.trim()
  const preview = useReplyTranslation(c.id, mode === "reply" ? value : "")

  const send = useMutation({
    mutationFn: async ({ after, translation }: { after: After; translation?: ConversationTranslation }) => {
      if (mode === "note") {
        // 只算还留在文字中的 @
        const ids = mentions.filter((m) => value.includes(`@${m.name}`)).map((m) => m.id)
        await tenantApi.conversation.note(c.id, { text: value, mentions: ids.length > 0 ? ids : undefined })
      } else {
        await tenantApi.conversation.reply(c.id, { text: value, translation })
      }
      if (after.kind === "close") await tenantApi.conversation.close(c.id)
      if (after.kind === "snooze") await tenantApi.conversation.snooze(c.id, { until: after.until.toISOString() })
    },
    onSuccess: () => {
      setText("")
      setMentions([])
      preview.reset()
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
    },
    onError: (err) => notifyError(err),
  })
  // 发送：回复要翻译时，发出的是当前的译文；译文不是这段文字的（还没翻译、改过原文、失败了）时先翻译，显示出来等成员再按一次
  const submit = async (after: After) => {
    if (!value || send.isPending) return
    if (mode === "note" || !preview.active) return send.mutate({ after })
    const current = preview.current ?? (await preview.translateNow())
    if (!current) return
    if (!current.needed) return send.mutate({ after })
    if (preview.current) return send.mutate({ after, translation: { language: current.language, text: current.translation } })
    preview.askConfirm()
  }

  // @ 同事：光标前面是 @ 加上几个字时，列出名字里有这几个字的同事
  const candidates =
    mode === "note" && query !== null
      ? members.filter((m) => m.id !== me.data?.id && m.name.toLowerCase().includes(query.toLowerCase())).slice(0, 6)
      : []
  const onChange = (next: string, caret: number) => {
    setText(next)
    if (next.trim()) onTyping()
    const m = mode === "note" ? /@([^\s@]*)$/.exec(next.slice(0, caret)) : null
    setQuery(m ? m[1] : null)
    setPick(0)
  }
  const choose = (m: MemberTenantMember) => {
    const el = area.current
    const caret = el?.selectionStart ?? text.length
    const before = text.slice(0, caret).replace(/@([^\s@]*)$/, `@${m.name} `)
    const next = before + text.slice(caret)
    setText(next)
    setMentions((all) => (all.some((x) => x.id === m.id) ? all : [...all, m]))
    setQuery(null)
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(before.length, before.length)
    })
  }

  const note = mode === "note"
  return (
    <Box className="inbox-composer" data-note={note || undefined} px="sm" pt={8} pb={10}>
      {/* 译文预览在最上面：它出现、变高、消失时，输入框和发送按钮的位置不动 */}
      {!note && <TranslationPreview c={c} preview={preview} onSendOriginal={() => preview.setTarget(NO_TRANSLATION)} />}
      <Group gap="xs" mb={6}>
        <SegmentedControl
          size="xs"
          value={mode}
          onChange={(v) => {
            setMode(v as Mode)
            setQuery(null)
          }}
          data={[
            { value: "reply", label: t("inbox.mode_reply") },
            { value: "note", label: t("inbox.mode_note") },
          ]}
        />
        {note && (
          <Text size="xs" c="yellow.9">
            {t("inbox.note_hint")}
          </Text>
        )}
      </Group>
      <Box pos="relative">
        {candidates.length > 0 && (
          <Paper withBorder shadow="md" radius="md" p={4} pos="absolute" bottom="100%" left={0} mb={4} w={240} style={{ zIndex: 10 }}>
            {candidates.map((m, i) => (
              <UnstyledButton
                key={m.id}
                w="100%"
                px="xs"
                py={6}
                className="inbox-mention"
                data-active={i === pick || undefined}
                onMouseDown={(e) => {
                  e.preventDefault()
                  choose(m)
                }}
              >
                <Group gap="xs" wrap="nowrap">
                  <Avatar src={m.avatar_url || undefined} size={22} name={m.name} />
                  <Text size="sm" truncate>
                    {m.name}
                  </Text>
                  <Text size="xs" c="dimmed" ml="auto">
                    {m.role_name}
                  </Text>
                </Group>
              </UnstyledButton>
            ))}
          </Paper>
        )}
        <Textarea
          ref={area}
          value={text}
          onChange={(e) => onChange(e.currentTarget.value, e.currentTarget.selectionStart)}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (candidates.length > 0 && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
              e.preventDefault()
              setPick((p) => (p + (e.key === "ArrowDown" ? 1 : candidates.length - 1)) % candidates.length)
              return
            }
            if (candidates.length > 0 && (e.key === "Enter" || e.key === "Tab")) {
              e.preventDefault()
              choose(candidates[pick])
              return
            }
            if (e.key === "Escape") setQuery(null)
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault()
              void submit({ kind: "none" })
            }
          }}
          placeholder={t(note ? "inbox.note_placeholder" : "inbox.reply_placeholder")}
          autosize
          minRows={2}
          maxRows={8}
          maxLength={10000}
          aria-label={t(note ? "inbox.mode_note" : "inbox.mode_reply")}
        />
      </Box>
      <Group gap="xs" mt={8}>
        {!note && preview.enabled && !!value && !preview.shown && (
          <Tooltip label={t("inbox.choose_language")}>
            <ActionIcon variant="subtle" color="gray" size="sm" onClick={preview.pick} aria-label={t("inbox.choose_language")}>
              <IconLanguage size={14} />
            </ActionIcon>
          </Tooltip>
        )}
        <Text size="xs" c={preview.confirming ? "bright-blue" : "dimmed"}>
          {t(preview.confirming ? "inbox.enter_again" : "inbox.enter_hint")}
        </Text>
        <Group gap={0} ml="auto" wrap="nowrap">
          <Button
            size="xs"
            color={note ? "yellow" : undefined}
            disabled={!value}
            loading={send.isPending || (!note && preview.pending)}
            onClick={() => void submit({ kind: "none" })}
            style={note ? undefined : { borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
          >
            {t(note ? "inbox.add_note" : preview.current?.needed ? "inbox.send_translation" : "inbox.send")}
          </Button>
          {!note && (
            <Menu position="top-end" withinPortal>
              <Menu.Target>
                <Button size="xs" px={6} disabled={!value || send.isPending || preview.pending} style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, borderLeft: "1px solid var(--mantine-color-white)" }} aria-label={t("inbox.send_options")}>
                  <IconChevronDown size={14} />
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item onClick={() => void submit({ kind: "none" })}>{t("inbox.send")}</Menu.Item>
                <Menu.Item onClick={() => void submit({ kind: "close" })}>{t("inbox.send_close")}</Menu.Item>
                <Menu.Label>{t("inbox.send_snooze")}</Menu.Label>
                {snoozeOptions(zone).map((o) => (
                  <Menu.Item key={o.key} onClick={() => void submit({ kind: "snooze", until: o.until })}>
                    {t(`inbox.snooze_${o.key}` as const)}
                  </Menu.Item>
                ))}
              </Menu.Dropdown>
            </Menu>
          )}
        </Group>
      </Group>
    </Box>
  )
}

/** ReplyTranslation 是回复的翻译预览的状态和操作，见 useReplyTranslation。 */
interface ReplyTranslation {
  enabled: boolean // 翻译可用
  active: boolean // 这段回复要经过翻译：翻译可用、有文字、目标语言不是“不翻译”
  shown: boolean // 输入框上方显示预览
  target: string // 选的目标语言；空是对方的语言
  setTarget: (target: string) => void
  pick: () => void // 显示预览来选择目标语言（不需要翻译、预览没显示时）
  last?: Preview // 最近一次预览，可能是改动之前的文字的
  current?: TranslationPreviewDTO // 最近一次预览正是当前的文字和目标语言时的结果
  pending: boolean
  error?: string // 翻译失败的提示
  confirming: boolean // 按回车时先翻译了，等成员再按一次
  translateNow: () => Promise<TranslationPreviewDTO | undefined> // 马上翻译当前的文字
  retry: () => void
  askConfirm: () => void
  reset: () => void // 发送之后清空
}

/**
 * useReplyTranslation 翻译会话 id 中的回复 value：停止输入 PAUSE 毫秒后调用预览接口，旧的请求的结果丢弃。
 * value 为空（没有文字、内部备注）时什么也不做。
 */
function useReplyTranslation(id: string, value: string): ReplyTranslation {
  const settings = useQuery(kit.queries.translation.settings())
  const enabled = settings.data?.enabled ?? false
  const [target, setTargetState] = useState("")
  const [manual, setManual] = useState(false)
  const [last, setLast] = useState<Preview>()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [confirming, setConfirming] = useState(false)
  const seq = useRef(0) // 每次请求加一，只采用最新一次的结果
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined) // 等停止输入后再翻译的计时
  const active = enabled && !!value && target !== NO_TRANSLATION
  const current = last && last.text === value && last.target === target ? last.result : undefined

  const run = useCallback(
    async (text: string, to: string): Promise<TranslationPreviewDTO | undefined> => {
      const n = ++seq.current
      setPending(true)
      setError(undefined)
      try {
        const result = await tenantApi.translation.preview(id, { text, language: to || undefined })
        if (n === seq.current) setLast({ text, target: to, result })
        return n === seq.current ? result : undefined
      } catch (err) {
        if (n === seq.current) setError(errorMessage(describeError(err)))
        return undefined
      } finally {
        if (n === seq.current) setPending(false)
      }
    },
    [id],
  )

  // 文字或目标语言变了：不再等成员确认
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在文字或目标语言变化时清掉
  useEffect(() => setConfirming(false), [value, target])

  // 停止输入片刻后翻译；文字或目标语言变了，之前没回来的请求作废
  useEffect(() => {
    if (!active) {
      seq.current++
      setPending(false)
      setError(undefined)
      return
    }
    if (last && last.text === value && last.target === target) return
    timer.current = setTimeout(() => void run(value, target), PAUSE)
    return () => clearTimeout(timer.current)
  }, [active, value, target, run, last])

  const needed = current?.needed ?? last?.result.needed ?? false
  return {
    enabled,
    active,
    shown: enabled && !!value && (manual || target !== "" || needed || !!error),
    target,
    setTarget: (to) => {
      setTargetState(to)
      setManual(true)
    },
    pick: () => setManual(true),
    last,
    current,
    pending,
    error,
    confirming: confirming && !!current,
    // 马上翻译：取消还在等的那次，否则它晚些发出的请求会让这次的结果作废
    translateNow: () => {
      clearTimeout(timer.current)
      return run(value, target)
    },
    retry: () => void run(value, target),
    askConfirm: () => setConfirming(true),
    reset: () => {
      seq.current++
      setLast(undefined)
      setPending(false)
      setError(undefined)
      setConfirming(false)
    },
  }
}

/**
 * TranslationPreview 是输入框上方的译文预览：发给对方的语言（可以换，或者不翻译），译文和回译；
 * 正在翻译、原文改了正在重新翻译、翻译失败（重试或者发送原文）。
 */
function TranslationPreview({ c, preview, onSendOriginal }: { c: ConversationDTO; preview: ReplyTranslation; onSendOriginal: () => void }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [search, setSearch] = useState("")
  if (!preview.shown) return null

  // 目标语言的选项：不翻译、对方的语言、常用语言；搜索的文字是一个语言标签时也加进去
  const theirs = c.language ?? ""
  const options = languageOptions(ui, [preview.target !== NO_TRANSLATION ? preview.target : "", theirs]).map((o) =>
    o.value === theirs ? { ...o, label: t("inbox.their_language", { lang: o.label }) } : o,
  )
  const typed = validLanguage(search)
  if (typed && !options.some((o) => o.value === typed)) options.unshift({ value: typed, label: `${languageName(typed, ui)} (${typed})` })
  const data = [{ value: NO_TRANSLATION, label: t("inbox.no_translation") }, ...options]
  const value = preview.target || preview.last?.result.language || theirs || null

  const off = preview.target === NO_TRANSLATION
  const shown = preview.current ?? preview.last?.result
  const stale = !preview.current && !!preview.last
  let status: ReactNode = null
  if (off) status = t("inbox.sends_original")
  else if (preview.pending) status = stale ? t("inbox.retranslating") : t("inbox.translating")
  else if (stale && !preview.error) status = t("inbox.retranslating")

  return (
    <Paper
      withBorder
      radius="md"
      px="sm"
      py={6}
      mb={8}
      className="inbox-preview"
      data-stale={(stale && !off) || undefined}
      bg={preview.error && !off ? "var(--mantine-color-red-light)" : "var(--mantine-color-bright-blue-light)"}
    >
      <Group gap="xs" wrap="nowrap">
        <Text size="xs" c="dimmed" style={{ flex: "none" }}>
          {t("inbox.preview_to")}
        </Text>
        <Select
          size="xs"
          w={220}
          data={data}
          value={value}
          onChange={(v) => v && preview.setTarget(v === theirs ? "" : v)}
          searchable
          searchValue={search}
          onSearchChange={setSearch}
          placeholder={t("inbox.choose_language")}
          nothingFoundMessage={t("inbox.language_not_found")}
          comboboxProps={{ withinPortal: true }}
          aria-label={t("inbox.choose_language")}
          allowDeselect={false}
        />
        {status && (
          <Group gap={4} ml="auto" wrap="nowrap">
            {preview.pending && !off && <Loader size={10} />}
            <Text size="xs" c="dimmed">
              {status}
            </Text>
          </Group>
        )}
      </Group>
      {!off && preview.error && (
        <Group gap="xs" mt={6}>
          <Text size="xs" c="red" style={{ flex: 1 }}>
            {t("inbox.translation_failed", { error: preview.error })}
          </Text>
          <UnstyledButton fz="xs" c="bright-blue" onClick={preview.retry}>
            {t("inbox.retry")}
          </UnstyledButton>
          <UnstyledButton fz="xs" c="bright-blue" onClick={onSendOriginal}>
            {t("inbox.send_original")}
          </UnstyledButton>
        </Group>
      )}
      {!off && !preview.error && shown?.needed && (
        <Box mt={6} className="inbox-preview-text">
          <Text size="sm" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
            {shown.translation}
          </Text>
          <Text size="xs" c="dimmed" mt={4} style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
            <Text span size="xs" fw={600} mr={6}>
              {t("inbox.back_translation")}
            </Text>
            {shown.back_translation}
          </Text>
        </Box>
      )}
      {!off && !preview.error && shown && !shown.needed && !preview.pending && (
        <Text size="xs" c="dimmed" mt={4}>
          {t("inbox.no_translation_needed")}
        </Text>
      )}
    </Paper>
  )
}
