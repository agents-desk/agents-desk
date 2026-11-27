import type { ConversationTicketDTO, ConversationTranslation, MemberTenantMember } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Box, FileButton, Group, SegmentedControl, Text, Textarea, Tooltip } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconLock, IconPaperclip, IconWorld } from "@tabler/icons-react"
import { notifyError, notifySuccess, TriggerMenu, textTriggerRule, useCan, useT, useTextTrigger, useUploadQueue } from "@go-tenon/ui"
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
  defaultPeerLanguage,
  InlineTranslationPane,
  useComposerTranslation,
} from "../inbox/ComposerTranslation"
import { useMe, useMembers } from "../inbox/data"
import { clearDraft, getDraft, saveDraft } from "../inbox/drafts"
import { sameLanguage } from "../languages"
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
  const can = useCan()
  const os = useOs()
  const isMac = os === "macos" || os === "ios"
  const sendModKey = isMac ? "⌘ + ↵" : "Ctrl + Enter"
  const queryClient = useQueryClient()
  const me = useMe()
  const ctrlEnterSend = me.data?.send_shortcut === "ctrl_enter"
  const { list: members } = useMembers()

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

  // 非输入态按 N 聚焦内部讨论，可公开时按 R 切到公开进度并聚焦
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if (key === "n" || (key === "r" && !canPublish)) {
        e.preventDefault()
        setMode("note")
        area.current?.focus()
      } else if (key === "r") {
        e.preventDefault()
        setMode("public")
        area.current?.focus()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [canPublish])

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

  const noteDefaultTarget = useMemo(() => {
    const mentioned = members.filter((m) => mentionIds.includes(m.id) && m.id !== me.data?.id).map((m) => m.language)
    return (
      defaultPeerLanguage(mentioned, me.data?.language) ||
      defaultPeerLanguage(
        members.filter((m) => m.id !== me.data?.id).map((m) => m.language),
        me.data?.language,
      )
    )
  }, [members, mentionIds, me.data?.id, me.data?.language])

  const publicPeerLanguages = useMemo(() => {
    const seen: string[] = []
    for (const lc of ticket.linked_contacts ?? []) {
      const l = lc.contact.language?.trim()
      if (!l || sameLanguage(l, me.data?.language)) continue
      if (!seen.some((x) => sameLanguage(x, l))) seen.push(l)
    }
    return seen
  }, [ticket.linked_contacts, me.data?.language])

  const publicDefaultTarget = useMemo(
    () => publicPeerLanguages[0] || noteDefaultTarget,
    [publicPeerLanguages, noteDefaultTarget],
  )

  const preview = useComposerTranslation({
    conversationId: ticket.id,
    text: value,
    defaultTarget: note ? noteDefaultTarget : publicDefaultTarget,
    myLang: me.data?.language,
    mode: "verified",
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
          <SegmentedControl
            value={mode}
            onChange={(v) => {
              setMode(v as TicketComposerMode)
              preview.setTarget("")
              preview.reset()
              trigger.close()
            }}
            data={[
              { value: "note", label: t("tickets.mode_note") },
              { value: "public", label: t("tickets.mode_public"), disabled: !canPublish },
            ]}
          />
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
            hintLabel={note ? undefined : t("tickets.auto_translate_public")}
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
