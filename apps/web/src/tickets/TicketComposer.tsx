import type { ConversationTicketDTO, MemberTenantMember } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Box, Button, FileButton, Group, SegmentedControl, Text, Textarea, Tooltip } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { notifications } from "@mantine/notifications"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconLock, IconPaperclip, IconSend, IconWorld } from "@tabler/icons-react"
import { notifyError, notifySuccess, TriggerMenu, textTriggerRule, useCan, useT, useTextTrigger, useUploadQueue } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import {
  ATTACHMENT_ACCEPT,
  AttachmentStrip,
  attachmentMaxSize,
  MAX_ATTACHMENTS,
  toAttachmentInput,
  type UploadedMedia,
  uploadMediaFile,
} from "../inbox/Attachments"
import { useMe, useMembers } from "../inbox/data"

/** TicketComposerMode 是工单输入区的两种模式：内部讨论备注或发布对外公开进度。 */
type TicketComposerMode = "note" | "public"

/**
 * TicketComposer 是工单底部的双模式输入卡片：
 * - 内部讨论（默认）：暖黄底，仅成员可见，支持 @ 提及同事、附件和跨语言成员自动互译；
 * - 公开进度：仅在工单 visibility=all 时可用，发送后按各关联客户语言去重翻译并同步到所有关联客户会话。
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
  const [mode, setMode] = useState<TicketComposerMode>("note")
  const [text, setText] = useState("")
  const [mentionIds, setMentionIds] = useState<string[]>([])
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const area = useRef<HTMLTextAreaElement>(null)
  const resetFile = useRef<() => void>(null)

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

  const send = useMutation({
    mutationFn: async () => {
      const attachments = attachmentInputs.length > 0 ? attachmentInputs : undefined
      if (note) {
        const ids = members.filter((m) => mentionIds.includes(m.id) && value.includes(`@${m.name}`)).map((m) => m.id)
        await tenantApi.conversation.note(ticket.id, {
          text: value || undefined,
          mentions: ids.length > 0 ? ids : undefined,
          attachments,
        })
        return "note" as const
      }
      await tenantApi.ticket.publishUpdate(ticket.id, {
        text: value || undefined,
        attachments,
      })
      return "public" as const
    },
    onSuccess: (sentMode) => {
      setText("")
      setMentionIds([])
      queue.clear()
      if (sentMode === "public") notifySuccess(t("tickets.public_sent"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
    },
    onError: (err) => notifyError(err),
  })

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

  const canSubmit = hasContent && !queue.uploading && !queue.hasError && !send.isPending
  const sendHint = ctrlEnterSend ? t("inbox.ctrl_enter_hint", { key: sendModKey }) : t("inbox.enter_hint")

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
      <Box className="inbox-composer-box" data-note={note || undefined} data-dragging={dragging || undefined}>
        <TriggerMenu trigger={trigger} position="top" width="min(100%, 280px)" maxHeight={240} />
        <Group gap="xs" className="inbox-composer-head" data-tn-density="inspector" wrap="wrap">
          <SegmentedControl
            value={mode}
            onChange={(v) => {
              setMode(v as TicketComposerMode)
              trigger.close()
            }}
            data={[
              { value: "note", label: t("tickets.mode_note") },
              { value: "public", label: t("tickets.mode_public"), disabled: !canPublish },
            ]}
          />
          {note ? (
            <Group gap={4} wrap="nowrap">
              <IconLock size={12} className="inbox-note-accent" style={{ flex: "none" }} />
              <Text size="xs" className="inbox-note-accent">
                {canPublish ? t("inbox.note_hint") : t("tickets.public_disabled_hint")}
              </Text>
            </Group>
          ) : (
            <Group gap={4} wrap="nowrap">
              <IconWorld size={13} color="var(--mantine-primary-color-filled)" style={{ flex: "none" }} />
              <Text size="xs" c="brand">
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
                send.mutate()
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

          <Tooltip label={sendHint} openDelay={350}>
            <Button
              ml="auto"
              color={note ? "yellow" : undefined}
              leftSection={note ? <IconLock size={14} /> : <IconSend size={14} />}
              disabled={!canSubmit}
              loading={send.isPending}
              onClick={() => send.mutate()}
            >
              {t(note ? "inbox.add_note" : "tickets.public_send")}
            </Button>
          </Tooltip>
        </Group>
      </Box>
    </Box>
  )
}
