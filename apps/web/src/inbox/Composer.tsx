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
  FileButton,
  Group,
  Loader,
  Menu,
  Paper,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconChevronDown, IconLanguage, IconLock, IconMessageBolt, IconPaperclip, IconX } from "@tabler/icons-react"
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
import { LanguageSelect } from "../LanguageSelect"
import { languageName } from "../languages"
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

/** PAUSE 是停止输入多久之后开始翻译（毫秒）。 */
const PAUSE = 800

/** Preview 是一次翻译预览：为哪段文字、哪个目标语言（空是对方的语言）做的，以及结果。 */
interface Preview {
  text: string
  target: string
  result: TranslationPreviewDTO
}

/**
 * Composer 是会话底部的回复框：回复和内部备注两种模式，内部备注时变成黄色底，输入 @ 选择同事；
 * 回复模式下输入 / 或点击工具栏按钮搜索并插入快捷回复（0ms 本地展开联系人与成员变量，附带操作以可移除胶囊条预览）。
 * 回车发送，Shift + 回车换行，用输入法选词时的回车只确认候选词。发送按钮的下拉中有“发送并关闭”“发送并稍后处理”。
 * 回复与附带操作（及发送并关闭/稍后处理）统一走单事务原子接口提交。
 *
 * 翻译可用时，回复在停止输入片刻后翻译，输入框上方显示发给对方的译文和回译，发送的就是这段译文。目标语言默认是对方的语言，
 * 可以换成别的语言或者“不翻译”。预览之后改了原文、马上按回车时，先翻译并显示新的译文，再按一次回车才发送：发出的总是成员看过的译文。
 * 不需要翻译时（对方和成员同一种语言、还没识别出对方的语言）不显示预览，直接发送原文。内部备注不翻译。
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
  const preview = useReplyTranslation(c.id, !note ? value : "")
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

  // 发送：有文字且需要翻译时先确保译文与当前原文一致；无文字仅有附件或附带操作时直接发送/应用
  const submit = async (after: After) => {
    if (queue.uploading || queue.hasError || send.isPending || (!note && preview.pending)) return
    if (note) {
      if (!hasContent) return
      return send.mutate({ after, mode: "note" })
    }
    if (!value) {
      if (!hasContent && !hasPending && after.kind === "none") return
      return send.mutate({ after, mode: "reply" })
    }
    if (!preview.active) return send.mutate({ after, mode: "reply" })
    const current = preview.current ?? (await preview.translateNow())
    if (!current) return
    if (!current.needed) return send.mutate({ after, mode: "reply" })
    if (preview.current) return send.mutate({ after, mode: "reply", translation: { language: current.language, text: current.translation } })
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
  const mainLabel = note
    ? t("inbox.add_note")
    : !hasContent && hasPending
      ? t("inbox.apply_actions")
      : preview.current?.needed
        ? t("inbox.send_translation")
        : t("inbox.send")

  return (
    <Box
      className="inbox-composer"
      data-note={note || undefined}
      data-dragging={dragging || undefined}
      px="var(--tn-pane-px)"
      pt={10}
      pb={12}
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
      {/* 译文预览在最上面：它出现、变高、消失时，输入框和发送按钮的位置不动 */}
      {!note && <TranslationPreview c={c} preview={preview} onSendOriginal={() => preview.setTarget(NO_TRANSLATION)} />}
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
      <Group gap="xs" mb={8} data-tn-density="dense">
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
        {dragging && (
          <Text size="xs" c="brand" fw={500} ml="auto">
            {t("inbox.attach_drop")}
          </Text>
        )}
      </Group>
      <AttachmentStrip items={queue.items} onRemove={queue.remove} onRetry={queue.retry} />
      <Box pos="relative">
        <TriggerMenu
          trigger={trigger}
          position="top"
          width={trigger.activeRule?.id === "macro" ? 360 : 240}
          header={trigger.activeRule?.id === "macro" ? t("inbox.macro_header") : undefined}
          empty={trigger.activeRule?.id === "macro" ? t("inbox.macro_empty") : undefined}
        />
        <Textarea
          ref={area}
          size="md"
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
          minRows={2}
          maxRows={8}
          maxLength={10000}
          aria-label={t(note ? "inbox.mode_note" : "inbox.mode_reply")}
        />
      </Box>
      <Group gap="xs" mt={8} data-tn-density="dense">
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
        {!note && preview.enabled && !!value && !preview.shown && (
          <Tooltip label={t("inbox.choose_language")}>
            <ActionIcon variant="subtle" color="gray" size="input-sm" onClick={preview.pick} aria-label={t("inbox.choose_language")}>
              <IconLanguage size={15} />
            </ActionIcon>
          </Tooltip>
        )}
        <Text size="xs" c={preview.confirming ? "brand" : "dimmed"}>
          {ctrlEnterSend
            ? t(preview.confirming ? "inbox.ctrl_enter_again" : "inbox.ctrl_enter_hint", { key: sendModKey })
            : t(preview.confirming ? "inbox.enter_again" : "inbox.enter_hint")}
        </Text>
        <Group gap={0} ml="auto" wrap="nowrap">
          <Button
            color={note ? "yellow" : undefined}
            disabled={!canSubmit}
            loading={send.isPending || (!note && preview.pending)}
            onClick={() => void submit({ kind: "none" })}
            style={note ? undefined : { borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
          >
            {mainLabel}
          </Button>
          {!note && (
            <Menu position="top-end" withinPortal>
              <Menu.Target>
                <Button px={7} disabled={!canSubmit} className="inbox-send-menu" aria-label={t("inbox.send_options")}>
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
  if (!preview.shown) return null

  // 目标语言的选项：不翻译、对方的语言（注明）、常用语言
  const theirs = c.language ?? ""
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
      bg={preview.error && !off ? "var(--mantine-color-red-light)" : "var(--mantine-primary-color-light)"}
    >
      <Group gap="xs" wrap="wrap" data-tn-density="inspector">
        <Text size="xs" c="dimmed" style={{ flex: "none" }}>
          {t("inbox.preview_to")}
        </Text>
        <LanguageSelect
          style={{ flex: "1 1 160px", maxWidth: 220, minWidth: 140 }}
          before={[{ value: NO_TRANSLATION, label: t("inbox.no_translation") }]}
          first={[theirs]}
          labels={theirs ? { [theirs]: t("inbox.their_language", { lang: languageName(theirs, ui) }) } : {}}
          value={value}
          onChange={(v) => v && preview.setTarget(v === theirs ? "" : v)}
          placeholder={t("inbox.choose_language")}
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
          <UnstyledButton fz="xs" c="brand" onClick={preview.retry}>
            {t("inbox.retry")}
          </UnstyledButton>
          <UnstyledButton fz="xs" c="brand" onClick={onSendOriginal}>
            {t("inbox.send_original")}
          </UnstyledButton>
        </Group>
      )}
      {!off && !preview.error && shown?.needed && (
        <Box mt={6} className="inbox-preview-text">
          <Text size="md" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
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
