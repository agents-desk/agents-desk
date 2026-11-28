import type { ConversationAttachmentDTO, ConversationAttachmentInput } from "@agents-desk/client/widget"
import { ActionIcon, Box, FileButton, Group, Text, Textarea, Tooltip } from "@mantine/core"
import { IconPaperclip, IconSend, IconX } from "@tabler/icons-react"
import { useT, useUploadQueue } from "@go-tenon/ui/core"
import { type ReactNode, useRef, useState } from "react"
import { authed, widgetApi } from "../api"
import {
  ATTACHMENT_ACCEPT,
  attachmentMaxSize,
  MAX_ATTACHMENTS,
  toOptimisticAttachment,
  toWidgetAttachmentInput,
  uploadWidgetMediaFile,
  WidgetAttachmentStrip,
  type WidgetUploadedMedia,
} from "./Attachments"

/** ComposerProps 是输入框的设置。 */
interface ComposerProps {
  color: string
  touch: boolean // 手机和 App：回车换行，用按钮发送；电脑：回车发送，Shift + 回车换行
  placeholder: string
  allowAttachments?: boolean // 预览模式关闭附件上传，仅保留文字发送体验
  onSend: (text: string, inputs: ConversationAttachmentInput[], previews: ConversationAttachmentDTO[]) => void
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
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const queue = useUploadQueue<WidgetUploadedMedia>({
    upload: (file) => uploadWidgetMediaFile(file, (f) => authed(() => widgetApi.file.upload({ file: f }))),
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
    const inputs = queue.ready.map(toWidgetAttachmentInput)
    const previews = queue.ready.map(toOptimisticAttachment)
    onSend(value, inputs, previews)
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
        <Box
          className="w-composer-field"
          data-attach={allowAttachments || undefined}
          onClick={(e) => {
            if (e.target === e.currentTarget) textareaRef.current?.focus()
          }}
        >
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
                <Tooltip label={t("composer.attach")} disabled={touch} openDelay={400}>
                  <ActionIcon
                    {...props}
                    size={34}
                    radius="xl"
                    variant="subtle"
                    color="gray"
                    className="w-composer-attach"
                    aria-label={t("composer.attach")}
                  >
                    <IconPaperclip size={18} />
                  </ActionIcon>
                </Tooltip>
              )}
            </FileButton>
          )}
          <Textarea
            ref={textareaRef}
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
            variant="unstyled"
            autosize
            minRows={1}
            maxRows={5}
            size="md"
            maxLength={10000}
            classNames={{ root: "w-composer-textarea", input: "w-composer-input" }}
            aria-label={placeholder}
          />
        </Box>
        <ActionIcon size={42} radius="xl" color={color} variant="filled" disabled={!canSend} onClick={send} aria-label={t("composer.send")}>
          <IconSend size={18} />
        </ActionIcon>
      </Group>
    </Box>
  )
}
