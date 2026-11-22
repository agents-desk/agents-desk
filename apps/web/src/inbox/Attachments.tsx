import type { ConversationAttachmentDTO, ConversationPreviewAttachment } from "@agents-desk/client/tenant"
import { ActionIcon, Box, Group, Loader, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { IconAlertCircle, IconDownload, IconFile, IconFileSpreadsheet, IconFileText, IconFileZip, IconRefresh, IconX } from "@tabler/icons-react"
import { formatFileSize, type UploadQueueItem, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

/** ATTACHMENT_ACCEPT 是回复框和内部备注允许选择的图片和常用文件类型，与服务端白名单保持一致。 */
export const ATTACHMENT_ACCEPT =
  "image/png,image/jpeg,image/webp,image/gif,.pdf,.docx,.xlsx,.pptx,.doc,.xls,.ppt,.txt,.csv,.md,.log,.json,.zip"

/** MAX_ATTACHMENTS 是单条消息或内部备注最多附带的附件数量。 */
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

/**
 * previewSummary 拼出会话列表和历史记录里的最后一条消息摘要：有附件时按界面语言显示“[图片]”或“[文件]”，
 * 同时带文字时把摘要放在文字前面。
 */
export function previewSummary(
  preview: string,
  attachment: ConversationPreviewAttachment | undefined,
  t: ReturnType<typeof useT>,
): string {
  if (!attachment) return preview
  const label = attachment.kind === "image" ? t("inbox.preview_image") : t("inbox.preview_file")
  if (!preview.trim()) return attachment.count > 1 ? `${label} ×${attachment.count}` : label
  return `${label} ${preview}`
}

/** fileIcon 按文件类型和扩展名返回合适的文档图标。 */
function fileIcon(contentType: string, filename: string, size = 18): ReactNode {
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
 * AttachmentStrip 是回复框上方的待发送附件队列条：图片显示缩略图，文件显示名称和体积，
 * 上传中显示进度指示，失败时可重试，每项都可以移除。
 */
export function AttachmentStrip<T>({
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
    <Group gap={8} mb={8} wrap="wrap" className="inbox-attach-strip">
      {items.map((it) =>
        it.preview ? (
          <Box key={it.id} className="inbox-attach-thumb" data-error={it.status === "error" || undefined}>
            <img src={it.preview} alt={it.file.name} />
            {it.status === "uploading" && (
              <Box className="inbox-attach-overlay" aria-label={t("inbox.attach_uploading")}>
                <Loader size={16} color="white" />
              </Box>
            )}
            {it.status === "error" && (
              <Tooltip label={it.error || t("inbox.attach_retry")} multiline maw={240}>
                <UnstyledButton
                  className="inbox-attach-overlay inbox-attach-overlay-error"
                  onClick={() => onRetry(it.id)}
                  aria-label={t("inbox.attach_retry")}
                >
                  <IconRefresh size={16} />
                </UnstyledButton>
              </Tooltip>
            )}
            <ActionIcon
              size={18}
              radius="xl"
              variant="filled"
              color="dark"
              className="inbox-attach-remove"
              onClick={() => onRemove(it.id)}
              aria-label={t("inbox.attach_remove")}
            >
              <IconX size={11} />
            </ActionIcon>
          </Box>
        ) : (
          <Group key={it.id} gap={6} wrap="nowrap" px={8} py={5} className="inbox-attach-file" data-error={it.status === "error" || undefined}>
            <Box className="inbox-attach-file-icon" c={it.status === "error" ? "red" : "dimmed"}>
              {it.status === "uploading" ? (
                <Loader size={14} />
              ) : it.status === "error" ? (
                <IconAlertCircle size={16} />
              ) : (
                fileIcon(it.file.type, it.file.name, 16)
              )}
            </Box>
            <Box style={{ minWidth: 0, flex: 1 }}>
              <Tooltip label={it.status === "error" ? (it.error ?? it.file.name) : it.file.name} openDelay={300}>
                <Text size="xs" fw={500} truncate>
                  {it.file.name}
                </Text>
              </Tooltip>
              <Text size="xs" c={it.status === "error" ? "red" : "dimmed"} truncate>
                {it.status === "error" ? (it.error ?? formatFileSize(it.file.size)) : formatFileSize(it.file.size)}
              </Text>
            </Box>
            {it.status === "error" && (
              <Tooltip label={t("inbox.attach_retry")}>
                <ActionIcon size="xs" variant="subtle" color="red" onClick={() => onRetry(it.id)} aria-label={t("inbox.attach_retry")}>
                  <IconRefresh size={13} />
                </ActionIcon>
              </Tooltip>
            )}
            <ActionIcon size="xs" variant="subtle" color="gray" onClick={() => onRemove(it.id)} aria-label={t("inbox.attach_remove")}>
              <IconX size={13} />
            </ActionIcon>
          </Group>
        ),
      )}
    </Group>
  )
}

/**
 * MessageAttachments 渲染消息或内部备注附带的图片缩略图与文件卡片：
 * 图片按尺寸比例占位并在点击后打开原图灯箱，文件显示名称、体积和下载入口。
 */
export function MessageAttachments({
  attachments,
  hasText,
  onPreview,
}: {
  attachments?: ConversationAttachmentDTO[]
  hasText: boolean
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
        <Box className="inbox-msg-images" data-cols={cols}>
          {images.map((img) => {
            const singleStyle =
              images.length === 1 && img.width && img.height ? { aspectRatio: `${img.width} / ${img.height}` } : undefined
            return (
              <UnstyledButton
                key={img.file_id}
                className="inbox-msg-img"
                data-single={images.length === 1 || undefined}
                style={singleStyle}
                onClick={() => onPreview({ src: img.url, filename: img.filename })}
                aria-label={t("inbox.view_image")}
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
              className="inbox-msg-file"
              aria-label={t("inbox.download_file")}
            >
              <Box className="inbox-attach-file-icon" c="dimmed">
                {fileIcon(f.content_type, f.filename, 18)}
              </Box>
              <Box style={{ minWidth: 0, flex: 1 }}>
                <Tooltip label={f.filename} openDelay={350}>
                  <Text size="xs" fw={500} truncate>
                    {f.filename}
                  </Text>
                </Tooltip>
                <Text size="xs" c="dimmed">
                  {formatFileSize(f.size)}
                </Text>
              </Box>
              <Box className="inbox-attach-file-icon" c="dimmed">
                <IconDownload size={15} />
              </Box>
            </a>
          ))}
        </Stack>
      )}
    </Stack>
  )
}
