import type { ConversationAttachmentDTO, File as UploadedFile } from "@agents-desk/client/widget"
import { ActionIcon, Box, FileButton, Group, Text, Textarea } from "@mantine/core"
import { IconPaperclip, IconSend, IconX } from "@tabler/icons-react"
import { useT, useUploadQueue } from "@go-tenon/ui"
import { type ReactNode, useRef, useState } from "react"
import { authed, widgetApi } from "../api"
import { ATTACHMENT_ACCEPT, attachmentMaxSize, MAX_ATTACHMENTS, WidgetAttachmentStrip } from "./Attachments"

/** ComposerProps 是输入框的设置。 */
interface ComposerProps {
  color: string
  touch: boolean // 手机和 App：回车换行，用按钮发送；电脑：回车发送，Shift + 回车换行
  placeholder: string
  allowAttachments?: boolean // 预览模式关闭附件上传，仅保留文字发送体验
  onSend: (text: string, attachments: ConversationAttachmentDTO[]) => void
  onTyping?: () => void // 客户在输入：会话页据此告诉成员“正在输入”
}

/** Composer 是底部的输入框、附件上传与发送按钮。字号 16px，iOS 点进去时页面不会放大；底部留出横条的安全区域。 */
export function Composer({ color, touch, placeholder, allowAttachments = true, onSend, onTyping }: ComposerProps): ReactNode {
  const t = useT()
  const [text, setText] = useState("")
  const [uploadError, setUploadError] = useState<string | null>(null)
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const resetFile = useRef<() => void>(null)
  const queue = useUploadQueue<UploadedFile>({
    upload: (file) => authed(() => widgetApi.file.upload({ file })),
    accept: ATTACHMENT_ACCEPT,
    maxFiles: MAX_ATTACHMENTS,
    maxSize: attachmentMaxSize,
    onError: (msg) => setUploadError(msg),
  })
  const hasContent = Boolean(text.trim()) || queue.ready.length > 0
  const canSend = hasContent && !queue.uploading && !queue.hasError

  const send = () => {
    if (!canSend) return
    const value = text.trim()
    const attachments: ConversationAttachmentDTO[] = queue.ready.map((f) => {
      const isImage = f.content_type.startsWith("image/")
      return {
        file_id: f.id,
        kind: isImage ? "image" : "file",
        filename: f.filename,
        content_type: f.content_type,
        size: f.size,
        url: f.url,
        thumb_url: isImage ? f.url : undefined,
      }
    })
    onSend(value, attachments)
    setText("")
    setUploadError(null)
    queue.clear()
  }

  return (
    <Box
      className="w-composer w-safe-bottom"
      data-dragging={dragging || undefined}
      style={{ flex: "none" }}
      onDragEnter={(e) => {
        if (!allowAttachments || !e.dataTransfer.types.includes("Files")) return
        e.preventDefault()
        dragDepth.current++
        setDragging(true)
      }}
      onDragOver={(e) => {
        if (!allowAttachments || !e.dataTransfer.types.includes("Files")) return
        e.preventDefault()
      }}
      onDragLeave={() => {
        if (!allowAttachments || dragDepth.current === 0) return
        dragDepth.current--
        if (dragDepth.current === 0) setDragging(false)
      }}
      onDrop={(e) => {
        if (!allowAttachments) return
        dragDepth.current = 0
        setDragging(false)
        setUploadError(null)
        queue.handleDrop(e)
      }}
    >
      {allowAttachments && dragging && (
        <Text size="xs" c="dimmed" fw={500} ta="center" pt={6}>
          {t("composer.attach_drop")}
        </Text>
      )}
      {allowAttachments && uploadError && (
        <Group gap={6} px="xs" pt={6} wrap="nowrap">
          <Text size="xs" c="red" style={{ flex: 1 }}>
            {uploadError}
          </Text>
          <ActionIcon size="xs" variant="subtle" color="gray" onClick={() => setUploadError(null)} aria-label={t("composer.attach_remove")}>
            <IconX size={12} />
          </ActionIcon>
        </Group>
      )}
      {allowAttachments && <WidgetAttachmentStrip items={queue.items} onRemove={queue.remove} onRetry={queue.retry} />}
      <Group gap="xs" align="flex-end" wrap="nowrap" p="xs">
        {allowAttachments && (
          <FileButton
            resetRef={resetFile}
            multiple
            accept={ATTACHMENT_ACCEPT}
            onChange={(files) => {
              if (files.length > 0) {
                setUploadError(null)
                queue.add(files)
              }
              resetFile.current?.()
            }}
          >
            {(props) => (
              <ActionIcon {...props} size={42} radius="xl" variant="subtle" color="gray" aria-label={t("composer.attach")}>
                <IconPaperclip size={18} />
              </ActionIcon>
            )}
          </FileButton>
        )}
        <Textarea
          value={text}
          onChange={(e) => {
            setText(e.currentTarget.value)
            if (e.currentTarget.value.trim()) onTyping?.()
          }}
          onPaste={(e) => {
            if (allowAttachments && queue.handlePaste(e)) setUploadError(null)
          }}
          onKeyDown={(e) => {
            if (!touch && e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault()
              send()
            }
          }}
          placeholder={placeholder}
          autosize
          minRows={1}
          maxRows={5}
          radius="xl"
          size="md"
          maxLength={10000}
          style={{ flex: 1 }}
          aria-label={placeholder}
        />
        <ActionIcon size={42} radius="xl" color={color} variant="filled" disabled={!canSend} onClick={send} aria-label={t("composer.send")}>
          <IconSend size={18} />
        </ActionIcon>
      </Group>
      {!touch && (
        <Text size="xs" c="dimmed" ta="center" pb={6}>
          {t("composer.hint")}
        </Text>
      )}
    </Box>
  )
}
