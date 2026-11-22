import type { useT } from "@go-tenon/ui"
import type { Thresholds } from "./data"

// 时间的显示：等待时长、消息旁的时:分、日期分组。按界面的语言格式化，时区跟随成员的偏好（TimeZoneProvider）。

type T = ReturnType<typeof useT>

/** duration 返回从 since 到 now 的时长：刚刚、几分钟、几小时、几天。 */
export function duration(since: string, now: number, t: T): string {
  const minutes = Math.floor((now - new Date(since).getTime()) / 60_000)
  if (minutes < 1) return t("time.just_now")
  if (minutes < 60) return t("time.minutes", { n: minutes })
  if (minutes < 24 * 60) return t("time.hours", { n: Math.floor(minutes / 60) })
  return t("time.days", { n: Math.floor(minutes / (24 * 60)) })
}

/** WaitLevel 是等待时长的程度：没有超过回复时间、等待处理（超过回复时间）、需要注意（超过更长的时限）。 */
export type WaitLevel = "ok" | "warn" | "alert"

/** waitLevel 返回从 since 等到 now 的程度；时限还没读到时是 ok。 */
export function waitLevel(since: string, now: number, th: Thresholds | undefined): WaitLevel {
  if (!th) return "ok"
  const seconds = (now - new Date(since).getTime()) / 1000
  if (seconds >= th.alert) return "alert"
  if (seconds >= th.warn) return "warn"
  return "ok"
}

/** waitColor 是等待时长的颜色：等待处理 yellow，需要注意 deep-orange（状态色的约定）。 */
export const waitColor: Record<WaitLevel, string | undefined> = { ok: undefined, warn: "yellow", alert: "deep-orange" }

/** startOfDay 返回 d 所在那天的零点（浏览器的时区）。 */
const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()

/** clock 返回时:分。 */
export const clock = (iso: string, language: string, timeZone?: string) =>
  new Intl.DateTimeFormat(language, { hour: "2-digit", minute: "2-digit", timeZone }).format(new Date(iso))

/** fullDateTime 返回包含年月日与时分秒的完整日期时间，用于悬停提示。 */
export const fullDateTime = (iso: string, language: string, timeZone?: string) =>
  new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "medium", timeZone }).format(new Date(iso))

/** dayLabel 返回日期分组的标题：今天、昨天，或者月日。 */
export function dayLabel(iso: string, language: string, t: T, timeZone?: string): string {
  const d = new Date(iso)
  const days = Math.round((startOfDay(new Date()) - startOfDay(d)) / 86_400_000)
  if (days === 0) return t("time.today")
  if (days === 1) return t("time.yesterday")
  return new Intl.DateTimeFormat(language, { month: "short", day: "numeric", timeZone }).format(d)
}

/** activity 返回会话列表中已回复的会话的时间：今天是时:分，其余同 dayLabel。 */
export function activity(iso: string, language: string, t: T, timeZone?: string): string {
  const label = dayLabel(iso, language, t, timeZone)
  return label === t("time.today") ? clock(iso, language, timeZone) : label
}

/** sameDay 报告两个时间是不是同一天。 */
export const sameDay = (a: string, b: string) => startOfDay(new Date(a)) === startOfDay(new Date(b))
