import type {
  ConversationAttachmentDTO,
  ConversationAttachmentInput,
  ConversationPreviewAttachment,
  File as UploadedFile,
} from "@agents-desk/client/tenant"
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
import { useQueryClient } from "@tanstack/react-query"
import { formatFileSize, notifyError, type UploadQueueItem, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../api"

/** ATTACHMENT_ACCEPT 是回复框和内部备注允许选择的图片、视频、音频和常用文件类型，与服务端白名单保持一致。 */
export const ATTACHMENT_ACCEPT =
  "image/png,image/jpeg,image/webp,image/gif,video/mp4,video/webm,video/quicktime,.mov,audio/ogg,audio/mpeg,audio/mp4,audio/webm,audio/wav,audio/aac,audio/flac,.m4a,.opus,.pdf,.docx,.xlsx,.pptx,.doc,.xls,.ppt,.txt,.csv,.md,.log,.json,.zip"

/** MAX_ATTACHMENTS 是单条消息或内部备注最多附带的附件数量。 */
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

/** UploadedMedia 是前端上传完一个媒体附件后的完整信息（包含可选视频封面与音视频元数据）。 */
export interface UploadedMedia {
  file: UploadedFile
  thumbFile?: UploadedFile
  durationMs?: number
  width?: number
  height?: number
}

/** toAttachmentInput 将已上传的媒体结果转换为发消息接口要求的 ConversationAttachmentInput。 */
export function toAttachmentInput(m: UploadedMedia): ConversationAttachmentInput {
  return {
    file_id: m.file.id,
    thumb_file_id: m.thumbFile?.id,
    duration_ms: m.durationMs,
    width: m.width,
    height: m.height,
  }
}

/**
 * uploadMediaFile 上传一个媒体文件：若是视频则在浏览器端零依赖提取首帧封面与时长/尺寸一并上传，
 * 若是音频则提取时长，图片与普通文件直接上传。
 */
export async function uploadMediaFile(file: File, upload: (f: File) => Promise<UploadedFile>): Promise<UploadedMedia> {
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

/** probeVideoFile 使用浏览器原生 video + canvas 抽取视频首帧封面、宽高和时长（超时 4 秒兜底）。 */
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

/** probeAudioDuration 使用浏览器原生 audio 读取音频时长（毫秒）。 */
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

/** formatDuration 将毫秒格式化为 m:ss（超过 1 小时为 h:mm:ss）。 */
export function formatDuration(ms?: number): string {
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

/**
 * previewSummary 拼出会话列表和历史记录里的最后一条消息摘要：有附件时按界面语言显示“[图片]”、“[视频]”、“[语音]”或“[文件]”，
 * 同时带文字时把摘要放在文字前面。
 */
export function previewSummary(
  preview: string,
  attachment: ConversationPreviewAttachment | undefined,
  t: ReturnType<typeof useT>,
): string {
  if (!attachment) return preview
  const label =
    attachment.kind === "image"
      ? t("inbox.preview_image")
      : attachment.kind === "video"
        ? t("inbox.preview_video")
        : attachment.kind === "audio"
          ? t("inbox.preview_audio")
          : t("inbox.preview_file")
  if (!preview.trim()) return attachment.count > 1 ? `${label} ×${attachment.count}` : label
  return `${label} ${preview}`
}

/** fileIcon 按文件类型和扩展名返回合适的文档或媒体图标。 */
function fileIcon(contentType: string, filename: string, size = 18): ReactNode {
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
 * AttachmentStrip 是回复框上方的待发送附件队列条：图片显示缩略图，视频/音频/文件显示名称、体积与封面，
 * 上传中显示进度指示，失败时可重试，每项都可以移除。
 */
export function AttachmentStrip({
  items,
  onRemove,
  onRetry,
}: {
  items: UploadQueueItem<UploadedMedia>[]
  onRemove: (id: string) => void
  onRetry: (id: string) => void
}): ReactNode {
  const t = useT()
  if (items.length === 0) return null
  return (
    <Group gap={8} mb={8} wrap="wrap" className="inbox-attach-strip">
      {items.map((it) => {
        const thumbSrc = it.preview || it.result?.thumbFile?.url
        const dur = formatDuration(it.result?.durationMs)
        const isVideo = it.file.type.startsWith("video/") || /\.(mp4|mov|webm)$/i.test(it.file.name)
        return thumbSrc ? (
          <Box key={it.id} className="inbox-attach-thumb" data-error={it.status === "error" || undefined}>
            <img src={thumbSrc} alt={it.file.name} />
            {isVideo && it.status === "ready" && (
              <Box className="inbox-attach-badge">
                <IconPlayerPlay size={10} />
                {dur && <span>{dur}</span>}
              </Box>
            )}
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
                {it.status === "error"
                  ? (it.error ?? formatFileSize(it.file.size))
                  : [dur, formatFileSize(it.file.size)].filter(Boolean).join(" · ")}
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
        )
      })}
    </Group>
  )
}

/**
 * MessageAttachments 渲染消息或内部备注附带的图片、视频、音频/语音与文件附件：
 * 外部渠道未下载的视频和大文件先展示封面与元数据，点击后按需拉取并播放或下载。
 */
export function MessageAttachments({
  conversationId,
  attachments,
  hasText,
  onPreview,
}: {
  conversationId: string
  attachments?: ConversationAttachmentDTO[]
  hasText: boolean
  onPreview: (image: LightboxImage) => void
}): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [overrides, setOverrides] = useState<Record<string, ConversationAttachmentDTO>>({})
  const [fetchingIds, setFetchingIds] = useState<Record<string, boolean>>({})

  if (!attachments || attachments.length === 0) return null

  const resolved = attachments.map((a) => overrides[a.id] ?? a)
  const images = resolved.filter((a) => a.kind === "image")
  const videos = resolved.filter((a) => a.kind === "video")
  const audios = resolved.filter((a) => a.kind === "audio")
  const files = resolved.filter((a) => a.kind === "file")
  const cols = images.length === 1 ? 1 : images.length === 2 || images.length === 4 ? 2 : 3

  const fetchAttachment = async (att: ConversationAttachmentDTO): Promise<ConversationAttachmentDTO | null> => {
    if (fetchingIds[att.id]) return null
    setFetchingIds((prev) => ({ ...prev, [att.id]: true }))
    try {
      const updated = await tenantApi.conversation.attachmentFetch(conversationId, att.id)
      setOverrides((prev) => ({ ...prev, [att.id]: updated }))
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      return updated
    } catch (err) {
      notifyError(err)
      return null
    } finally {
      setFetchingIds((prev) => {
        const next = { ...prev }
        delete next[att.id]
        return next
      })
    }
  }

  return (
    <Stack gap={6} mt={hasText ? 6 : 0}>
      {images.length > 0 && (
        <Box className="inbox-msg-images" data-cols={cols}>
          {images.map((img) => {
            const singleStyle =
              images.length === 1 && img.width && img.height ? { aspectRatio: `${img.width} / ${img.height}` } : undefined
            const loading = Boolean(fetchingIds[img.id]) || img.status === "pending" || img.status === "fetching"
            if (img.status === "ready" && img.url) {
              return (
                <UnstyledButton
                  key={img.id}
                  className="inbox-msg-img"
                  data-single={images.length === 1 || undefined}
                  style={singleStyle}
                  onClick={() => onPreview({ src: img.url!, filename: img.filename })}
                  aria-label={t("inbox.view_image")}
                >
                  <img src={img.thumb_url || img.url} alt={img.filename} loading="lazy" />
                </UnstyledButton>
              )
            }
            return (
              <UnstyledButton
                key={img.id}
                className="inbox-msg-img inbox-msg-img-placeholder"
                data-single={images.length === 1 || undefined}
                style={singleStyle}
                disabled={loading || (img.status === "unavailable" && img.error === "channel.media_too_large")}
                onClick={() => void fetchAttachment(img)}
                aria-label={t("inbox.view_image")}
              >
                {img.thumb_url && <img src={img.thumb_url} alt={img.filename} loading="lazy" />}
                <Box className="inbox-media-overlay">
                  {loading ? (
                    <Loader size={18} color="white" />
                  ) : img.status === "unavailable" ? (
                    <IconAlertCircle size={18} />
                  ) : (
                    <IconDownload size={18} />
                  )}
                </Box>
              </UnstyledButton>
            )
          })}
        </Box>
      )}
      {videos.length > 0 && (
        <Stack gap={6}>
          {videos.map((v) => (
            <VideoAttachmentCard
              key={v.id}
              att={v}
              loading={Boolean(fetchingIds[v.id]) || v.status === "pending" || v.status === "fetching"}
              onFetch={() => fetchAttachment(v)}
            />
          ))}
        </Stack>
      )}
      {audios.length > 0 && (
        <Stack gap={4}>
          {audios.map((a) => (
            <AudioAttachmentCard
              key={a.id}
              att={a}
              loading={Boolean(fetchingIds[a.id]) || a.status === "pending" || a.status === "fetching"}
              onFetch={() => void fetchAttachment(a)}
            />
          ))}
        </Stack>
      )}
      {files.length > 0 && (
        <Stack gap={4}>
          {files.map((f) => {
            const loading = Boolean(fetchingIds[f.id]) || f.status === "pending" || f.status === "fetching"
            const tooLarge = f.status === "unavailable" && f.error === "channel.media_too_large"
            if (f.status === "ready" && f.url) {
              return (
                <a
                  key={f.id}
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
              )
            }
            return (
              <UnstyledButton
                key={f.id}
                className="inbox-msg-file"
                disabled={loading || tooLarge}
                onClick={async () => {
                  const res = await fetchAttachment(f)
                  if (res?.url) {
                    window.open(res.url, "_blank", "noopener,noreferrer")
                  }
                }}
              >
                <Box className="inbox-attach-file-icon" c={tooLarge ? "red" : "dimmed"}>
                  {loading ? <Loader size={15} /> : tooLarge ? <IconAlertCircle size={16} /> : fileIcon(f.content_type, f.filename, 18)}
                </Box>
                <Box style={{ minWidth: 0, flex: 1 }}>
                  <Tooltip label={f.filename} openDelay={350}>
                    <Text size="xs" fw={500} truncate>
                      {f.filename}
                    </Text>
                  </Tooltip>
                  <Text size="xs" c={tooLarge ? "red" : "dimmed"} truncate>
                    {loading
                      ? t("inbox.media_loading")
                      : tooLarge
                        ? `${formatFileSize(f.size)} · ${t("inbox.media_too_large")}`
                        : f.status === "unavailable"
                          ? t("inbox.media_unavailable")
                          : `${formatFileSize(f.size)} · ${t("inbox.media_load_file")}`}
                  </Text>
                </Box>
                {!loading && !tooLarge && (
                  <Box className="inbox-attach-file-icon" c="dimmed">
                    {f.status === "unavailable" ? <IconRefresh size={15} /> : <IconDownload size={15} />}
                  </Box>
                )}
              </UnstyledButton>
            )
          })}
        </Stack>
      )}
    </Stack>
  )
}

/** VideoAttachmentCard 渲染视频封面预览卡片：未下载的远端视频点击后按需拉取并原地播放。 */
function VideoAttachmentCard({
  att,
  loading,
  onFetch,
}: {
  att: ConversationAttachmentDTO
  loading: boolean
  onFetch: () => Promise<ConversationAttachmentDTO | null>
}): ReactNode {
  const t = useT()
  const [playing, setPlaying] = useState(false)
  const dur = formatDuration(att.duration_ms)
  const tooLarge = att.status === "unavailable" && att.error === "channel.media_too_large"
  const ratio = att.width && att.height ? { aspectRatio: `${att.width} / ${att.height}` } : undefined

  if (playing && att.status === "ready" && att.url) {
    return (
      <Box className="inbox-msg-video" style={ratio}>
        {/* biome-ignore lint/a11y/useMediaCaption: 用户上传的客服聊天短视频不包含外挂字幕轨 */}
        <video src={att.url} poster={att.thumb_url || undefined} controls autoPlay playsInline />
      </Box>
    )
  }

  const handleActivate = async () => {
    if (loading || tooLarge) return
    if (att.status === "ready" && att.url) {
      setPlaying(true)
      return
    }
    const fetched = await onFetch()
    if (fetched?.status === "ready" && fetched.url) {
      setPlaying(true)
    }
  }

  return (
    <Box className="inbox-msg-video" style={ratio}>
      {att.thumb_url ? (
        <img src={att.thumb_url} alt={att.filename} loading="lazy" />
      ) : (
        <Box className="inbox-msg-video-empty">
          <IconMovie size={28} stroke={1.4} />
        </Box>
      )}
      <UnstyledButton
        className="inbox-media-overlay"
        disabled={loading || tooLarge}
        onClick={() => void handleActivate()}
        aria-label={att.status === "ready" ? t("inbox.media_play_video") : t("inbox.media_load_video")}
      >
        <Box className="inbox-media-play-btn">
          {loading ? (
            <Loader size={20} color="white" />
          ) : tooLarge ? (
            <IconAlertCircle size={20} />
          ) : att.status === "ready" ? (
            <IconPlayerPlay size={20} />
          ) : att.status === "unavailable" ? (
            <IconRefresh size={20} />
          ) : (
            <IconDownload size={20} />
          )}
        </Box>
        {(loading || att.status !== "ready") && (
          <Text size="xs" fw={500} className="inbox-media-hint">
            {loading
              ? t("inbox.media_loading")
              : tooLarge
                ? t("inbox.media_too_large")
                : att.status === "unavailable"
                  ? t("inbox.media_retry")
                  : t("inbox.media_load_video")}
          </Text>
        )}
      </UnstyledButton>
      <Group gap={6} className="inbox-msg-video-meta" wrap="nowrap">
        {dur && <span>{dur}</span>}
        {att.size > 0 && <span>{formatFileSize(att.size)}</span>}
      </Group>
      {att.status === "ready" && att.url && (
        <Tooltip label={t("inbox.download_file")}>
          <ActionIcon
            component="a"
            href={att.url}
            download={att.filename}
            target="_blank"
            rel="noopener noreferrer"
            size="sm"
            radius="xl"
            variant="filled"
            color="dark"
            className="inbox-msg-video-dl"
            aria-label={t("inbox.download_file")}
          >
            <IconDownload size={13} />
          </ActionIcon>
        </Tooltip>
      )}
    </Box>
  )
}

/** AudioAttachmentCard 渲染语音条或音频附件卡片，就绪后提供内联播放器。 */
function AudioAttachmentCard({
  att,
  loading,
  onFetch,
}: {
  att: ConversationAttachmentDTO
  loading: boolean
  onFetch: () => void
}): ReactNode {
  const t = useT()
  const dur = formatDuration(att.duration_ms)
  const title = att.is_voice ? t("inbox.media_voice") : att.filename || t("inbox.media_play_audio")
  const tooLarge = att.status === "unavailable" && att.error === "channel.media_too_large"

  if (att.status === "ready" && att.url) {
    return (
      <Box className="inbox-msg-audio">
        <Group gap={6} wrap="nowrap" mb={4}>
          <Box className="inbox-attach-file-icon" c="brand">
            {att.is_voice ? <IconMicrophone size={15} /> : <IconMusic size={15} />}
          </Box>
          <Text size="xs" fw={500} truncate style={{ flex: 1 }}>
            {title}
          </Text>
          {dur && (
            <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
              {dur}
            </Text>
          )}
          <Tooltip label={t("inbox.download_file")}>
            <ActionIcon
              component="a"
              href={att.url}
              download={att.filename}
              target="_blank"
              rel="noopener noreferrer"
              size="xs"
              variant="subtle"
              color="gray"
              aria-label={t("inbox.download_file")}
            >
              <IconDownload size={13} />
            </ActionIcon>
          </Tooltip>
        </Group>
        {/* biome-ignore lint/a11y/useMediaCaption: 聊天语音与音频附件无外挂字幕 */}
        <audio controls preload="metadata" src={att.url} className="inbox-audio-player" />
      </Box>
    )
  }

  return (
    <UnstyledButton className="inbox-msg-file" disabled={loading || tooLarge} onClick={onFetch}>
      <Box className="inbox-attach-file-icon" c={tooLarge ? "red" : "brand"}>
        {loading ? <Loader size={15} /> : tooLarge ? <IconAlertCircle size={16} /> : att.is_voice ? <IconMicrophone size={16} /> : <IconMusic size={16} />}
      </Box>
      <Box style={{ minWidth: 0, flex: 1 }}>
        <Text size="xs" fw={500} truncate>
          {title}
        </Text>
        <Text size="xs" c={tooLarge ? "red" : "dimmed"} truncate>
          {loading
            ? t("inbox.media_loading")
            : tooLarge
              ? t("inbox.media_too_large")
              : [dur, att.size > 0 ? formatFileSize(att.size) : ""].filter(Boolean).join(" · ")}
        </Text>
      </Box>
      {!loading && !tooLarge && (
        <Box className="inbox-attach-file-icon" c="dimmed">
          <IconDownload size={15} />
        </Box>
      )}
    </UnstyledButton>
  )
}
