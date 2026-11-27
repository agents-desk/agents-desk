import type { ConversationTicketDTO, ConversationTranslation, MemberTenantMember } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Box, FileButton, Group, SegmentedControl, Text, Textarea, Tooltip } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconLock, IconPaperclip, IconWorld } from "@tabler/icons-react"
import { notifyError, notifySuccess, TriggerMenu, textTriggerRule, useCan, useLanguage, useT, useTextTrigger, useUploadQueue } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { tenantApi } from "../api"
import {
  ATTACHMENT_ACCEPT,
  AttachmentStrip,
  attachmentMaxSize,
  MAX_ATTACHMENTS,
  toAttachmentInput,
  type UploadedMedia,
  uploadMediaFile,
} from "../inbox/Attachments"
import {
  ComposerSendArea,
  InlineTranslationPane,
  resolveInternalTargetLanguage,
  useComposerTranslation,
} from "../inbox/ComposerTranslation"
import { useMe, useMembers, useTimeline } from "../inbox/data"
import { clearDraft, getDraft, saveDraft } from "../inbox/drafts"
import { languageName, sameLanguage } from "../languages"
import { isTypingTarget } from "../layout"
import { invalidateTicket, mergeTimelineItem } from "./meta"

/** TicketComposerMode 是工单输入区的两种模式：内部讨论备注或发布对外公开进度。 */
type TicketComposerMode = "note" | "public"

/**
 * TicketComposer 是工单底部的双模式输入卡片：
 * - 内部讨论（默认）：中性表面底色，仅成员可见，支持 @ 提及同事、附件和跨语言成员自动互译；
 * - 公开进度：品牌浅底提示，仅在工单 visibility=all 时可用，发送后按各关联客户语言去重翻译并同步到所有关联客户会话。
 */
export function TicketComposer({ ticket, onTyping }: { ticket: ConversationTicketDTO; onTyping: () => void }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const can = useCan()
  const os = useOs()
  const isMac = os === "macos" || os === "ios"
  const modeKey = isMac ? "⌘I" : "Ctrl+I"
  const sendModKey = isMac ? "⌘ + ↵" : "Ctrl + Enter"
  const queryClient = useQueryClient()
  const me = useMe()
  const ctrlEnterSend = me.data?.send_shortcut === "ctrl_enter"
  const { list: members, byId: membersById } = useMembers()
  const timeline = useTimeline(ticket.id)

  const canPublish = ticket.visibility === "all"
  // 未发送的内容按工单保存为草稿（与收件箱共用：公开进度记为 reply，内部讨论记为 note）
  const [mode, setMode] = useState<TicketComposerMode>(() => (getDraft(ticket.id)?.mode === "reply" && canPublish ? "public" : "note"))
  const [text, setText] = useState(() => getDraft(ticket.id)?.text ?? "")
  const [mentionIds, setMentionIds] = useState<string[]>(() => getDraft(ticket.id)?.mentionIds ?? [])
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const area = useRef<HTMLTextAreaElement>(null)
  const resetFile = useRef<() => void>(null)

  useEffect(() => {
    saveDraft(ticket.id, { text, mode: mode === "public" ? "reply" : "note", mentionIds })
  }, [ticket.id, text, mode, mentionIds])

  // 工单被切回仅成员可见时自动切回内部讨论模式
  useEffect(() => {
    if (!canPublish && mode === "public") setMode("note")
  }, [canPublish, mode])

  const queue = useUploadQueue<UploadedMedia>({
    upload: (file) => uploadMediaFile(file, (f) => tenantApi.file.upload({ file: f })),
    accept: ATTACHMENT_ACCEPT,
    maxFiles: MAX_ATTACHMENTS,
    maxSize: attachmentMaxSize,
    onError: (message) => notifications.show({ color: "red", message }),
  })
  const attachmentInputs = queue.ready.map(toAttachmentInput)
  const value = text.trim()
  const hasContent = Boolean(value) || attachmentInputs.length > 0
  const note = mode === "note"

  // 内部讨论：严格按真实接收者推导（@ 提及同事 → 工单发起人、负责人、时间线参与成员），不扫描全工作区无关成员
  const noteDefaultTarget = useMemo(() => {
    const directIds: (string | undefined)[] = [
      ticket.created_by,
      ticket.assignee?.type === "member" ? ticket.assignee.id : undefined,
      ...(timeline.data ?? []).filter((it) => it.actor.type === "member").map((it) => it.actor.id),
    ]
    return resolveInternalTargetLanguage({
      mentionIds,
      directMemberIds: directIds,
      membersById,
      myId: me.data?.id,
      myLang: me.data?.language,
    })
  }, [ticket.created_by, ticket.assignee, timeline.data, mentionIds, membersById, me.data?.id, me.data?.language])

  // 公开进度：严格按关联受影响客户语言去重，不回退内部成员语言
  const publicPeerLanguages = useMemo(() => {
    const seen: string[] = []
    for (const lc of ticket.linked_contacts ?? []) {
      const l = lc.contact.language?.trim()
      if (!l || sameLanguage(l, me.data?.language)) continue
      if (!seen.some((x) => sameLanguage(x, l))) seen.push(l)
    }
    return seen
  }, [ticket.linked_contacts, me.data?.language])

  const publicDefaultTarget = publicPeerLanguages[0] || ""

  const preview = useComposerTranslation({
    conversationId: ticket.id,
    text: value,
    defaultTarget: note ? noteDefaultTarget : publicDefaultTarget,
    myLang: me.data?.language,
    mode: "verified",
    previewMode: note ? "internal" : "outbound",
    mentions: note ? mentionIds : undefined,
  })

  const send = useMutation({
    mutationFn: async ({ translation, sendMode }: { translation?: ConversationTranslation; sendMode: TicketComposerMode }) => {
      const attachments = attachmentInputs.length > 0 ? attachmentInputs : undefined
      if (sendMode === "note") {
        const ids = members.filter((m) => mentionIds.includes(m.id) && value.includes(`@${m.name}`)).map((m) => m.id)
        const item = await tenantApi.ticket.note(ticket.id, {
          text: value || undefined,
          mentions: ids.length > 0 ? ids : undefined,
          attachments,
          translation,
        })
        return { sentMode: "note" as const, item }
      }
      const item = await tenantApi.ticket.publishUpdate(ticket.id, {
        text: value || undefined,
        attachments,
        translation,
      })
      return { sentMode: "public" as const, item }
    },
    onSuccess: ({ sentMode, item }) => {
      clearDraft(ticket.id)
      setText("")
      setMentionIds([])
      queue.clear()
      preview.reset()
      mergeTimelineItem(queryClient, ticket.id, item)
      if (sentMode === "public") notifySuccess(t("tickets.public_sent"))
      invalidateTicket(queryClient, ticket.id)
    },
    onError: (err) => notifyError(err),
  })

  const submit = async (options?: { skipTranslation?: boolean }) => {
    if (!hasContent || queue.uploading || queue.hasError || send.isPending || preview.pending) return
    const prep = await preview.prepareSubmit({ skipTranslation: options?.skipTranslation })
    if (!prep.send) return
    send.mutate({ translation: prep.translation, sendMode: mode })
  }

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
    ],
    [note, members, me.data?.id],
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

  const switchMode = useCallback(
    (nextMode: TicketComposerMode) => {
      if (nextMode === mode || (nextMode === "public" && !canPublish)) return
      setMode(nextMode)
      preview.setTarget("")
      preview.reset()
      trigger.close()
    },
    [canPublish, mode, preview, trigger],
  )

  const focusEnd = useCallback(() => {
    requestAnimationFrame(() => {
      const el = area.current
      if (!el) return
      el.focus()
      const end = el.value.length
      el.setSelectionRange(end, end)
    })
  }, [])

  // 非输入态按 N 聚焦内部讨论，可公开时按 R 切到公开进度并聚焦到末尾
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if (key === "n" || (key === "r" && !canPublish)) {
        e.preventDefault()
        switchMode("note")
        focusEnd()
      } else if (key === "r") {
        e.preventDefault()
        switchMode("public")
        focusEnd()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [canPublish, focusEnd, switchMode])

  if (!can("conversations:reply")) return null

  const canSubmit = hasContent && !queue.uploading && !queue.hasError && !send.isPending && !preview.pending
  const sendHint = ctrlEnterSend ? t("inbox.ctrl_enter_hint", { key: sendModKey }) : t("inbox.enter_hint")
  const mainLabel = preview.confirming ? t("inbox.confirm_send") : t(note ? "inbox.add_note" : "tickets.public_send")

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
      <Box className="inbox-composer-box" data-public={!note || undefined} data-dragging={dragging || undefined}>
        <TriggerMenu trigger={trigger} position="top" width="min(100%, 280px)" maxHeight={240} />
        <Group gap="xs" className="inbox-composer-head" data-tn-density="inspector" wrap="wrap">
          <Tooltip label={t("tickets.switch_mode_hint", { key: modeKey })} disabled={!canPublish} openDelay={350}>
            <SegmentedControl
              value={mode}
              onChange={(v) => switchMode(v as TicketComposerMode)}
              data={[
                { value: "note", label: t("tickets.mode_note") },
                { value: "public", label: t("tickets.mode_public"), disabled: !canPublish },
              ]}
            />
          </Tooltip>
          {note ? (
            <Group gap={4} wrap="nowrap">
              <IconLock size={12} color="var(--mantine-color-dimmed)" style={{ flex: "none" }} />
              <Text size="xs" c="dimmed">
                {canPublish ? t("inbox.note_hint") : t("tickets.public_disabled_hint")}
              </Text>
            </Group>
          ) : (
            <Group gap={4} wrap="nowrap">
              <IconWorld size={14} color="var(--mantine-primary-color-filled)" style={{ flex: "none" }} />
              <Text size="xs" c="brand" fw={500}>
                {ticket.linked_count > 0
                  ? t("tickets.public_composer_hint", { n: ticket.linked_count })
                  : t("tickets.public_composer_no_links")}
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
              onPaste={(e) => queue.handlePaste(e)}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return
                if (canPublish && (e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "i") {
                  e.preventDefault()
                  switchMode(mode === "note" ? "public" : "note")
                  return
                }
                if (trigger.handleKeyDown(e)) return
                const hitSend = ctrlEnterSend
                  ? e.key === "Enter" && (e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey
                  : e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey
                if (hitSend && canSubmit) {
                  e.preventDefault()
                  void submit()
                }
              }}
              placeholder={t(note ? "tickets.note_placeholder" : "tickets.public_placeholder")}
              autosize
              minRows={3}
              maxRows={10}
              maxLength={10000}
              aria-label={t(note ? "tickets.mode_note" : "tickets.mode_public")}
            />
          </Box>
          {preview.expanded && (
            <InlineTranslationPane
              hasText={Boolean(value)}
              myLang={me.data?.language}
              preview={preview}
              ctrlEnterSend={ctrlEnterSend}
              onSendWithoutTranslation={() => void submit({ skipTranslation: true })}
              onSubmit={() => void submit()}
            />
          )}
        </Box>

        <Group gap={6} className="inbox-composer-foot" data-tn-density="dense">
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

          {!note && preview.expanded && publicPeerLanguages.length > 1 && (
            <Text size="xs" c="dimmed">
              {t("tickets.public_multi_lang_hint")}
            </Text>
          )}
          {preview.confirming && (
            <Text size="xs" c="brand" ml={4}>
              {ctrlEnterSend ? t("inbox.ctrl_enter_again", { key: sendModKey }) : t("inbox.enter_again")}
            </Text>
          )}

          <ComposerSendArea
            preview={preview}
            myLang={me.data?.language}
            hintLabel={
              note
                ? undefined
                : publicPeerLanguages.length > 1
                  ? t("tickets.auto_translate_public_multi", { n: publicPeerLanguages.length })
                  : publicPeerLanguages.length === 1
                    ? t("inbox.auto_translate_to", { lang: languageName(publicPeerLanguages[0], ui) })
                    : t("tickets.auto_translate_public")
            }
            label={mainLabel}
            sendHint={sendHint}
            disabled={!canSubmit}
            loading={send.isPending || preview.pending}
            onSend={() => void submit()}
          />
        </Group>
      </Box>
    </Box>
  )
}
