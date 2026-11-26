import type {
  ConversationAttachmentDTO,
  ConversationAttachmentInput,
  File as UploadedFile,
} from "@agents-desk/client/widget"
import { ActionIcon, Box, Group, Loader, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import {
  IconAlertCircle,
  IconDownload,
  IconFile,
  IconFileSpreadsheet,
  IconFileText,
  IconFileZip,
  IconMicrophone,
  IconMovie,
  IconMusic,
  IconPlayerPlay,
  IconRefresh,
  IconX,
} from "@tabler/icons-react"
import { formatFileSize, type UploadQueueItem, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { delegatesOpenLink, openLink } from "../host"

/** ATTACHMENT_ACCEPT 是聊天窗口允许选择的图片、视频、音频和常用文件类型，与服务端白名单一致。 */
export const ATTACHMENT_ACCEPT =
  "image/png,image/jpeg,image/webp,image/gif,video/mp4,video/webm,video/quicktime,.mov,audio/ogg,audio/mpeg,audio/mp4,audio/webm,audio/wav,audio/aac,audio/flac,.m4a,.opus,.pdf,.docx,.xlsx,.pptx,.doc,.xls,.ppt,.txt,.csv,.md,.log,.json,.zip"

/** MAX_ATTACHMENTS 是单条消息最多附带的附件数量。 */
export const MAX_ATTACHMENTS = 10

const MAX_IMAGE_SIZE = 10 * 1024 * 1024
const MAX_AUDIO_SIZE = 15 * 1024 * 1024
const MAX_FILE_SIZE = 20 * 1024 * 1024
const MAX_VIDEO_SIZE = 50 * 1024 * 1024

/** attachmentMaxSize 返回文件允许的最大字节数：图片 10 MiB、音频 15 MiB、文件 20 MiB、视频 50 MiB。 */
export function attachmentMaxSize(file: File): number {
  if (file.type.startsWith("image/")) return MAX_IMAGE_SIZE
  if (file.type.startsWith("video/") || /\.(mp4|mov|webm)$/i.test(file.name)) return MAX_VIDEO_SIZE
  if (file.type.startsWith("audio/") || /\.(ogg|oga|opus|mp3|m4a|wav|aac|flac)$/i.test(file.name)) return MAX_AUDIO_SIZE
  return MAX_FILE_SIZE
}

/** WidgetUploadedMedia 是聊天窗口上传一个媒体文件后的结果（含可选视频首帧封面和音视频元数据）。 */
export interface WidgetUploadedMedia {
  file: UploadedFile
  thumbFile?: UploadedFile
  durationMs?: number
  width?: number
  height?: number
}

/** toWidgetAttachmentInput 将已上传媒体转为发送接口要求的 ConversationAttachmentInput。 */
export function toWidgetAttachmentInput(m: WidgetUploadedMedia): ConversationAttachmentInput {
  return {
    file_id: m.file.id,
    thumb_file_id: m.thumbFile?.id,
    duration_ms: m.durationMs,
    width: m.width,
    height: m.height,
  }
}

/** toOptimisticAttachment 将已上传媒体转为发送中气泡乐观展示用的 ConversationAttachmentDTO。 */
export function toOptimisticAttachment(m: WidgetUploadedMedia): ConversationAttachmentDTO {
  const ct = m.file.content_type.toLowerCase()
  const kind = ct.startsWith("image/") ? "image" : ct.startsWith("video/") ? "video" : ct.startsWith("audio/") ? "audio" : "file"
  return {
    id: m.file.id,
    file_id: m.file.id,
    kind,
    status: "ready",
    filename: m.file.filename,
    content_type: m.file.content_type,
    size: m.file.size,
    width: m.width,
    height: m.height,
    duration_ms: m.durationMs,
    url: m.file.url,
    thumb_url: kind === "image" ? m.file.url : m.thumbFile?.url,
  }
}

/** uploadWidgetMediaFile 上传媒体文件，视频自动提取首帧封面与尺寸时长，音频提取时长。 */
export async function uploadWidgetMediaFile(file: File, upload: (f: File) => Promise<UploadedFile>): Promise<WidgetUploadedMedia> {
  const isVideo = file.type.startsWith("video/") || /\.(mp4|mov|webm)$/i.test(file.name)
  const isAudio = !isVideo && (file.type.startsWith("audio/") || /\.(ogg|oga|opus|mp3|m4a|wav|aac|flac)$/i.test(file.name))

  if (isVideo) {
    const [uploaded, meta] = await Promise.all([upload(file), probeVideoFile(file)])
    let thumbFile: UploadedFile | undefined
    if (meta.poster) {
      try {
        thumbFile = await upload(meta.poster)
      } catch {
        // 封面上传失败不阻断视频发送
      }
    }
    return {
      file: uploaded,
      thumbFile,
      durationMs: meta.durationMs,
      width: meta.width,
      height: meta.height,
    }
  }
  if (isAudio) {
    const [uploaded, durationMs] = await Promise.all([upload(file), probeAudioDuration(file)])
    return { file: uploaded, durationMs }
  }
  return { file: await upload(file) }
}

/** probeVideoFile 使用浏览器原生 video + canvas 抽取视频首帧封面、宽高和时长。 */
function probeVideoFile(file: File): Promise<{ poster?: File; durationMs?: number; width?: number; height?: number }> {
  return new Promise((resolve) => {
    const url = URL.createObjectURL(file)
    const video = document.createElement("video")
    video.preload = "metadata"
    video.muted = true
    video.playsInline = true
    let done = false
    const finish = (res: { poster?: File; durationMs?: number; width?: number; height?: number }) => {
      if (done) return
      done = true
      clearTimeout(timer)
      video.removeAttribute("src")
      video.load()
      URL.revokeObjectURL(url)
      resolve(res)
    }
    const timer = window.setTimeout(() => finish({}), 4000)

    const capture = () => {
      const durationMs = Number.isFinite(video.duration) && video.duration > 0 ? Math.round(video.duration * 1000) : undefined
      const width = video.videoWidth || undefined
      const height = video.videoHeight || undefined
      if (!width || !height) {
        finish({ durationMs })
        return
      }
      const maxEdge = 640
      const scale = Math.min(1, maxEdge / Math.max(width, height))
      const canvas = document.createElement("canvas")
      canvas.width = Math.max(1, Math.round(width * scale))
      canvas.height = Math.max(1, Math.round(height * scale))
      const ctx = canvas.getContext("2d")
      if (!ctx) {
        finish({ durationMs, width, height })
        return
      }
      ctx.drawImage(video, 0, 0, canvas.width, canvas.height)
      canvas.toBlob(
        (blob) => {
          const poster = blob ? new File([blob], "poster.jpg", { type: "image/jpeg" }) : undefined
          finish({ poster, durationMs, width, height })
        },
        "image/jpeg",
        0.8,
      )
    }

    video.onloadeddata = () => {
      if (video.duration > 0.2) {
        video.onseeked = capture
        video.currentTime = Math.min(0.1, video.duration / 4)
      } else {
        capture()
      }
    }
    video.onerror = () => finish({})
    video.src = url
  })
}

/** probeAudioDuration 读取音频时长（毫秒）。 */
function probeAudioDuration(file: File): Promise<number | undefined> {
  return new Promise((resolve) => {
    const url = URL.createObjectURL(file)
    const audio = document.createElement("audio")
    audio.preload = "metadata"
    let done = false
    const finish = (ms?: number) => {
      if (done) return
      done = true
      clearTimeout(timer)
      audio.removeAttribute("src")
      audio.load()
      URL.revokeObjectURL(url)
      resolve(ms)
    }
    const timer = window.setTimeout(() => finish(undefined), 3000)
    audio.onloadedmetadata = () => {
      const ms = Number.isFinite(audio.duration) && audio.duration > 0 ? Math.round(audio.duration * 1000) : undefined
      finish(ms)
    }
    audio.onerror = () => finish(undefined)
    audio.src = url
  })
}

/** formatDuration 将毫秒格式化为 m:ss。 */
function formatDuration(ms?: number): string {
  if (!ms || ms <= 0) return ""
  const totalSec = Math.max(1, Math.round(ms / 1000))
  const h = Math.floor(totalSec / 3600)
  const m = Math.floor((totalSec % 3600) / 60)
  const s = totalSec % 60
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`
  return `${m}:${String(s).padStart(2, "0")}`
}

/** LightboxImage 是灯箱正在预览的原图信息。 */
export interface LightboxImage {
  src: string
  filename?: string
}

/** fileIcon 按文件类型和扩展名返回合适的文档或媒体图标。 */
function fileIcon(contentType: string, filename: string, size = 16): ReactNode {
  const ct = contentType.toLowerCase()
  const name = filename.toLowerCase()
  if (ct.startsWith("video/") || /\.(mp4|mov|webm)$/.test(name)) return <IconMovie size={size} stroke={1.6} />
  if (ct.startsWith("audio/") || /\.(ogg|oga|opus|mp3|m4a|wav|aac|flac)$/.test(name)) return <IconMusic size={size} stroke={1.6} />
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
export function WidgetAttachmentStrip({
  items,
  onRemove,
  onRetry,
}: {
  items: UploadQueueItem<WidgetUploadedMedia>[]
  onRemove: (id: string) => void
  onRetry: (id: string) => void
}): ReactNode {
  const t = useT()
  if (items.length === 0) return null
  return (
    <Group gap={6} px="xs" pt="xs" wrap="wrap" className="w-attach-strip">
      {items.map((it) => {
        const thumbSrc = it.preview || it.result?.thumbFile?.url
        const dur = formatDuration(it.result?.durationMs)
        return thumbSrc ? (
          <Box key={it.id} className="w-attach-thumb" data-error={it.status === "error" || undefined}>
            <img src={thumbSrc} alt={it.file.name} />
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
                {it.status === "error"
                  ? (it.error ?? formatFileSize(it.file.size))
                  : [dur, formatFileSize(it.file.size)].filter(Boolean).join(" · ")}
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
        )
      })}
    </Group>
  )
}

/**
 * WidgetMessageAttachments 渲染聊天窗口消息气泡中的图片缩略图、视频播放器、音频/语音条与文件下载卡片。
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
  const videos = attachments.filter((a) => a.kind === "video")
  const audios = attachments.filter((a) => a.kind === "audio")
  const files = attachments.filter((a) => a.kind === "file")
  const cols = images.length === 1 ? 1 : images.length === 2 || images.length === 4 ? 2 : 3

  return (
    <Stack gap={6} mt={hasText ? 6 : 0}>
      {images.length > 0 && (
        <Box className="w-msg-images" data-cols={cols}>
          {images.map((img) => {
            const singleStyle =
              images.length === 1 && img.width && img.height ? { aspectRatio: `${img.width} / ${img.height}` } : undefined
            const src = img.url || img.thumb_url
            return (
              <UnstyledButton
                key={img.id}
                className="w-msg-img"
                data-single={images.length === 1 || undefined}
                style={singleStyle}
                disabled={!src}
                onClick={() => src && onPreview({ src, filename: img.filename })}
                aria-label={t("chat.view_image")}
              >
                {src ? <img src={img.thumb_url || src} alt={img.filename} loading="lazy" /> : <Loader size={16} />}
              </UnstyledButton>
            )
          })}
        </Box>
      )}
      {videos.length > 0 && (
        <Stack gap={6}>
          {videos.map((v) => (
            <WidgetVideoCard key={v.id} att={v} />
          ))}
        </Stack>
      )}
      {audios.length > 0 && (
        <Stack gap={4}>
          {audios.map((a) => (
            <Box key={a.id} className="w-msg-audio" data-mine={mine || undefined}>
              <Group gap={6} wrap="nowrap" mb={4}>
                <Box className="w-attach-icon">{a.is_voice ? <IconMicrophone size={14} /> : <IconMusic size={14} />}</Box>
                <Text size="xs" fw={500} truncate style={{ flex: 1 }}>
                  {a.is_voice ? t("chat.voice_message") : a.filename}
                </Text>
                {a.duration_ms ? (
                  <Text size="xs" className="w-msg-file-size">
                    {formatDuration(a.duration_ms)}
                  </Text>
                ) : null}
              </Group>
              {a.url ? (
                /* biome-ignore lint/a11y/useMediaCaption: 聊天语音与音频附件无外挂字幕 */
                <audio controls preload="metadata" src={a.url} className="w-audio-player" />
              ) : (
                <Text size="xs" className="w-msg-file-size">
                  {a.status === "unavailable" ? t("chat.media_unavailable") : t("chat.media_loading")}
                </Text>
              )}
            </Box>
          ))}
        </Stack>
      )}
      {files.length > 0 && (
        <Stack gap={4}>
          {files.map((f) => (
            <a
              key={f.id}
              href={f.url || undefined}
              download={f.filename}
              target="_blank"
              rel="noopener noreferrer"
              className="w-msg-file"
              data-mine={mine || undefined}
              aria-label={t("chat.download_file")}
              onClick={(e) => {
                if (!f.url) {
                  e.preventDefault()
                  return
                }
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

/** WidgetVideoCard 渲染聊天窗口中的视频封面卡片，点击后原地播放。 */
function WidgetVideoCard({ att }: { att: ConversationAttachmentDTO }): ReactNode {
  const t = useT()
  const [playing, setPlaying] = useState(false)
  const dur = formatDuration(att.duration_ms)
  const ratio = att.width && att.height ? { aspectRatio: `${att.width} / ${att.height}` } : undefined

  if (playing && att.url) {
    return (
      <Box className="w-msg-video" style={ratio}>
        {/* biome-ignore lint/a11y/useMediaCaption: 聊天短视频无外挂字幕 */}
        <video src={att.url} poster={att.thumb_url || undefined} controls autoPlay playsInline />
      </Box>
    )
  }

  return (
    <Box className="w-msg-video" style={ratio}>
      {att.thumb_url ? (
        <img src={att.thumb_url} alt={att.filename} loading="lazy" />
      ) : (
        <Box className="w-msg-video-empty">
          <IconMovie size={24} stroke={1.4} />
        </Box>
      )}
      <UnstyledButton
        className="w-media-overlay"
        disabled={!att.url}
        onClick={() => att.url && setPlaying(true)}
        aria-label={t("chat.play_video")}
      >
        <Box className="w-media-play-btn">
          {att.url ? <IconPlayerPlay size={18} /> : att.status === "unavailable" ? <IconAlertCircle size={18} /> : <Loader size={16} color="white" />}
        </Box>
      </UnstyledButton>
      {(dur || att.size > 0) && (
        <Group gap={6} className="w-msg-video-meta" wrap="nowrap">
          {dur && <span>{dur}</span>}
          {att.size > 0 && <span>{formatFileSize(att.size)}</span>}
        </Group>
      )}
    </Box>
  )
}
