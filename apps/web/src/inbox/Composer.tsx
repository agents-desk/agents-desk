import type {
  AttributeDTO,
  ConversationActions,
  ConversationDTO,
  ConversationTranslation,
  File as UploadedFile,
  MacroDTO,
  MemberTenantMember,
  TagDTO,
  TeamDTO,
  TranslationPreviewDTO,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Badge,
  Box,
  Button,
  Code,
  Combobox,
  FileButton,
  Group,
  Loader,
  Menu,
  Paper,
  ScrollArea,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  Tooltip,
  UnstyledButton,
  useCombobox,
} from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  IconCheck,
  IconChevronDown,
  IconLanguage,
  IconLock,
  IconMessageBolt,
  IconPaperclip,
  IconPencil,
  IconRefresh,
  IconX,
} from "@tabler/icons-react"
import { describeError } from "@go-tenon/client"
import {
  errorMessage,
  notifyError,
  TriggerMenu,
  textTriggerRule,
  useCan,
  useLanguage,
  useT,
  useTextTrigger,
  useTimeZone,
  useUploadQueue,
} from "@go-tenon/ui"
import { type ReactNode, type RefObject, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName, languageOptions, validLanguage } from "../languages"
import {
  countMacroActions,
  emptyPendingActions,
  hasPendingActions,
  type MacroWarning,
  matchMacros,
  mergePendingActions,
  type PendingActions,
  renderMacroBody,
  resolveMacroActions,
  toConversationActions,
} from "../macros/template"
import { ATTACHMENT_ACCEPT, AttachmentStrip, attachmentMaxSize, MAX_ATTACHMENTS } from "./Attachments"
import { useAttributes, useMacros, useMe, useMembers, useTags, useTeams } from "./data"
import { clearDraft, type DraftMode, getDraft, saveDraft } from "./drafts"
import { snoozeOptions } from "./menus"

/** Mode 是回复框的模式：回复发给对方；内部备注只有成员可见，可以 @ 同事。 */
type Mode = DraftMode

/** ComposerHandle 是暴露给会话视图的命令式控制接口，用于快捷键 R / N 切换模式并聚焦输入框。 */
export interface ComposerHandle {
  focus: (mode?: Mode) => void
}

/** After 是发送之后对会话做的事：什么也不做、关闭、稍后处理到某个时间。 */
type After = { kind: "none" } | { kind: "close" } | { kind: "snooze"; until: Date }

/** NO_TRANSLATION 是目标语言中的“不翻译”：发送原文。 */
const NO_TRANSLATION = "none"

/** PAUSE 是停止输入后触发翻译预览的防抖时长（毫秒）。 */
const PAUSE = 600

/** sameLanguage 判断两个 BCP 47 语言标签是否指向同一书写语言（合并 zh-CN/zh-Hans 与 zh-TW/zh-Hant）。 */
function sameLanguage(a?: string, b?: string): boolean {
  if (!a || !b) return false
  const norm = (s: string) => {
    const k = s.trim().toLowerCase().replaceAll("_", "-")
    if (k === "zh-cn" || k === "zh-sg" || k === "zh-hans") return "zh-hans"
    if (k === "zh-tw" || k === "zh-hk" || k === "zh-mo" || k === "zh-hant") return "zh-hant"
    return k
  }
  return norm(a) === norm(b)
}

/** Preview 是一次翻译预览：为哪段文字、哪个目标语言（空是对方的语言）做的，以及结果。 */
interface Preview {
  text: string
  target: string
  result: TranslationPreviewDTO
}

/**
 * Composer 是会话底部的一体化回复卡片：顶栏切换回复与内部备注模式（外语回复时右侧附带目标语言选择器），
 * 内部备注切换为淡黄底并支持输入 @ 提及同事；回复模式输入 / 或点击工具栏按钮搜索并插入快捷回复。
 * 翻译开启且目标语言与坐席不同时，宽屏在卡片内以“左原文 | 右译文”左右对照展示实时译文与回译（窄屏自动退化到输入框上方），
 * 支持点击译文轻量手动微调、恢复自动译文、重新翻译，以及在发送下拉菜单中单次发送原文。
 */
export function Composer({
  c,
  onTyping,
  onReplied,
  handleRef,
}: {
  c: ConversationDTO
  onTyping: () => void
  onReplied?: () => void
  handleRef?: RefObject<ComposerHandle | null>
}): ReactNode {
  const t = useT()
  const can = useCan()
  const os = useOs()
  const isMac = os === "macos" || os === "ios"
  const modeKey = isMac ? "⌘I" : "Ctrl+I"
  const sendModKey = isMac ? "⌘ + ↵" : "Ctrl + Enter"
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const me = useMe()
  const ctrlEnterSend = me.data?.send_shortcut === "ctrl_enter"
  const { list: members, byId: memberById } = useMembers()
  const teams = useTeams()
  const tags = useTags()
  const convAttrs = useAttributes("conversation")
  const macros = useMacros()

  const [mode, setMode] = useState<Mode>(() => getDraft(c.id)?.mode ?? "reply")
  const [text, setText] = useState(() => getDraft(c.id)?.text ?? "")
  const [mentionIds, setMentionIds] = useState<string[]>(() => getDraft(c.id)?.mentionIds ?? [])
  const [pendingActions, setPendingActions] = useState<PendingActions>(emptyPendingActions)
  const [macroWarnings, setMacroWarnings] = useState<MacroWarning[]>([])
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const resetFile = useRef<() => void>(null)

  const area = useRef<HTMLTextAreaElement>(null)
  const value = text.trim()
  const note = mode === "note"
  const hasPending = !note && hasPendingActions(pendingActions)
  const preview = useReplyTranslation(c.id, !note ? value : "", c.language, me.data?.language)
  const queue = useUploadQueue<UploadedFile>({
    upload: (file) => tenantApi.file.upload({ file }),
    accept: ATTACHMENT_ACCEPT,
    maxFiles: MAX_ATTACHMENTS,
    maxSize: attachmentMaxSize,
    onError: (message) => notifications.show({ color: "red", message }),
  })
  const attachmentIds = queue.ready.map((f) => f.id)
  const hasContent = Boolean(value) || attachmentIds.length > 0

  // 实时保存草稿，切换会话后再回来不丢失已输入内容与模式
  useEffect(() => {
    saveDraft(c.id, { text, mode, mentionIds })
  }, [c.id, text, mode, mentionIds])

  const send = useMutation({
    mutationFn: async ({ after, translation, mode: sendMode }: { after: After; translation?: ConversationTranslation; mode: Mode }) => {
      const attachments = attachmentIds.length > 0 ? attachmentIds : undefined
      if (sendMode === "note") {
        // 只算还留在文字中的 @
        const ids = members.filter((m) => mentionIds.includes(m.id) && value.includes(`@${m.name}`)).map((m) => m.id)
        await tenantApi.conversation.note(c.id, {
          text: value || undefined,
          mentions: ids.length > 0 ? ids : undefined,
          attachments,
        })
        return
      }
      // 合并草稿附带操作与发送下拉菜单中的显式状态操作（下拉菜单优先级更高）
      let actions: ConversationActions | undefined = toConversationActions(pendingActions)
      if (after.kind === "close") {
        actions = { ...(actions ?? {}), status: { kind: "close" } }
      } else if (after.kind === "snooze") {
        actions = { ...(actions ?? {}), status: { kind: "snooze", until: after.until.toISOString() } }
      }
      if (value || attachments) {
        await tenantApi.conversation.reply(c.id, { text: value || undefined, translation, attachments, actions })
      } else if (actions) {
        await tenantApi.conversation.applyActions(c.id, actions)
      }
    },
    onSuccess: (_data, variables) => {
      clearDraft(c.id)
      setText("")
      setMentionIds([])
      setPendingActions(emptyPendingActions())
      setMacroWarnings([])
      queue.clear()
      preview.reset()
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      // 回复发送成功且开启了“发送后自动打开下一个待回复”时，自动跳转（内部备注不跳转）
      if (variables.mode === "reply") {
        onReplied?.()
      }
    },
    onError: (err) => notifyError(err),
  })

  // 发送：有文字且需要翻译时确保译文与当前原文一致（支持发送手动微调后的译文）；skipTranslation 可单次跳过翻译直接发原文。
  const submit = async (after: After, options?: { skipTranslation?: boolean }) => {
    if (queue.uploading || queue.hasError || send.isPending || (!note && preview.pending)) return
    if (note) {
      if (!hasContent) return
      return send.mutate({ after, mode: "note" })
    }
    if (!value) {
      if (!hasContent && !hasPending && after.kind === "none") return
      return send.mutate({ after, mode: "reply" })
    }
    if (options?.skipTranslation || !preview.active) return send.mutate({ after, mode: "reply" })
    const current = preview.current ?? (await preview.translateNow())
    if (!current) return
    if (!current.needed) return send.mutate({ after, mode: "reply" })
    if (preview.current) {
      const translatedText = preview.editedText !== undefined ? preview.editedText.trim() : current.translation
      if (!translatedText) return send.mutate({ after, mode: "reply" })
      return send.mutate({ after, mode: "reply", translation: { language: current.language, text: translatedText } })
    }
    preview.askConfirm()
  }

  const canAssignOthers = can("conversations:assign")

  // 统一使用框架的 useTextTrigger 管理内部备注的 @ 提及同事与回复模式的 / 快捷回复
  const triggerRules = useMemo(
    () => [
      textTriggerRule({
        id: "mention",
        trigger: "@",
        enabled: note,
        items: (q) =>
          members
            .filter((m) => m.id !== me.data?.id && m.name.toLowerCase().includes(q.toLowerCase()))
            .slice(0, 6),
        getKey: (m) => m.id,
        renderItem: (m: MemberTenantMember) => (
          <Group gap="xs" wrap="nowrap">
            <Avatar src={m.avatar_url || undefined} size={22} name={m.name} />
            <Text size="sm" truncate>
              {m.name}
            </Text>
            <Text size="xs" c="dimmed" ml="auto">
              {m.role_name}
            </Text>
          </Group>
        ),
        onSelect: (m, { replace }) => {
          setMentionIds((all) => (all.includes(m.id) ? all : [...all, m.id]))
          replace(`@${m.name} `)
        },
      }),
      textTriggerRule({
        id: "macro",
        trigger: "/",
        enabled: !note,
        items: (q) => matchMacros(macros, q),
        getKey: (m) => m.id,
        renderItem: (m: MacroDTO) => {
          const actionCount = countMacroActions(m.actions)
          return (
            <Stack gap={2}>
              <Group gap={6} wrap="nowrap">
                <Text size="sm" fw={500} truncate>
                  {m.title}
                </Text>
                {m.shortcut && (
                  <Code fz={11} style={{ flex: "none" }}>
                    /{m.shortcut}
                  </Code>
                )}
                <Badge size="xs" variant="light" color={m.scope === "shared" ? "brand" : "gray"} ml="auto" style={{ flex: "none" }}>
                  {t(m.scope === "shared" ? "inbox.macro_scope_shared" : "inbox.macro_scope_personal")}
                </Badge>
              </Group>
              <Group gap={6} wrap="nowrap">
                {m.body ? (
                  <Text size="xs" c="dimmed" truncate style={{ flex: 1 }}>
                    {m.body}
                  </Text>
                ) : (
                  <Text size="xs" c="dimmed" style={{ flex: 1 }}>
                    —
                  </Text>
                )}
                {actionCount > 0 && (
                  <Badge size="xs" variant="dot" color="bright-blue" style={{ flex: "none" }}>
                    {t("macros.summary_actions_count", { n: actionCount })}
                  </Badge>
                )}
              </Group>
            </Stack>
          )
        },
        onSelect: (m, { replace }) => {
          const rendered = renderMacroBody(m.body, { contact: c.contact, memberName: me.data?.name })
          const { pending, warnings } = resolveMacroActions(m.actions, {
            c,
            meId: me.data?.id,
            canAssignOthers,
            teams,
            members,
            tags,
            convAttrs,
            zone,
          })
          if (hasPendingActions(pending)) {
            setPendingActions((prev) => mergePendingActions(prev, pending))
          }
          setMacroWarnings(warnings)
          replace(rendered)
        },
      }),
    ],
    [note, members, me.data?.id, me.data?.name, macros, t, c, canAssignOthers, teams, tags, convAttrs, zone],
  )

  const handleTextChange = useCallback(
    (next: string) => {
      setText(next)
      if (next.trim()) onTyping()
    },
    [onTyping],
  )

  const trigger = useTextTrigger({
    ref: area,
    value: text,
    onChange: handleTextChange,
    rules: triggerRules,
  })

  const toggleMode = () => {
    setMode((m) => (m === "reply" ? "note" : "reply"))
    trigger.close()
  }

  // 供 R / N 快捷键切换到对应模式并聚焦回复框
  useImperativeHandle(
    handleRef,
    () => ({
      focus: (nextMode?: Mode) => {
        if (nextMode) {
          setMode(nextMode)
          trigger.close()
        }
        requestAnimationFrame(() => {
          const el = area.current
          if (!el) return
          el.focus()
          const end = el.value.length
          el.setSelectionRange(end, end)
        })
      },
    }),
    [trigger],
  )

  const canSubmit =
    !queue.uploading && !queue.hasError && !send.isPending && !(!note && preview.pending) && (note ? hasContent : hasContent || hasPending)
  const sendHint = ctrlEnterSend ? t("inbox.ctrl_enter_hint", { key: sendModKey }) : t("inbox.enter_hint")
  const hasTranslationOutput = !note && preview.active && Boolean(preview.current?.needed || preview.last?.result.needed)
  const mainLabel = note
    ? t("inbox.add_note")
    : !hasContent && hasPending
      ? t("inbox.apply_actions")
      : preview.confirming
        ? t("inbox.confirm_send")
        : preview.current?.needed
          ? t("inbox.send_translation")
          : t("inbox.send")

  return (
    <Box
      className="inbox-composer"
      onDragEnter={(e) => {
        if (!e.dataTransfer.types.includes("Files")) return
        e.preventDefault()
        dragDepth.current++
        setDragging(true)
      }}
      onDragOver={(e) => {
        if (!e.dataTransfer.types.includes("Files")) return
        e.preventDefault()
      }}
      onDragLeave={() => {
        if (dragDepth.current === 0) return
        dragDepth.current--
        if (dragDepth.current === 0) setDragging(false)
      }}
      onDrop={(e) => {
        dragDepth.current = 0
        setDragging(false)
        queue.handleDrop(e)
      }}
    >
      {/* 待执行快捷操作胶囊条在输入卡片上方 */}
      {!note && (hasPending || macroWarnings.length > 0) && (
        <PendingActionsBar
          pending={pendingActions}
          warnings={macroWarnings}
          teams={teams}
          memberById={memberById}
          tags={tags}
          convAttrs={convAttrs}
          onChange={setPendingActions}
          onClear={() => {
            setPendingActions(emptyPendingActions())
            setMacroWarnings([])
          }}
          onDismissWarnings={() => setMacroWarnings([])}
        />
      )}
      <Box className="inbox-composer-box" data-note={note || undefined} data-dragging={dragging || undefined}>
        <TriggerMenu
          trigger={trigger}
          position="top"
          width={trigger.activeRule?.id === "macro" ? 360 : 240}
          header={trigger.activeRule?.id === "macro" ? t("inbox.macro_header") : undefined}
          empty={trigger.activeRule?.id === "macro" ? t("inbox.macro_empty") : undefined}
        />
        <Group gap="xs" className="inbox-composer-head" data-tn-density="inspector">
          <Tooltip label={t("inbox.switch_mode_hint", { key: modeKey })} openDelay={350}>
            <SegmentedControl
              value={mode}
              onChange={(v) => {
                setMode(v as Mode)
                trigger.close()
              }}
              data={[
                { value: "reply", label: t("inbox.mode_reply") },
                { value: "note", label: t("inbox.mode_note") },
              ]}
            />
          </Tooltip>
          {note && (
            <Group gap={4} wrap="nowrap">
              <IconLock size={12} className="inbox-note-accent" style={{ flex: "none" }} />
              <Text size="xs" className="inbox-note-accent">
                {t("inbox.note_hint")}
              </Text>
            </Group>
          )}
          {dragging ? (
            <Text size="xs" c="brand" fw={500} ml="auto">
              {t("inbox.attach_drop")}
            </Text>
          ) : (
            !note && preview.enabled && <TranslationTargetPicker c={c} myLang={me.data?.language} preview={preview} />
          )}
        </Group>
        <AttachmentStrip items={queue.items} onRemove={queue.remove} onRetry={queue.retry} />
        <Box className="inbox-composer-main" data-split={(!note && preview.split) || undefined}>
          <Box
            className="inbox-composer-body"
            onClick={(e) => {
              if (e.target === e.currentTarget) area.current?.focus()
            }}
          >
            <Textarea
              ref={area}
              variant="unstyled"
              size="md"
              classNames={{ input: "inbox-composer-input" }}
              value={text}
              onChange={(e) => trigger.handleChange(e.currentTarget.value, e.currentTarget.selectionStart)}
              onPaste={(e) => {
                queue.handlePaste(e)
              }}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return
                if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "i") {
                  e.preventDefault()
                  toggleMode()
                  return
                }
                if (trigger.handleKeyDown(e)) return
                const hitSend = ctrlEnterSend
                  ? e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey
                  : e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey
                if (hitSend) {
                  e.preventDefault()
                  void submit({ kind: "none" })
                }
              }}
              placeholder={t(note ? "inbox.note_placeholder" : c.status === "closed" ? "inbox.reopen_reply_placeholder" : "inbox.reply_placeholder")}
              autosize
              minRows={3}
              maxRows={10}
              maxLength={10000}
              aria-label={t(note ? "inbox.mode_note" : "inbox.mode_reply")}
            />
          </Box>
          {!note && preview.split && (
            <InlineTranslationPreview
              c={c}
              hasText={Boolean(value)}
              preview={preview}
              ctrlEnterSend={ctrlEnterSend}
              onSendOriginal={() => void submit({ kind: "none" }, { skipTranslation: true })}
              onSubmit={() => void submit({ kind: "none" })}
            />
          )}
        </Box>
        <Group gap={6} className="inbox-composer-foot" data-tn-density="dense">
          {!note && (
            <Tooltip label={t("inbox.macro_button")}>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="input-sm"
                onClick={() => trigger.open("macro")}
                aria-label={t("inbox.macro_button")}
              >
                <IconMessageBolt size={16} />
              </ActionIcon>
            </Tooltip>
          )}
          <FileButton
            resetRef={resetFile}
            multiple
            accept={ATTACHMENT_ACCEPT}
            onChange={(files) => {
              if (files.length > 0) queue.add(files)
              resetFile.current?.()
            }}
          >
            {(props) => (
              <Tooltip label={t("inbox.attach")}>
                <ActionIcon {...props} variant="subtle" color="gray" size="input-sm" aria-label={t("inbox.attach")}>
                  <IconPaperclip size={16} />
                </ActionIcon>
              </Tooltip>
            )}
          </FileButton>
          {!note && preview.confirming && (
            <Text size="xs" c="brand" ml={4}>
              {ctrlEnterSend ? t("inbox.ctrl_enter_again", { key: sendModKey }) : t("inbox.enter_again")}
            </Text>
          )}
          <Group gap={0} ml="auto" wrap="nowrap">
            <Tooltip label={sendHint} openDelay={350}>
              <Button
                color={note ? "yellow" : undefined}
                disabled={!canSubmit}
                loading={send.isPending || (!note && preview.pending)}
                onClick={() => void submit({ kind: "none" })}
                style={note ? undefined : { borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
              >
                {mainLabel}
              </Button>
            </Tooltip>
            {!note && (
              <Menu position="top-end" withinPortal>
                <Menu.Target>
                  <Button px={7} disabled={!canSubmit} className="inbox-send-menu" aria-label={t("inbox.send_options")}>
                    <IconChevronDown size={14} />
                  </Button>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item onClick={() => void submit({ kind: "none" })}>{t("inbox.send")}</Menu.Item>
                  {hasTranslationOutput && (
                    <Menu.Item onClick={() => void submit({ kind: "none" }, { skipTranslation: true })}>{t("inbox.send_original")}</Menu.Item>
                  )}
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
    </Box>
  )
}

/** PendingActionsBar 在回复框上方以可关闭的胶囊展示快捷回复载入的待执行操作，以及失效或无权项提醒。 */
function PendingActionsBar({
  pending,
  warnings,
  teams,
  memberById,
  tags,
  convAttrs,
  onChange,
  onClear,
  onDismissWarnings,
}: {
  pending: PendingActions
  warnings: MacroWarning[]
  teams: TeamDTO[]
  memberById: Map<string, MemberTenantMember>
  tags: TagDTO[]
  convAttrs: AttributeDTO[]
  onChange: (next: PendingActions) => void
  onClear: () => void
  onDismissWarnings: () => void
}): ReactNode {
  const t = useT()
  const teamById = useMemo(() => new Map(teams.map((x) => [x.id, x])), [teams])
  const tagById = useMemo(() => new Map(tags.map((x) => [x.id, x])), [tags])
  const attrByKey = useMemo(() => new Map(convAttrs.map((x) => [x.key, x])), [convAttrs])

  const chips: { key: string; label: string; color?: string; onRemove: () => void }[] = []

  if (pending.team) {
    const label =
      pending.team.mode === "clear"
        ? t("inbox.macro_action_clear_team")
        : t("inbox.macro_action_assign_team", { name: (pending.team.id && teamById.get(pending.team.id)?.name) || "" })
    chips.push({
      key: "team",
      label,
      color: "bright-blue",
      onRemove: () => onChange({ ...pending, team: undefined }),
    })
  }

  if (pending.assignee) {
    let label: string
    if (pending.assignee.mode === "me") label = t("inbox.macro_action_assign_me")
    else if (pending.assignee.mode === "clear") label = t("inbox.macro_action_clear_assignee")
    else label = t("inbox.macro_action_assign_member", { name: (pending.assignee.id && memberById.get(pending.assignee.id)?.name) || "" })
    chips.push({
      key: "assignee",
      label,
      color: "bright-blue",
      onRemove: () => onChange({ ...pending, assignee: undefined }),
    })
  }

  if (pending.priority) {
    chips.push({
      key: "priority",
      label: t(pending.priority === "high" ? "inbox.macro_action_priority_high" : "inbox.macro_action_priority_normal"),
      color: pending.priority === "high" ? "deep-orange" : "gray",
      onRemove: () => onChange({ ...pending, priority: undefined }),
    })
  }

  for (const tagId of pending.addTagIDs) {
    const tag = tagById.get(tagId)
    if (!tag) continue
    chips.push({
      key: `tag:${tagId}`,
      label: `+${tag.name}`,
      color: tag.color,
      onRemove: () => onChange({ ...pending, addTagIDs: pending.addTagIDs.filter((id) => id !== tagId) }),
    })
  }

  for (const [k, v] of Object.entries(pending.setAttributes)) {
    const def = attrByKey.get(k)
    const display = v === null || v === undefined ? t("macros.action_clear_value") : Array.isArray(v) ? v.join(", ") : String(v)
    chips.push({
      key: `attr:${k}`,
      label: `${def?.name ?? k}: ${display}`,
      color: "violet",
      onRemove: () => {
        const nextAttrs = { ...pending.setAttributes }
        delete nextAttrs[k]
        onChange({ ...pending, setAttributes: nextAttrs })
      },
    })
  }

  if (pending.status) {
    const label =
      pending.status.kind === "close"
        ? t("inbox.macro_action_close")
        : t("inbox.macro_action_snooze", { when: t(`inbox.snooze_${pending.status.preset}` as const) })
    chips.push({
      key: "status",
      label,
      color: pending.status.kind === "close" ? "green" : "yellow",
      onRemove: () => onChange({ ...pending, status: undefined }),
    })
  }

  const warningText = (w: MacroWarning): string => {
    switch (w.kind) {
      case "no_assign_perm":
        return t("inbox.macro_warn_no_assign_perm")
      case "team_missing":
        return t("inbox.macro_warn_team_missing")
      case "team_disabled":
        return t("inbox.macro_warn_team_disabled", { name: w.name })
      case "member_missing":
        return t("inbox.macro_warn_member_missing")
      case "tag_missing":
        return t("inbox.macro_warn_tag_missing")
      case "attr_missing":
        return t("inbox.macro_warn_attr_missing", { key: w.key })
    }
  }

  return (
    <Stack gap={6} mb={8}>
      {chips.length > 0 && (
        <Paper withBorder radius="md" px="xs" py={6} bg="var(--tn-canvas)">
          <Group gap={6} wrap="wrap" align="center">
            <Text size="xs" c="dimmed" fw={500}>
              {t("inbox.macro_pending_label")}
            </Text>
            {chips.map((chip) => (
              <Badge
                key={chip.key}
                size="sm"
                variant="light"
                color={chip.color}
                pr={4}
                rightSection={
                  <ActionIcon
                    size={14}
                    variant="transparent"
                    color={chip.color ?? "gray"}
                    aria-label={t("inbox.macro_action_remove")}
                    onClick={chip.onRemove}
                  >
                    <IconX size={10} />
                  </ActionIcon>
                }
              >
                {chip.label}
              </Badge>
            ))}
            <UnstyledButton fz="xs" c="dimmed" ml="auto" onClick={onClear}>
              {t("inbox.macro_action_clear_actions")}
            </UnstyledButton>
          </Group>
        </Paper>
      )}
      {warnings.length > 0 && (
        <Paper withBorder radius="md" px="xs" py={5} bg="var(--mantine-color-deep-orange-light)">
          <Group gap="xs" wrap="nowrap" align="flex-start">
            <Stack gap={2} style={{ flex: 1 }}>
              {warnings.map((w, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: 单次选中生成的静态警告列表
                <Text key={i} size="xs" c="deep-orange">
                  {warningText(w)}
                </Text>
              ))}
            </Stack>
            <ActionIcon size="xs" variant="transparent" color="deep-orange" onClick={onDismissWarnings} aria-label={t("inbox.cancel")}>
              <IconX size={12} />
            </ActionIcon>
          </Group>
        </Paper>
      )}
    </Stack>
  )
}

/** ReplyTranslation 是回复的翻译预览状态和操作，包含手动微调译文的能力。 */
interface ReplyTranslation {
  enabled: boolean // 翻译可用
  active: boolean // 这段回复要经过翻译：翻译可用、有文字、目标语言有效
  split: boolean // 启用左右对照译文栏：翻译开启且当前目标语言需要翻译（或已有译文/错误）
  target: string // 选的目标语言；空为跟随对方语言，NO_TRANSLATION 为不翻译
  setTarget: (target: string) => void
  last?: Preview // 最近一次预览，可能是改动原文之前的结果
  current?: TranslationPreviewDTO // 与当前原文和目标语言一致的最新预览结果
  editedText?: string // 坐席手动微调后的译文；未修改时为 undefined
  setEditedText: (text: string | undefined) => void
  pending: boolean
  error?: string // 翻译失败的提示
  confirming: boolean // 按回车时先触发了翻译，等待成员再按一次发送
  translateNow: () => Promise<TranslationPreviewDTO | undefined> // 立即翻译当前文字
  retry: () => void
  askConfirm: () => void
  reset: () => void // 发送之后清空
}

/**
 * useReplyTranslation 翻译会话 id 中的回复 value：停止输入 PAUSE 毫秒后调用预览接口，旧请求的结果自动丢弃；
 * 支持坐席手动微调译文，原文或目标语言变化时自动清空手动修改并重新翻译。
 */
function useReplyTranslation(id: string, value: string, customerLang?: string, myLang?: string): ReplyTranslation {
  const settings = useQuery(kit.queries.translation.settings())
  const enabled = settings.data?.enabled ?? false
  const [target, setTargetState] = useState("")
  const [last, setLast] = useState<Preview>()
  const [editedText, setEditedText] = useState<string | undefined>(undefined)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [confirming, setConfirming] = useState(false)
  const seq = useRef(0) // 每次请求递增，只采用最新一次结果
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const active = enabled && !!value && target !== NO_TRANSLATION
  const current = last && last.text === value && last.target === target ? last.result : undefined

  const run = useCallback(
    async (text: string, to: string): Promise<TranslationPreviewDTO | undefined> => {
      const n = ++seq.current
      setPending(true)
      setError(undefined)
      try {
        const result = await tenantApi.translation.preview(id, { text, language: to || undefined })
        if (n === seq.current) {
          setLast({ text, target: to, result })
          setEditedText(undefined)
        }
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

  // 原文或目标语言变化时清除二次确认态和手动译文编辑
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在原文或目标语言变化时重置
  useEffect(() => {
    setConfirming(false)
    setEditedText(undefined)
  }, [value, target])

  // 停止输入片刻后自动翻译；原文或目标语言变化时作废在途请求
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

  const willTranslate =
    enabled &&
    target !== NO_TRANSLATION &&
    (target !== "" ? !sameLanguage(target, myLang) : Boolean(customerLang) && !sameLanguage(customerLang, myLang))
  const needed = current?.needed ?? last?.result.needed ?? false
  return {
    enabled,
    active,
    split: willTranslate || (active && (needed || !!error)),
    target,
    setTarget: (to) => setTargetState(to),
    last,
    current,
    editedText,
    setEditedText,
    pending,
    error,
    confirming: confirming && !!current,
    translateNow: () => {
      clearTimeout(timer.current)
      return run(value, target)
    },
    retry: () => {
      clearTimeout(timer.current)
      void run(value, target)
    },
    askConfirm: () => setConfirming(true),
    reset: () => {
      seq.current++
      setLast(undefined)
      setEditedText(undefined)
      setPending(false)
      setError(undefined)
      setConfirming(false)
    },
  }
}

/**
 * TranslationTargetPicker 是输入卡片顶栏右侧的目标语言选择器：
 * 打开会话即可直观看到当前翻译目标语言，点击弹出可搜索下拉菜单切换语言或关闭翻译。
 */
function TranslationTargetPicker({
  c,
  myLang,
  preview,
}: {
  c: ConversationDTO
  myLang?: string
  preview: ReplyTranslation
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

  const theirs = c.language ?? ""
  const off = preview.target === NO_TRANSLATION
  const effectiveLang = off ? "" : preview.target || preview.last?.result.language || theirs
  const sameAsMe = Boolean(effectiveLang) && sameLanguage(effectiveLang, myLang)
  const activeValue = off ? NO_TRANSLATION : effectiveLang

  let label: string
  let active = false
  if (off) {
    label = t("inbox.no_translation")
  } else if (effectiveLang && !sameAsMe) {
    label = t("inbox.translate_to", { lang: languageName(effectiveLang, ui) })
    active = true
  } else {
    label = t("inbox.sends_original")
  }

  const baseOptions = languageOptions(ui, [effectiveLang, theirs].filter(Boolean))
  const typed = validLanguage(search)
  const rawOptions = [
    { value: NO_TRANSLATION, label: t("inbox.no_translation") },
    ...baseOptions.map((o) => ({
      value: o.value,
      label: theirs && o.value === theirs ? t("inbox.their_language", { lang: o.label }) : o.label,
    })),
  ]
  if (typed && !rawOptions.some((o) => o.value === typed)) {
    rawOptions.splice(1, 0, { value: typed, label: `${languageName(typed, ui)} (${typed})` })
  }
  const q = search.trim().toLowerCase()
  const filtered = q ? rawOptions.filter((o) => o.label.toLowerCase().includes(q) || o.value.toLowerCase().includes(q)) : rawOptions

  return (
    <Combobox
      store={combobox}
      width={240}
      position="top-end"
      withinPortal
      onOptionSubmit={(val) => {
        preview.setTarget(val === theirs ? "" : val)
        combobox.closeDropdown()
      }}
    >
      <Combobox.Target targetType="button">
        <Button
          ml="auto"
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

/**
 * InlineTranslationPreview 是输入卡片内的译文区（宽屏居右与原文左右对照，窄屏自动排在输入框上方）：
 * 展示空态占位、翻译中、实时译文与回译校验，支持点击译文直接轻量手动微调并可一键恢复或重新翻译。
 */
function InlineTranslationPreview({
  c,
  hasText,
  preview,
  ctrlEnterSend,
  onSendOriginal,
  onSubmit,
}: {
  c: ConversationDTO
  hasText: boolean
  preview: ReplyTranslation
  ctrlEnterSend: boolean
  onSendOriginal: () => void
  onSubmit: () => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [editing, setEditing] = useState(false)
  const shown = preview.current ?? preview.last?.result
  const stale = hasText && !preview.current && !!preview.last
  const isEdited = preview.editedText !== undefined && shown !== undefined && preview.editedText !== shown.translation
  const displayText = preview.editedText ?? shown?.translation ?? ""
  const targetLang = preview.target || shown?.language || c.language || ""
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
      {preview.error ? (
        <Stack gap={6}>
          <Text size="xs" c="red">
            {t("inbox.translation_failed", { error: preview.error })}
          </Text>
          <Group gap="sm" wrap="nowrap">
            <UnstyledButton fz="xs" c="brand" onClick={preview.retry}>
              {t("inbox.retry")}
            </UnstyledButton>
            <UnstyledButton fz="xs" c="dimmed" onClick={onSendOriginal}>
              {t("inbox.send_original")}
            </UnstyledButton>
          </Group>
        </Stack>
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
          <Group gap={6} align="flex-start" wrap="nowrap">
            <Box style={{ flex: 1, minWidth: 0 }}>
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
            </Box>
            <Group gap={2} wrap="nowrap" style={{ flex: "none" }}>
              {preview.pending || stale ? (
                <Tooltip label={t("inbox.retranslating")} openDelay={200}>
                  <Box px={4} pt={4}>
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
            </Group>
          </Group>
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
                <Text span size="xs" fw={600} mr={6}>
                  {t("inbox.back_translation")}
                </Text>
                {shown.back_translation}
              </Text>
            )
          )}
        </Box>
      )}
    </Box>
  )
}
