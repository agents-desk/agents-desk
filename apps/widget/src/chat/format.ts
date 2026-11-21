// 时间的显示：消息旁边是时:分；会话列表和日期分组是“刚刚”“几分钟前”“今天”“昨天”或者月日。按界面的语言格式化。

type T = (id: "time.just_now" | "time.minutes_ago" | "time.today" | "time.yesterday", data?: Record<string, unknown>) => string

/** startOfDay 返回 d 所在那天的零点。 */
const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()

/** clock 返回时:分，例如 10:06。 */
export function clock(iso: string, language: string): string {
  return new Intl.DateTimeFormat(language, { hour: "2-digit", minute: "2-digit" }).format(new Date(iso))
}

/** dayLabel 返回日期分组的标题：今天、昨天，或者月日。 */
export function dayLabel(iso: string, language: string, t: T): string {
  const d = new Date(iso)
  const days = Math.round((startOfDay(new Date()) - startOfDay(d)) / 86_400_000)
  if (days === 0) return t("time.today")
  if (days === 1) return t("time.yesterday")
  return new Intl.DateTimeFormat(language, { month: "short", day: "numeric" }).format(d)
}

/** ago 返回会话列表中的时间：一分钟内是刚刚，一小时内是几分钟前，今天是时:分，其余同 dayLabel。 */
export function ago(iso: string, language: string, t: T): string {
  const minutes = Math.floor((Date.now() - new Date(iso).getTime()) / 60_000)
  if (minutes < 1) return t("time.just_now")
  if (minutes < 60) return t("time.minutes_ago", { n: minutes })
  const label = dayLabel(iso, language, t)
  return label === t("time.today") ? clock(iso, language) : label
}

/** sameDay 报告两个时间是不是同一天。 */
export const sameDay = (a: string, b: string) => startOfDay(new Date(a)) === startOfDay(new Date(b))
