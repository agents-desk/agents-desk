import type { MemberNotificationSettings } from "@agents-desk/client/api"
import type { useT } from "@go-tenon/ui"
import { tenantApi } from "../api"
import { chatDisplayName } from "../chat/data"
import { previewSummary } from "./Attachments"
import { contactName } from "./List"

type Translate = ReturnType<typeof useT>

/** PushAlert 是服务端推送事件中携带的实时提醒类型。 */
export type PushAlert = "assigned" | "new_message" | "mention"

/** THROTTLE_MS 控制同一会话连续触发提醒的最小间隔（毫秒），防止短时间连续消息刷屏。 */
const THROTTLE_MS = 2000

/** lastAlertAt 记录每个会话最近一次触发提醒的时间戳。 */
const lastAlertAt = new Map<string, number>()

let sharedAudioCtx: AudioContext | null = null

/** playAlertSound 使用 Web Audio API 播放柔和的双音提示音；浏览器未解锁音频上下文时静默忽略。 */
export function playAlertSound(): void {
  if (typeof window === "undefined") return
  const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (!Ctx) return
  try {
    const ctx = sharedAudioCtx ?? (sharedAudioCtx = new Ctx())
    if (ctx.state === "suspended") {
      void ctx.resume()
    }
    const now = ctx.currentTime
    const notes = [
      { freq: 880, start: now, duration: 0.12 },
      { freq: 1174.66, start: now + 0.11, duration: 0.16 },
    ]
    for (const note of notes) {
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()
      osc.type = "sine"
      osc.frequency.setValueAtTime(note.freq, note.start)
      gain.gain.setValueAtTime(0.0001, note.start)
      gain.gain.exponentialRampToValueAtTime(0.12, note.start + 0.015)
      gain.gain.exponentialRampToValueAtTime(0.0001, note.start + note.duration)
      osc.connect(gain)
      gain.connect(ctx.destination)
      osc.start(note.start)
      osc.stop(note.start + note.duration + 0.02)
    }
  } catch {
    // 浏览器限制自动播放时跳过提示音
  }
}

/**
 * isViewingConversation 判断当前标签页是否处于前台且正在查看指定的会话、工单或聊天：
 * 它们在收件箱模块中的地址是 /{slug}/inbox/{id}、/{slug}/inbox/tickets/{id}、/{slug}/inbox/chat/{id}，前面还有前端的挂载路径。
 */
function isViewingConversation(slug: string, conversationId: string): boolean {
  if (typeof document === "undefined" || typeof window === "undefined") return false
  if (document.visibilityState !== "visible") return false
  const path = window.location.pathname
  const inbox = `/${slug}/inbox/`
  return [inbox, `${inbox}tickets/`, `${inbox}chat/`].some((prefix) => path.endsWith(prefix + conversationId))
}

/**
 * dispatchPushAlert 根据成员的通知偏好处理一次实时提醒：
 * 正在前台查看同一会话时不打扰；否则按开关播放提示音，并在已授权时弹出浏览器桌面通知（点击后聚焦并打开该会话）。
 */
export function dispatchPushAlert({
  slug,
  kind = "conversation",
  conversationId,
  alert,
  settings,
  t,
  onOpen,
}: {
  slug: string
  kind?: string
  conversationId: string
  alert: PushAlert
  settings?: MemberNotificationSettings
  t: Translate
  onOpen: (conversationId: string) => void
}): void {
  if (!settings) return
  const pref = settings[alert]
  if (!pref || (!pref.browser && !pref.sound)) return
  if (isViewingConversation(slug, conversationId)) return

  const now = Date.now()
  const prev = lastAlertAt.get(conversationId)
  if (prev !== undefined && now - prev < THROTTLE_MS) return
  lastAlertAt.set(conversationId, now)

  if (pref.sound) {
    playAlertSound()
  }
  if (!pref.browser || typeof Notification === "undefined" || Notification.permission !== "granted") {
    return
  }

  void showBrowserNotification(kind, conversationId, alert, t, onOpen)
}

async function showBrowserNotification(
  kind: string,
  conversationId: string,
  alert: PushAlert,
  t: Translate,
  onOpen: (conversationId: string) => void,
): Promise<void> {
  let targetName = t("inbox.visitor")
  let body = t(`alerts.${alert}_body` as const)
  try {
    if (kind === "chat") {
      const chat = await tenantApi.chat.get(conversationId)
      targetName = chatDisplayName(chat, t as Parameters<typeof chatDisplayName>[1])
      const snippet = previewSummary(chat.preview, chat.preview_attachment, t, chat.preview_recalled)
      if (snippet) {
        body = snippet
      }
    } else if (kind === "ticket") {
      const ticket = await tenantApi.ticket.get(conversationId)
      targetName = ticket.title || `#${ticket.number}`
    } else {
      const conv = await tenantApi.conversation.get(conversationId)
      targetName = contactName(conv, t)
      const snippet = previewSummary(conv.preview, conv.preview_attachment, t, conv.preview_recalled)
      if (snippet) {
        body = snippet
      }
    }
  } catch (err) {
    console.warn("load conversation for desktop notification failed", err)
  }
  const titleKey = kind === "chat" ? "alerts.chat_title" : kind === "ticket" ? "alerts.ticket_title" : (`alerts.${alert}_title` as const)
  const title = t(titleKey, { contact: targetName })
  const notif = new Notification(title, { body, tag: `agents-desk:${conversationId}` })
  notif.onclick = () => {
    window.focus()
    notif.close()
    onOpen(conversationId)
  }
}
