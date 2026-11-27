import type {
  AttributeDTO,
  ChannelExternalMention,
  ConversationActions,
  ConversationAutoTranslateRequest,
  ConversationDTO,
  ConversationItemDTO,
  ConversationTranslation,
  MacroDTO,
  MemberTenantMember,
  TagDTO,
  TeamDTO,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Alert,
  Avatar,
  Badge,
  Box,
  Button,
  Code,
  FileButton,
  Group,
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
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  IconAlertTriangle,
  IconAt,
  IconCheck,
  IconChevronDown,
  IconCornerUpLeft,
  IconLock,
  IconMessageBolt,
  IconPaperclip,
  IconPencil,
  IconX,
} from "@tabler/icons-react"
import {
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
import { languageName, sameLanguage } from "../languages"
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
import {
  ATTACHMENT_ACCEPT,
  AttachmentStrip,
  attachmentMaxSize,
  MAX_ATTACHMENTS,
  toAttachmentInput,
  type UploadedMedia,
  uploadMediaFile,
} from "./Attachments"
import { ChannelIcon } from "./channel-badge"
import {
  ComposerSendArea,
  InlineTranslationPane,
  NO_TRANSLATION,
  resolveInternalTargetLanguage,
  useComposerTranslation,
} from "./ComposerTranslation"
import { useAttributes, useMacros, useMe, useMembers, useTags, useTeams, useTimeline } from "./data"
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

/**
 * Composer 是会话底部的一体化回复卡片：顶栏切换回复与内部备注模式，内部备注切换为淡黄底并支持输入 @ 提及同事，
 * 回复模式输入 / 或点击工具栏按钮搜索并插入快捷回复。
 * 回复与内部备注共用同一套跨语言翻译交互：默认收起自动翻译，点击底栏“译文校对”可在卡片内展开“左原文 | 右译文”对照栏
 * 进行目标语言选择、盲回译核对与轻量手动微调。
 */
export function Composer({
  c,
  replyTo,
  onClearReplyTo,
  correcting,
  onClearCorrecting,
  extMentions,
  onChangeExtMentions,
  onTyping,
  onReplied,
  handleRef,
}: {
  c: ConversationDTO
  replyTo?: ConversationItemDTO | null
  onClearReplyTo?: () => void
  correcting?: ConversationItemDTO | null
  onClearCorrecting?: () => void
  extMentions?: ChannelExternalMention[]
  onChangeExtMentions?: (next: ChannelExternalMention[]) => void
  onTyping: () => void
  onReplied?: () => void
  handleRef?: RefObject<ComposerHandle | null>
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
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

  // 发送渠道选择：默认跟随会话最新的默认回复渠道，坐席可手动切换到联系人已绑定的其他启用渠道
  const defaultReplyChannelId = c.reply_channel?.id ?? c.channel?.id ?? ""
  const availableChannels = c.available_channels ?? []
  const [selectedChannelId, setSelectedChannelId] = useState<string | undefined>(undefined)
  const effectiveChannelId =
    selectedChannelId && availableChannels.some((ch) => ch.id === selectedChannelId)
      ? selectedChannelId
      : defaultReplyChannelId
  const effectiveChannel =
    availableChannels.find((ch) => ch.id === effectiveChannelId) ??
    (c.reply_channel?.id === effectiveChannelId ? c.reply_channel : undefined) ??
    c.channel
  const prevDefaultChannel = useRef(defaultReplyChannelId)
  useEffect(() => {
    if (prevDefaultChannel.current !== defaultReplyChannelId) {
      prevDefaultChannel.current = defaultReplyChannelId
      setSelectedChannelId(undefined)
    }
  }, [defaultReplyChannelId])

  const timeline = useTimeline(c.id)
  const area = useRef<HTMLTextAreaElement>(null)
  const value = text.trim()
  const note = mode === "note"
  const isExternalGroup = Boolean(c.external_group)
  const groupRemoved = c.external_group?.status === "removed"
  const activeExtMentions = extMentions ?? []

  // 汇总群会话时间线中出现过的外部发言人，供坐席快速选择 @提及
  const groupSpeakers = useMemo<ChannelExternalMention[]>(() => {
    if (!isExternalGroup) return []
    const seen = new Map<string, ChannelExternalMention>()
    for (const item of timeline.data ?? []) {
      if (item.actor.type === "external_user" && item.actor.external_id) {
        seen.set(item.actor.external_id, {
          external_id: item.actor.external_id,
          external_user_id: item.actor.id || undefined,
          name: item.actor.name || item.actor.external_id,
        })
      }
    }
    return [...seen.values()]
  }, [isExternalGroup, timeline.data])
  const hasPending = !note && hasPendingActions(pendingActions)
  const translatableText = !value.startsWith("/") ? value : ""
  // 内部备注模式按真实接收者推导（@ 提及同事 → 会话负责人与时间线参与成员）；回复模式默认跟随客户语言
  const noteDefaultTarget = useMemo(() => {
    const directIds: (string | undefined)[] = [
      c.assignee?.type === "member" ? c.assignee.id : undefined,
      ...(timeline.data ?? []).filter((it) => it.actor.type === "member").map((it) => it.actor.id),
    ]
    return resolveInternalTargetLanguage({
      mentionIds,
      directMemberIds: directIds,
      membersById: memberById,
      myId: me.data?.id,
      myLang: me.data?.language,
    })
  }, [c.assignee, timeline.data, mentionIds, memberById, me.data?.id, me.data?.language])
  const preview = useComposerTranslation({
    conversationId: c.id,
    text: translatableText,
    defaultTarget: note ? noteDefaultTarget : (c.language ?? ""),
    myLang: me.data?.language,
    mode: note ? "verified" : "replace",
    previewMode: note ? "internal" : "outbound",
    mentions: note ? mentionIds : undefined,
  })
  const queue = useUploadQueue<UploadedMedia>({
    upload: (file) => uploadMediaFile(file, (f) => tenantApi.file.upload({ file: f })),
    accept: ATTACHMENT_ACCEPT,
    maxFiles: MAX_ATTACHMENTS,
    maxSize: attachmentMaxSize,
    onError: (message) => notifications.show({ color: "red", message }),
  })
  const attachmentInputs = queue.ready.map(toAttachmentInput)
  const hasContent = Boolean(value) || attachmentInputs.length > 0

  // 选定更正目标且输入框为空时，自动带入原消息正文方便直接修改
  const prevCorrectingRef = useRef<string | null>(null)
  useEffect(() => {
    const nextId = correcting?.id ?? null
    if (nextId && nextId !== prevCorrectingRef.current) {
      setText((cur) => (cur.trim() === "" ? (correcting?.content.original || correcting?.content.text || "") : cur))
    }
    prevCorrectingRef.current = nextId
  }, [correcting])

  // 实时保存草稿，切换会话后再回来不丢失已输入内容与模式
  useEffect(() => {
    saveDraft(c.id, { text, mode, mentionIds })
  }, [c.id, text, mode, mentionIds])

  const send = useMutation({
    mutationFn: async ({
      after,
      translation,
      autoTranslate,
      mode: sendMode,
    }: {
      after: After
      translation?: ConversationTranslation
      autoTranslate?: ConversationAutoTranslateRequest
      mode: Mode
    }) => {
      const attachments = attachmentInputs.length > 0 ? attachmentInputs : undefined
      if (sendMode === "note") {
        // 只算还留在文字中的 @
        const ids = members.filter((m) => mentionIds.includes(m.id) && value.includes(`@${m.name}`)).map((m) => m.id)
        await tenantApi.conversation.note(c.id, {
          text: value || undefined,
          mentions: ids.length > 0 ? ids : undefined,
          attachments,
          translation,
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
        const channel_id = selectedChannelId && selectedChannelId !== defaultReplyChannelId ? selectedChannelId : undefined
        await tenantApi.conversation.reply(c.id, {
          text: value || undefined,
          channel_id,
          reply_to_item_id: replyTo?.id,
          corrects_item_id: correcting?.id,
          external_mentions: activeExtMentions.length > 0 ? activeExtMentions : undefined,
          translation,
          auto_translate: autoTranslate,
          attachments,
          actions,
        })
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
      onClearReplyTo?.()
      onClearCorrecting?.()
      onChangeExtMentions?.([])
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

  // 发送：折叠态回复自动翻译发出、备注直发由后台互译；展开校对时发送核对/微调后的译文；skipTranslation 可单次跳过翻译。
  const submit = async (after: After, options?: { skipTranslation?: boolean }) => {
    if (queue.uploading || queue.hasError || send.isPending || preview.pending) return
    if (!note && groupRemoved) return
    if (note) {
      if (!hasContent) return
      const prep = await preview.prepareSubmit({ skipTranslation: options?.skipTranslation })
      if (!prep.send) return
      return send.mutate({ after, translation: prep.translation, mode: "note" })
    }
    if (!value) {
      if (!hasContent && !hasPending && after.kind === "none") return
      return send.mutate({ after, mode: "reply" })
    }
    const prep = await preview.prepareSubmit({ skipTranslation: options?.skipTranslation })
    if (!prep.send) return
    return send.mutate({ after, translation: prep.translation, autoTranslate: prep.autoTranslate, mode: "reply" })
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
              <Group gap={6} wrap="nowrap" align="flex-start">
                {m.body ? (
                  <Text size="xs" c="dimmed" lineClamp={2} lh={1.45} style={{ flex: 1, minWidth: 0 }}>
                    {m.body}
                  </Text>
                ) : (
                  <Text size="xs" c="dimmed" style={{ flex: 1 }}>
                    —
                  </Text>
                )}
                {actionCount > 0 && (
                  <Badge size="xs" variant="dot" color="bright-blue" mt={1} style={{ flex: "none" }}>
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

  const switchMode = (nextMode: Mode) => {
    if (nextMode === mode) return
    setMode(nextMode)
    preview.setTarget("")
    preview.reset()
    trigger.close()
  }

  const toggleMode = () => {
    switchMode(mode === "reply" ? "note" : "reply")
  }

  // 供 R / N 快捷键切换到对应模式并聚焦回复框
  useImperativeHandle(
    handleRef,
    () => ({
      focus: (nextMode?: Mode) => {
        if (nextMode && nextMode !== mode) {
          setMode(nextMode)
          preview.setTarget("")
          preview.reset()
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
    [mode, preview, trigger],
  )

  const canSubmit =
    !queue.uploading &&
    !queue.hasError &&
    !send.isPending &&
    !preview.pending &&
    (note ? hasContent : !groupRemoved && (hasContent || hasPending))
  const sendHint = ctrlEnterSend ? t("inbox.ctrl_enter_hint", { key: sendModKey }) : t("inbox.enter_hint")
  const replyPlaceholder =
    c.status === "closed"
      ? t("inbox.reopen_reply_placeholder")
      : preview.enabled &&
          preview.target !== NO_TRANSLATION &&
          preview.effectiveLang &&
          !sameLanguage(preview.effectiveLang, me.data?.language)
        ? t("inbox.reply_placeholder_auto_translate", { lang: languageName(preview.effectiveLang, ui) })
        : t("inbox.reply_placeholder")
  const willAutoTranslateReply =
    !note &&
    preview.enabled &&
    Boolean(value) &&
    preview.target !== NO_TRANSLATION &&
    ((Boolean(preview.effectiveLang) && !sameLanguage(preview.effectiveLang, me.data?.language)) || Boolean(preview.current?.needed))
  const mainLabel = preview.confirming
    ? t("inbox.confirm_send")
    : note
      ? t("inbox.add_note")
      : !hasContent && hasPending
        ? t("inbox.apply_actions")
        : preview.expanded && preview.current?.needed
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
      {!note && groupRemoved && (
        <Alert variant="light" color="red" radius="md" py={6} px="sm" mb={8} icon={<IconAlertTriangle size={15} />}>
          <Text size="xs">{t("inbox.group_removed_notice")}</Text>
        </Alert>
      )}
      {!note && correcting && (
        <Paper withBorder radius="md" px="xs" py={6} mb={8} bg="var(--mantine-color-deep-orange-light)">
          <Group gap={6} wrap="nowrap">
            <IconPencil size={13} style={{ flex: "none", color: "var(--mantine-color-deep-orange-6)" }} />
            <Text size="xs" c="deep-orange" truncate style={{ flex: 1, minWidth: 0 }}>
              {t("inbox.correcting_message", {
                text: (correcting.content.original || correcting.content.text || "…").slice(0, 60),
              })}
            </Text>
            <ActionIcon size={18} variant="subtle" color="gray" onClick={onClearCorrecting} aria-label={t("inbox.cancel")}>
              <IconX size={12} />
            </ActionIcon>
          </Group>
        </Paper>
      )}
      {!note && isExternalGroup && (replyTo || activeExtMentions.length > 0) && (
        <Paper withBorder radius="md" px="xs" py={6} mb={8} bg="var(--tn-canvas)">
          <Stack gap={4}>
            {replyTo && (
              <Group gap={6} wrap="nowrap">
                <IconCornerUpLeft size={13} style={{ flex: "none", color: "var(--tn-muted)" }} />
                <Text size="xs" c="dimmed" truncate style={{ flex: 1, minWidth: 0 }}>
                  {t("inbox.replying_to", {
                    who: replyTo.actor.name || replyTo.actor.external_id || t("inbox.visitor"),
                    text: (replyTo.content.text || replyTo.content.original || "…").slice(0, 60),
                  })}
                </Text>
                <ActionIcon size={18} variant="subtle" color="gray" onClick={onClearReplyTo} aria-label={t("inbox.cancel")}>
                  <IconX size={12} />
                </ActionIcon>
              </Group>
            )}
            {activeExtMentions.length > 0 && (
              <Group gap={6} wrap="wrap">
                {activeExtMentions.map((m) => (
                  <Badge
                    key={m.external_id}
                    size="sm"
                    variant="light"
                    color="bright-blue"
                    pr={4}
                    rightSection={
                      <ActionIcon
                        size={14}
                        variant="transparent"
                        color="bright-blue"
                        aria-label={t("inbox.cancel")}
                        onClick={() => onChangeExtMentions?.(activeExtMentions.filter((x) => x.external_id !== m.external_id))}
                      >
                        <IconX size={10} />
                      </ActionIcon>
                    }
                  >
                    @{m.name}
                  </Badge>
                ))}
              </Group>
            )}
          </Stack>
        </Paper>
      )}
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
          width={trigger.activeRule?.id === "macro" ? "100%" : "min(100%, 280px)"}
          maxHeight={trigger.activeRule?.id === "macro" ? 320 : 240}
          header={trigger.activeRule?.id === "macro" ? t("inbox.macro_header") : undefined}
          empty={trigger.activeRule?.id === "macro" ? t("inbox.macro_empty") : undefined}
        />
        <Group gap="xs" className="inbox-composer-head" data-tn-density="inspector">
          <Tooltip label={t("inbox.switch_mode_hint", { key: modeKey })} openDelay={350}>
            <SegmentedControl
              value={mode}
              onChange={(v) => switchMode(v as Mode)}
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
          {!note && availableChannels.length > 1 && effectiveChannel && (
            <Menu position="top-start" withinPortal>
              <Menu.Target>
                <Tooltip label={t("inbox.choose_reply_channel")} openDelay={350}>
                  <Button
                    size="compact-xs"
                    variant="light"
                    color="bright-blue"
                    leftSection={<ChannelIcon kind={effectiveChannel.kind} size={12} />}
                    rightSection={<IconChevronDown size={12} />}
                  >
                    {t("inbox.channel_send_via", { channel: effectiveChannel.name })}
                  </Button>
                </Tooltip>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Label>{t("inbox.choose_reply_channel")}</Menu.Label>
                {availableChannels.map((ch) => (
                  <Menu.Item
                    key={ch.id}
                    leftSection={<ChannelIcon kind={ch.kind} size={13} />}
                    rightSection={ch.id === effectiveChannel.id ? <IconCheck size={14} /> : undefined}
                    onClick={() => setSelectedChannelId(ch.id)}
                  >
                    {ch.name}
                  </Menu.Item>
                ))}
              </Menu.Dropdown>
            </Menu>
          )}
          {dragging && (
            <Text size="xs" c="brand" fw={500} ml="auto">
              {t("inbox.attach_drop")}
            </Text>
          )}
        </Group>
        <AttachmentStrip items={queue.items} onRemove={queue.remove} onRetry={queue.retry} />
        <Box className="inbox-composer-main" data-split={preview.expanded || undefined}>
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
              placeholder={note ? t("inbox.note_placeholder") : replyPlaceholder}
              autosize
              minRows={3}
              maxRows={10}
              maxLength={10000}
              aria-label={t(note ? "inbox.mode_note" : "inbox.mode_reply")}
            />
          </Box>
          {preview.expanded && (
            <InlineTranslationPane
              hasText={Boolean(translatableText)}
              myLang={me.data?.language}
              preview={preview}
              ctrlEnterSend={ctrlEnterSend}
              onSendWithoutTranslation={() => void submit({ kind: "none" }, { skipTranslation: true })}
              onSubmit={() => void submit({ kind: "none" })}
            />
          )}
        </Box>
        <Group gap={6} className="inbox-composer-foot" data-tn-density="dense">
          {!note && isExternalGroup && groupSpeakers.length > 0 && onChangeExtMentions && (
            <Menu position="top-start" withinPortal>
              <Menu.Target>
                <Tooltip label={t("inbox.mention_speaker")}>
                  <ActionIcon variant="subtle" color="gray" size="input-sm" aria-label={t("inbox.mention_speaker")}>
                    <IconAt size={16} />
                  </ActionIcon>
                </Tooltip>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Label>{t("inbox.mention_speaker")}</Menu.Label>
                {groupSpeakers.map((sp) => {
                  const picked = activeExtMentions.some((x) => x.external_id === sp.external_id)
                  return (
                    <Menu.Item
                      key={sp.external_id}
                      rightSection={picked ? <IconCheck size={14} /> : undefined}
                      onClick={() => {
                        if (picked) {
                          onChangeExtMentions(activeExtMentions.filter((x) => x.external_id !== sp.external_id))
                        } else {
                          onChangeExtMentions([...activeExtMentions, sp])
                        }
                      }}
                    >
                      {sp.name}
                    </Menu.Item>
                  )
                })}
              </Menu.Dropdown>
            </Menu>
          )}
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
          {preview.confirming && (
            <Text size="xs" c="brand" ml={4}>
              {ctrlEnterSend ? t("inbox.ctrl_enter_again", { key: sendModKey }) : t("inbox.enter_again")}
            </Text>
          )}
          <ComposerSendArea
            preview={preview}
            myLang={me.data?.language}
            label={mainLabel}
            sendHint={sendHint}
            note={note}
            disabled={!canSubmit}
            loading={send.isPending || preview.pending}
            onSend={() => void submit({ kind: "none" })}
            menu={
              !note && (
                <>
                  <Menu.Item onClick={() => void submit({ kind: "none" })}>{t("inbox.send")}</Menu.Item>
                  {willAutoTranslateReply && (
                    <Menu.Item onClick={() => void submit({ kind: "none" }, { skipTranslation: true })}>{t("inbox.send_original")}</Menu.Item>
                  )}
                  <Menu.Item onClick={() => void submit({ kind: "close" })}>{t("inbox.send_close")}</Menu.Item>
                  <Menu.Label>{t("inbox.send_snooze")}</Menu.Label>
                  {snoozeOptions(zone).map((o) => (
                    <Menu.Item key={o.key} onClick={() => void submit({ kind: "snooze", until: o.until })}>
                      {t(`inbox.snooze_${o.key}` as const)}
                    </Menu.Item>
                  ))}
                </>
              )
            }
          />
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


