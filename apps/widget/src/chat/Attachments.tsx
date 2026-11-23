import type { ConversationAttachmentDTO, ConversationPreviewAttachment } from "@agents-desk/client/widget"
import { ActionIcon, Box, Group, Loader, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { IconAlertCircle, IconDownload, IconFile, IconFileSpreadsheet, IconFileText, IconFileZip, IconRefresh, IconX } from "@tabler/icons-react"
import { formatFileSize, type UploadQueueItem, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { delegatesOpenLink, openLink } from "../host"

/** ATTACHMENT_ACCEPT 是聊天窗口允许选择的图片和常用文件类型，与服务端白名单一致。 */
export const ATTACHMENT_ACCEPT =
  "image/png,image/jpeg,image/webp,image/gif,.pdf,.docx,.xlsx,.pptx,.doc,.xls,.ppt,.txt,.csv,.md,.log,.json,.zip"

/** MAX_ATTACHMENTS 是单条消息最多附带的附件数量。 */
export const MAX_ATTACHMENTS = 10

/** MAX_IMAGE_SIZE 是单张图片附件的最大体积（10 MiB）。 */
const MAX_IMAGE_SIZE = 10 * 1024 * 1024

/** MAX_FILE_SIZE 是单个非图片文件附件的最大体积（20 MiB）。 */
const MAX_FILE_SIZE = 20 * 1024 * 1024

/** attachmentMaxSize 返回文件允许的最大字节数：图片 10 MiB，其他文件 20 MiB。 */
export function attachmentMaxSize(file: File): number {
  return file.type.startsWith("image/") ? MAX_IMAGE_SIZE : MAX_FILE_SIZE
}

/** LightboxImage 是灯箱正在预览的原图信息。 */
export interface LightboxImage {
  src: string
  filename?: string
}

/** widgetPreviewSummary 拼出聊天窗口首页会话列表的最后一条消息摘要。 */
export function widgetPreviewSummary(
  text: string,
  attachment: ConversationPreviewAttachment | undefined,
  t: ReturnType<typeof useT>,
): string {
  if (!attachment) return text
  const label = attachment.kind === "image" ? t("chat.preview_image") : t("chat.preview_file")
  if (!text.trim()) return attachment.count > 1 ? `${label} ×${attachment.count}` : label
  return `${label} ${text}`
}

/** fileIcon 按文件类型和扩展名返回合适的文档图标。 */
function fileIcon(contentType: string, filename: string, size = 16): ReactNode {
  const ct = contentType.toLowerCase()
  const name = filename.toLowerCase()
  if (ct.includes("zip") || name.endsWith(".zip")) return <IconFileZip size={size} stroke={1.6} />
  if (ct.includes("spreadsheet") || ct.includes("excel") || ct === "text/csv" || /\.(xlsx?|csv)$/.test(name)) {
    return <IconFileSpreadsheet size={size} stroke={1.6} />
  }
  if (ct.includes("pdf") || ct.includes("word") || ct.startsWith("text/") || ct === "application/json" || /\.(pdf|docx?|txt|md|log|json)$/.test(name)) {
    return <IconFileText size={size} stroke={1.6} />
  }
  return <IconFile size={size} stroke={1.6} />
}

/**
 * WidgetAttachmentStrip 是聊天窗口输入框上方的待发送附件队列条。
 */
export function WidgetAttachmentStrip<T>({
  items,
  onRemove,
  onRetry,
}: {
  items: UploadQueueItem<T>[]
  onRemove: (id: string) => void
  onRetry: (id: string) => void
}): ReactNode {
  const t = useT()
  if (items.length === 0) return null
  return (
    <Group gap={6} px="xs" pt="xs" wrap="wrap" className="w-attach-strip">
      {items.map((it) =>
        it.preview ? (
          <Box key={it.id} className="w-attach-thumb" data-error={it.status === "error" || undefined}>
            <img src={it.preview} alt={it.file.name} />
            {it.status === "uploading" && (
              <Box className="w-attach-overlay">
                <Loader size={14} color="white" />
              </Box>
            )}
            {it.status === "error" && (
              <Tooltip label={it.error || t("composer.attach_retry")} multiline maw={220}>
                <UnstyledButton
                  className="w-attach-overlay w-attach-overlay-error"
                  onClick={() => onRetry(it.id)}
                  aria-label={t("composer.attach_retry")}
                >
                  <IconRefresh size={14} />
                </UnstyledButton>
              </Tooltip>
            )}
            <ActionIcon
              size={18}
              radius="xl"
              variant="filled"
              color="dark"
              className="w-attach-remove"
              onClick={() => onRemove(it.id)}
              aria-label={t("composer.attach_remove")}
            >
              <IconX size={10} />
            </ActionIcon>
          </Box>
        ) : (
          <Group key={it.id} gap={6} wrap="nowrap" px={8} py={4} className="w-attach-file" data-error={it.status === "error" || undefined}>
            <Box className="w-attach-icon" c={it.status === "error" ? "red" : "dimmed"}>
              {it.status === "uploading" ? (
                <Loader size={13} />
              ) : it.status === "error" ? (
                <IconAlertCircle size={15} />
              ) : (
                fileIcon(it.file.type, it.file.name, 15)
              )}
            </Box>
            <Box style={{ minWidth: 0, flex: 1 }}>
              <Text size="xs" fw={500} truncate>
                {it.file.name}
              </Text>
              <Text size="xs" c={it.status === "error" ? "red" : "dimmed"} truncate>
                {it.status === "error" ? (it.error ?? formatFileSize(it.file.size)) : formatFileSize(it.file.size)}
              </Text>
            </Box>
            {it.status === "error" && (
              <ActionIcon size="xs" variant="subtle" color="red" onClick={() => onRetry(it.id)} aria-label={t("composer.attach_retry")}>
                <IconRefresh size={12} />
              </ActionIcon>
            )}
            <ActionIcon size="xs" variant="subtle" color="gray" onClick={() => onRemove(it.id)} aria-label={t("composer.attach_remove")}>
              <IconX size={12} />
            </ActionIcon>
          </Group>
        ),
      )}
    </Group>
  )
}

/**
 * WidgetMessageAttachments 渲染聊天窗口消息气泡中的图片缩略图与文件下载卡片。
 */
export function WidgetMessageAttachments({
  attachments,
  hasText,
  mine,
  onPreview,
}: {
  attachments?: ConversationAttachmentDTO[]
  hasText: boolean
  mine: boolean
  onPreview: (image: LightboxImage) => void
}): ReactNode {
  const t = useT()
  if (!attachments || attachments.length === 0) return null
  const images = attachments.filter((a) => a.kind === "image")
  const files = attachments.filter((a) => a.kind !== "image")
  const cols = images.length === 1 ? 1 : images.length === 2 || images.length === 4 ? 2 : 3

  return (
    <Stack gap={6} mt={hasText ? 6 : 0}>
      {images.length > 0 && (
        <Box className="w-msg-images" data-cols={cols}>
          {images.map((img) => {
            const singleStyle =
              images.length === 1 && img.width && img.height ? { aspectRatio: `${img.width} / ${img.height}` } : undefined
            return (
              <UnstyledButton
                key={img.file_id}
                className="w-msg-img"
                data-single={images.length === 1 || undefined}
                style={singleStyle}
                onClick={() => onPreview({ src: img.url, filename: img.filename })}
                aria-label={t("chat.view_image")}
              >
                <img src={img.thumb_url || img.url} alt={img.filename} loading="lazy" />
              </UnstyledButton>
            )
          })}
        </Box>
      )}
      {files.length > 0 && (
        <Stack gap={4}>
          {files.map((f) => (
            <a
              key={f.file_id}
              href={f.url}
              download={f.filename}
              target="_blank"
              rel="noopener noreferrer"
              className="w-msg-file"
              data-mine={mine || undefined}
              aria-label={t("chat.download_file")}
              onClick={(e) => {
                if (delegatesOpenLink()) {
                  e.preventDefault()
                  openLink(f.url)
                }
              }}
            >
              <Box className="w-attach-icon">{fileIcon(f.content_type, f.filename, 17)}</Box>
              <Box style={{ minWidth: 0, flex: 1 }}>
                <Text size="xs" fw={500} truncate>
                  {f.filename}
                </Text>
                <Text size="xs" className="w-msg-file-size">
                  {formatFileSize(f.size)}
                </Text>
              </Box>
              <Box className="w-attach-icon">
                <IconDownload size={14} />
              </Box>
            </a>
          ))}
        </Stack>
      )}
    </Stack>
  )
}
