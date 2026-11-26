import type {
  ConversationAutoTranslateRequest,
  ConversationItemDTO,
  ConversationTranslation,
  TranslationPreviewDTO,
  TranslationTone,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Box,
  Button,
  Combobox,
  Group,
  Loader,
  Menu,
  ScrollArea,
  Stack,
  Text,
  Textarea,
  Tooltip,
  UnstyledButton,
  useCombobox,
} from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { IconCheck, IconChevronDown, IconLanguage, IconPencil, IconRefresh, IconX } from "@tabler/icons-react"
import { describeError } from "@go-tenon/client"
import { errorMessage, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName, languageOptions, sameLanguage, validLanguage } from "../languages"

/** NO_TRANSLATION 是目标语言中的“不翻译”：按原文发出。 */
export const NO_TRANSLATION = "none"

/** PAUSE 是展开校对面板后停止输入触发翻译预览的防抖时长（毫秒）。 */
const PAUSE = 600

/** Preview 是一次翻译预览缓存：为哪段文字、哪个目标语言与语气做的，以及结果。 */
interface Preview {
  text: string
  target: string
  tone: TranslationTone | ""
  result: TranslationPreviewDTO
}

/**
 * ComposerTranslationMode 区分两种译文提交语义：
 * - replace：对外发给客户的回复，发送译文并保留原文（默认折叠态也会自动翻译后再发出）；
 * - verified：内部聊天、内部备注与工单公开进度，默认折叠态直接发原文由后台自动互译，展开校对后把核对过的译文一并写入派生译文。
 */
export type ComposerTranslationMode = "replace" | "verified"

/** ComposerTranslation 是统一的输入框译文校对状态与操作接口。 */
export interface ComposerTranslation {
  enabled: boolean // 工作区翻译功能已启用
  expanded: boolean // 左右对照译文校对面板是否展开
  setExpanded: (open: boolean) => void
  toggleExpanded: () => void
  mode: ComposerTranslationMode
  allowOff: boolean // 是否允许在目标语言中选择“不翻译”
  defaultTarget: string // 场景自动推导的默认目标语言
  target: string // 用户显式选择的目标语言；空表示跟随 defaultTarget，NO_TRANSLATION 表示不翻译
  effectiveLang: string // 生效中的目标语言（不含 NO_TRANSLATION）
  setTarget: (target: string) => void
  tone: TranslationTone | "" // 本次单条翻译的可选语气覆盖；空表示使用工作区默认语气
  setTone: (tone: TranslationTone | "") => void
  last?: Preview
  current?: TranslationPreviewDTO
  editedText?: string
  setEditedText: (text: string | undefined) => void
  pending: boolean
  error?: string
  confirming: boolean
  prepareSubmit: (options?: {
    skipTranslation?: boolean
  }) => Promise<{ send: boolean; translation?: ConversationTranslation; autoTranslate?: ConversationAutoTranslateRequest }>
  retry: () => void
  reset: () => void
}

/** defaultPeerLanguage 从一组候选语言里选出最常见且与 myLang 不同的语言；没有不同语言时返回空字符串。 */
export function defaultPeerLanguage(langs: (string | undefined)[], myLang?: string): string {
  const counts = new Map<string, number>()
  let best = ""
  let bestCount = 0
  for (const raw of langs) {
    const code = raw?.trim()
    if (!code || (myLang && sameLanguage(code, myLang))) continue
    const next = (counts.get(code) ?? 0) + 1
    counts.set(code, next)
    if (next > bestCount) {
      best = code
      bestCount = next
    }
  }
  return best
}

/** verifiedTranslationOf 返回作者在发送前人工校对过的译文与目标语言；未附带校对译文时返回 undefined。 */
export function verifiedTranslationOf(it: ConversationItemDTO): { language: string; text: string } | undefined {
  const lang = it.derived.verified_language as string | undefined
  if (!lang) return undefined
  const all = it.derived.translations as Record<string, string> | undefined
  if (!all) return undefined
  if (all[lang]) return { language: lang, text: all[lang] }
  for (const [k, v] of Object.entries(all)) {
    if (v && sameLanguage(k, lang)) return { language: k, text: v }
  }
  return undefined
}

/**
 * useComposerTranslation 管理统一的输入框翻译校对状态：
 * 默认折叠（输入零打扰，不触发击键防抖请求）；展开后与客服回复一致，停止输入 600ms 后实时预览目标译文与盲回译，
 * 支持切换目标语言、单次覆盖语气、点击译文轻量微调、恢复自动译文与重新翻译。
 */
export function useComposerTranslation({
  conversationId,
  text,
  defaultTarget = "",
  myLang,
  mode,
  active = true,
}: {
  conversationId: string
  text: string
  defaultTarget?: string
  myLang?: string
  mode: ComposerTranslationMode
  active?: boolean
}): ComposerTranslation {
  const settings = useQuery(kit.queries.translation.settings())
  const enabled = active && (settings.data?.enabled ?? false)
  const allowOff = mode === "replace"

  const [expanded, setExpanded] = useState(false)
  const [target, setTargetState] = useState("")
  const [tone, setToneState] = useState<TranslationTone | "">("")
  const [last, setLast] = useState<Preview>()
  const [editedText, setEditedText] = useState<string | undefined>(undefined)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [confirming, setConfirming] = useState(false)
  const seq = useRef(0)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

  const off = allowOff && target === NO_TRANSLATION
  const requestedTarget = off ? "" : target || defaultTarget
  const targetKey = off ? NO_TRANSLATION : requestedTarget
  const effectiveLang = off ? "" : target || last?.result.language || defaultTarget
  const sameAsMe = Boolean(requestedTarget) && sameLanguage(requestedTarget, myLang)
  // 展开态下需要预览的条件：有文字、未关闭翻译，且目标语言与我不同（replace 模式允许目标未知时交由后端自动识别客户语言）
  const livePreviewActive = enabled && expanded && Boolean(text) && !off && !sameAsMe && (mode === "replace" || Boolean(requestedTarget))
  const current = last && last.text === text && last.target === targetKey && last.tone === tone ? last.result : undefined

  const run = useCallback(
    async (bodyText: string, to: string, chosenTone: TranslationTone | "", skipBack: boolean): Promise<TranslationPreviewDTO | undefined> => {
      const n = ++seq.current
      setPending(true)
      setError(undefined)
      try {
        const result = await tenantApi.translation.preview(conversationId, {
          text: bodyText,
          language: to || undefined,
          tone: chosenTone || undefined,
          skip_back_translation: skipBack || undefined,
        })
        if (n === seq.current) {
          if (!skipBack) {
            setLast({ text: bodyText, target: to, tone: chosenTone, result })
            setEditedText(undefined)
          }
        }
        return n === seq.current ? result : undefined
      } catch (err) {
        if (n === seq.current) setError(errorMessage(describeError(err)))
        return undefined
      } finally {
        if (n === seq.current) setPending(false)
      }
    },
    [conversationId],
  )

  // 原文、目标语言或语气变化时清除二次确认态和手动译文编辑
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在原文、目标语言或语气变化时重置
  useEffect(() => {
    setConfirming(false)
    setEditedText(undefined)
  }, [text, targetKey, tone])

  // 仅在展开校对面板时，停止输入 PAUSE 毫秒后实时预览译文与盲回译
  useEffect(() => {
    if (!livePreviewActive) {
      clearTimeout(timer.current)
      seq.current++
      setPending(false)
      if (!expanded) setError(undefined)
      if (!text) setLast(undefined)
      return
    }
    if (last && last.text === text && last.target === targetKey && last.tone === tone) return
    timer.current = setTimeout(() => void run(text, requestedTarget, tone, false), PAUSE)
    return () => clearTimeout(timer.current)
  }, [livePreviewActive, expanded, text, targetKey, requestedTarget, tone, run, last])

  const prepareSubmit = useCallback(
    async (options?: {
      skipTranslation?: boolean
    }): Promise<{ send: boolean; translation?: ConversationTranslation; autoTranslate?: ConversationAutoTranslateRequest }> => {
      if (options?.skipTranslation || !enabled || !text || off) {
        return { send: true }
      }
      if (!expanded) {
        if (mode === "verified") {
          // 内部聊天/备注/工单公开进度在折叠态零延迟直发原文，由后台按读者/客户语言自动翻译
          return { send: true }
        }
        if (sameAsMe) {
          return { send: true }
        }
        // 客服回复与商务对外回复在折叠态零等待秒发：交由后端投递队列异步翻译并发出，不阻塞输入框
        clearTimeout(timer.current)
        return {
          send: true,
          autoTranslate: {
            language: requestedTarget || undefined,
            tone: tone || undefined,
          },
        }
      }

      // 展开态：所有场景统一遵循“有最新译文直接发送（含手动微调），未翻译完先触发预览并提示再按一次确认”
      if (sameAsMe || (mode === "verified" && !requestedTarget)) {
        return { send: true }
      }
      if (current) {
        if (!current.needed) return { send: true }
        const translatedText = editedText !== undefined ? editedText.trim() : current.translation
        if (!translatedText) return { send: true }
        return { send: true, translation: { language: current.language, text: translatedText } }
      }
      clearTimeout(timer.current)
      const res = await run(text, requestedTarget, tone, false)
      if (!res) return { send: false }
      if (!res.needed) return { send: true }
      setConfirming(true)
      return { send: false }
    },
    [enabled, text, off, expanded, mode, sameAsMe, requestedTarget, tone, current, editedText, run],
  )

  return {
    enabled,
    expanded,
    setExpanded,
    toggleExpanded: () => setExpanded((v) => !v),
    mode,
    allowOff,
    defaultTarget,
    target,
    effectiveLang,
    setTarget: (to) => setTargetState(to),
    tone,
    setTone: (nextTone) => setToneState(nextTone),
    last,
    current,
    editedText,
    setEditedText,
    pending,
    error,
    confirming: expanded && confirming && !!current,
    prepareSubmit,
    retry: () => {
      clearTimeout(timer.current)
      if (!text || off || sameAsMe) return
      void run(text, requestedTarget, tone, false)
    },
    reset: () => {
      clearTimeout(timer.current)
      seq.current++
      setLast(undefined)
      setEditedText(undefined)
      setToneState("")
      setPending(false)
      setError(undefined)
      setConfirming(false)
    },
  }
}

/**
 * TranslationToggleButton 是所有输入框底栏统一放置的译文校对图标开关，
 * 与快捷回复、附件等纯图标按钮保持一致视觉密度；状态细节通过 Tooltip 提示。
 */
export function TranslationToggleButton({
  preview,
  myLang,
  note,
}: {
  preview: ComposerTranslation
  myLang?: string
  note?: boolean
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  if (!preview.enabled) return null

  const off = preview.allowOff && preview.target === NO_TRANSLATION
  const sameAsMe = Boolean(preview.effectiveLang) && sameLanguage(preview.effectiveLang, myLang)

  let tooltip: string
  if (preview.expanded) {
    tooltip = t("inbox.collapse_translation")
  } else if (off) {
    tooltip = t("inbox.check_translation_off")
  } else if (preview.allowOff && preview.effectiveLang && !sameAsMe) {
    tooltip = t("inbox.check_translation_auto_to", { lang: languageName(preview.effectiveLang, ui) })
  } else {
    tooltip = t("inbox.check_translation_auto")
  }

  return (
    <Tooltip label={tooltip} openDelay={250}>
      <ActionIcon
        variant={preview.expanded ? "light" : "subtle"}
        color={preview.expanded ? (note ? "yellow" : undefined) : "gray"}
        size="input-sm"
        opacity={off && !preview.expanded ? 0.55 : undefined}
        onClick={preview.toggleExpanded}
        aria-pressed={preview.expanded}
        aria-label={t(preview.expanded ? "inbox.collapse_translation" : "inbox.check_translation")}
      >
        <IconLanguage size={16} />
      </ActionIcon>
    </Tooltip>
  )
}

/**
 * AutoTranslateHint 是译文校对收起时展示在发送按钮旁的轻量自动翻译状态提示：
 * 对外回复显示“自动译为 {{.lang}}”（或关闭时显示“按原文发送”），跨语言内部聊天显示“自动互译”，
 * 点击直接展开译文校对栏；展开校对栏后自动隐藏，保持界面无重复提示。
 */
export function AutoTranslateHint({
  preview,
  myLang,
}: {
  preview: ComposerTranslation
  myLang?: string
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  if (!preview.enabled || preview.expanded) return null

  const off = preview.allowOff && preview.target === NO_TRANSLATION
  const effectiveLang = preview.effectiveLang
  const sameAsMe = Boolean(effectiveLang) && sameLanguage(effectiveLang, myLang)

  let label = ""
  if (preview.mode === "replace") {
    if (off) {
      label = t("inbox.auto_translate_off")
    } else if (effectiveLang && !sameAsMe) {
      label = t("inbox.auto_translate_to", { lang: languageName(effectiveLang, ui) })
    }
  } else if (effectiveLang && !sameAsMe) {
    label = t("inbox.auto_translate_mutual")
  }

  if (!label) return null

  return (
    <Tooltip label={t("inbox.auto_translate_click_hint")} openDelay={300}>
      <UnstyledButton className="inbox-caption-btn" onClick={() => preview.setExpanded(true)}>
        {label}
      </UnstyledButton>
    </Tooltip>
  )
}

/**
 * TranslationTargetPicker 是展开后的译文校对面板顶部的目标语言选择器：
 * 支持搜索常见语言或输入任意 BCP 47 标签；对外回复场景支持选择“不翻译”。
 */
function TranslationTargetPicker({
  myLang,
  preview,
}: {
  myLang?: string
  preview: ComposerTranslation
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [search, setSearch] = useState("")
  const combobox = useCombobox({
    onDropdownClose: () => {
      combobox.resetSelectedOption()
      setSearch("")
    },
    onDropdownOpen: () => {
      combobox.focusSearchInput()
    },
  })

  const theirs = preview.defaultTarget
  const off = preview.allowOff && preview.target === NO_TRANSLATION
  const effectiveLang = preview.effectiveLang
  const sameAsMe = Boolean(effectiveLang) && sameLanguage(effectiveLang, myLang)
  const activeValue = off ? NO_TRANSLATION : effectiveLang

  let label: string
  let active = false
  if (off) {
    label = t("inbox.no_translation")
  } else if (effectiveLang && !sameAsMe) {
    label = t("inbox.translate_to", { lang: languageName(effectiveLang, ui) })
    active = true
  } else if (effectiveLang && sameAsMe) {
    label = t("inbox.sends_original")
  } else {
    label = t("inbox.choose_language")
  }

  const baseOptions = languageOptions(ui, [effectiveLang, theirs].filter(Boolean))
  const typed = validLanguage(search)
  const rawOptions = [
    ...(preview.allowOff ? [{ value: NO_TRANSLATION, label: t("inbox.no_translation") }] : []),
    ...baseOptions.map((o) => ({
      value: o.value,
      label: theirs && o.value === theirs ? t("inbox.their_language", { lang: o.label }) : o.label,
    })),
  ]
  if (typed && !rawOptions.some((o) => o.value === typed)) {
    rawOptions.splice(preview.allowOff ? 1 : 0, 0, { value: typed, label: `${languageName(typed, ui)} (${typed})` })
  }
  const q = search.trim().toLowerCase()
  const filtered = q ? rawOptions.filter((o) => o.label.toLowerCase().includes(q) || o.value.toLowerCase().includes(q)) : rawOptions

  return (
    <Combobox
      store={combobox}
      width={240}
      position="top-start"
      withinPortal
      onOptionSubmit={(val) => {
        preview.setTarget(val === theirs ? "" : val)
        combobox.closeDropdown()
      }}
    >
      <Combobox.Target targetType="button">
        <Button
          size="compact-xs"
          variant={active ? "light" : "subtle"}
          color={active ? undefined : "gray"}
          leftSection={<IconLanguage size={13} />}
          rightSection={<IconChevronDown size={11} />}
          onClick={() => combobox.toggleDropdown()}
          aria-label={t("inbox.choose_language")}
        >
          {label}
        </Button>
      </Combobox.Target>
      <Combobox.Dropdown>
        <Combobox.Search
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
          placeholder={t("inbox.choose_language")}
          aria-label={t("inbox.choose_language")}
        />
        <Combobox.Options>
          <ScrollArea.Autosize mah={220} type="scroll">
            {filtered.length === 0 ? (
              <Combobox.Empty>{t("inbox.language_not_found")}</Combobox.Empty>
            ) : (
              filtered.map((o) => (
                <Combobox.Option value={o.value} key={o.value} active={o.value === activeValue}>
                  <Group gap="xs" wrap="nowrap" justify="space-between">
                    <Text size="xs" truncate>
                      {o.label}
                    </Text>
                    {o.value === activeValue && <IconCheck size={13} color="var(--mantine-primary-color-filled)" style={{ flex: "none" }} />}
                  </Group>
                </Combobox.Option>
              ))
            )}
          </ScrollArea.Autosize>
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  )
}

/** TranslationTonePicker 是展开后的单次翻译语气选择器：默认沿用工作区全局语气，可按需单条切换亲切/中性/正式。 */
function TranslationTonePicker({ preview }: { preview: ComposerTranslation }): ReactNode {
  const t = useT()
  const toneLabels: Record<TranslationTone, string> = {
    friendly: t("inbox.tone_friendly"),
    neutral: t("inbox.tone_neutral"),
    formal: t("inbox.tone_formal"),
  }
  const options: { value: TranslationTone | ""; label: string }[] = [
    { value: "", label: t("inbox.translation_tone_default") },
    { value: "friendly", label: toneLabels.friendly },
    { value: "neutral", label: toneLabels.neutral },
    { value: "formal", label: toneLabels.formal },
  ]
  const currentLabel = preview.tone ? toneLabels[preview.tone] : t("inbox.translation_tone")

  return (
    <Menu position="top-start" withinPortal>
      <Menu.Target>
        <Button
          size="compact-xs"
          variant={preview.tone ? "light" : "subtle"}
          color={preview.tone ? undefined : "gray"}
          rightSection={<IconChevronDown size={11} />}
          aria-label={t("inbox.translation_tone")}
        >
          {currentLabel}
        </Button>
      </Menu.Target>
      <Menu.Dropdown>
        {options.map((o) => (
          <Menu.Item
            key={o.value || "default"}
            rightSection={preview.tone === o.value ? <IconCheck size={13} color="var(--mantine-primary-color-filled)" /> : undefined}
            onClick={() => preview.setTone(o.value)}
          >
            {o.label}
          </Menu.Item>
        ))}
      </Menu.Dropdown>
    </Menu>
  )
}

/**
 * InlineTranslationPane 是输入卡片内展开后的右侧实时译文校对栏（窄容器自动排在输入框上方）：
 * 顶部内置目标语言选择器、可选单次语气选择器与编辑/重译/收起按钮，主体展示实时译文、轻量就地微调与盲回译校验。
 */
export function InlineTranslationPane({
  hasText,
  myLang,
  preview,
  ctrlEnterSend,
  onSendWithoutTranslation,
  onSubmit,
}: {
  hasText: boolean
  myLang?: string
  preview: ComposerTranslation
  ctrlEnterSend: boolean
  onSendWithoutTranslation: () => void
  onSubmit: () => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [editing, setEditing] = useState(false)
  const shown = preview.current ?? preview.last?.result
  const stale = hasText && !preview.current && !!preview.last
  const isEdited = preview.editedText !== undefined && shown !== undefined && preview.editedText !== shown.translation
  const displayText = preview.editedText ?? shown?.translation ?? ""
  const off = preview.allowOff && preview.target === NO_TRANSLATION
  const targetLang = preview.effectiveLang
  const sameAsMe = Boolean(targetLang) && sameLanguage(targetLang, myLang)
  const empty = !hasText && !preview.error

  // 新译文到达、清空输入或出错时自动退出编辑态
  useEffect(() => {
    if (!hasText || preview.pending || preview.error) {
      setEditing(false)
    }
  }, [hasText, preview.pending, preview.error])

  const startEdit = () => {
    if (!shown?.needed || preview.pending || stale) return
    if (preview.editedText === undefined) {
      preview.setEditedText(shown.translation)
    }
    setEditing(true)
  }

  return (
    <Box
      className="inbox-composer-translation"
      data-empty={empty || undefined}
      data-stale={stale || undefined}
      data-confirming={preview.confirming || undefined}
      data-error={preview.error ? "" : undefined}
    >
      <Group gap={4} mb={6} wrap="nowrap" justify="space-between" className="inbox-translation-head">
        <Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
          <TranslationTargetPicker myLang={myLang} preview={preview} />
          {!off && !sameAsMe && <TranslationTonePicker preview={preview} />}
        </Group>
        <Group gap={2} wrap="nowrap" style={{ flex: "none" }}>
          {shown?.needed && !off && !sameAsMe && (
            <>
              {preview.pending || stale ? (
                <Tooltip label={t("inbox.retranslating")} openDelay={200}>
                  <Box px={4} pt={2}>
                    <Loader size={11} />
                  </Box>
                </Tooltip>
              ) : (
                <>
                  {!editing && (
                    <Tooltip label={t("inbox.edit_translation")} openDelay={300}>
                      <ActionIcon size={22} variant="subtle" color="gray" aria-label={t("inbox.edit_translation")} onClick={startEdit}>
                        <IconPencil size={13} />
                      </ActionIcon>
                    </Tooltip>
                  )}
                  <Tooltip label={t("inbox.retranslate")} openDelay={300}>
                    <ActionIcon size={22} variant="subtle" color="gray" aria-label={t("inbox.retranslate")} onClick={preview.retry}>
                      <IconRefresh size={13} />
                    </ActionIcon>
                  </Tooltip>
                </>
              )}
            </>
          )}
          <Tooltip label={t("inbox.collapse_translation")} openDelay={300}>
            <ActionIcon
              size={22}
              variant="subtle"
              color="gray"
              aria-label={t("inbox.collapse_translation")}
              onClick={() => preview.setExpanded(false)}
            >
              <IconX size={13} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>

      {preview.error ? (
        <Stack gap={6}>
          <Text size="xs" c="red">
            {t("inbox.translation_failed", { error: preview.error })}
          </Text>
          <Group gap="sm" wrap="nowrap">
            <UnstyledButton fz="xs" c="brand" onClick={preview.retry}>
              {t("inbox.retry")}
            </UnstyledButton>
            <UnstyledButton fz="xs" c="dimmed" onClick={onSendWithoutTranslation}>
              {t(preview.allowOff ? "inbox.send_original" : "inbox.skip_check_send")}
            </UnstyledButton>
          </Group>
        </Stack>
      ) : off ? (
        <Text size="xs" c="dimmed" lh={1.5} className="inbox-translation-placeholder">
          {t("inbox.sends_original")}
        </Text>
      ) : sameAsMe ? (
        <Text size="xs" c="dimmed" lh={1.5} className="inbox-translation-placeholder">
          {t("inbox.sends_original")}
        </Text>
      ) : !targetLang && preview.mode === "verified" ? (
        <Text size="xs" c="dimmed" lh={1.5} className="inbox-translation-placeholder">
          {t("inbox.translation_placeholder_generic")}
        </Text>
      ) : empty ? (
        <Text size="md" c="dimmed" lh={1.55} className="inbox-translation-placeholder">
          {targetLang ? t("inbox.translation_placeholder", { lang: languageName(targetLang, ui) }) : t("inbox.choose_language")}
        </Text>
      ) : !shown?.needed ? (
        <Group gap={6} wrap="nowrap">
          <Loader size={12} />
          <Text size="xs" c="dimmed">
            {t("inbox.translating")}
          </Text>
        </Group>
      ) : (
        <Box className="inbox-preview-text">
          {editing ? (
            <Textarea
              variant="unstyled"
              size="md"
              autosize
              minRows={1}
              maxRows={8}
              autoFocus
              classNames={{ input: "inbox-translation-input" }}
              value={displayText}
              onChange={(e) => preview.setEditedText(e.currentTarget.value)}
              onBlur={() => {
                const trimmed = displayText.trim()
                if (!trimmed || trimmed === shown.translation) {
                  preview.setEditedText(undefined)
                }
                setEditing(false)
              }}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return
                if (e.key === "Escape") {
                  e.preventDefault()
                  setEditing(false)
                  return
                }
                const hitSend = ctrlEnterSend
                  ? e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey
                  : e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey
                if (hitSend) {
                  e.preventDefault()
                  setEditing(false)
                  onSubmit()
                }
              }}
              aria-label={t("inbox.edit_translation")}
            />
          ) : (
            <Text
              size="md"
              lh={1.55}
              className="inbox-translation-editable"
              style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
              onClick={startEdit}
              title={t("inbox.edit_translation")}
            >
              {displayText}
            </Text>
          )}
          {isEdited ? (
            <Group gap={6} mt={6} wrap="nowrap">
              <Text size="xs" c="brand" fw={500}>
                {t("inbox.translation_edited")}
              </Text>
              <Text size="xs" c="dimmed">
                ·
              </Text>
              <UnstyledButton
                className="inbox-caption-btn"
                onClick={() => {
                  preview.setEditedText(undefined)
                  setEditing(false)
                }}
              >
                {t("inbox.reset_translation")}
              </UnstyledButton>
            </Group>
          ) : (
            shown.back_translation && (
              <Text size="xs" c="dimmed" mt={6} lh={1.45} style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                <Tooltip label={t("inbox.back_translation_hint")} multiline w={240} openDelay={200}>
                  <Text span size="xs" fw={600} mr={6} style={{ cursor: "help", textDecoration: "underline dotted" }}>
                    {t("inbox.back_translation")}
                  </Text>
                </Tooltip>
                {shown.back_translation}
              </Text>
            )
          )}
        </Box>
      )}
    </Box>
  )
}
