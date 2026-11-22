import type { ConversationPrompt } from "@agents-desk/client/tenant"
import type { useT } from "@go-tenon/ui"

type T = ReturnType<typeof useT>

/** formatNextOpen 把 RFC3339 时刻按成员当前的语言和时区格式化成简短的日期与时间。 */
function formatNextOpen(iso: string, language: string, timeZone?: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return new Intl.DateTimeFormat(language, {
    month: "short",
    day: "numeric",
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
    timeZone,
  }).format(d)
}

/** promptText 把系统发出的结构化提示按成员当前的界面语言和时区格式化成一句文字；不认识的种类回退到 fallback。 */
export function promptText(prompt: ConversationPrompt | undefined, fallback: string, language: string, t: T, timeZone?: string): string {
  if (!prompt) return fallback
  const params = prompt.params ?? {}
  switch (prompt.kind) {
    case "reply_time_open": {
      const rt = params.reply_time
      if (rt === "minutes") return t("prompt.reply_time_minutes")
      if (rt === "hour") return t("prompt.reply_time_hour")
      if (rt === "hours") return t("prompt.reply_time_hours")
      if (rt === "day") return t("prompt.reply_time_day")
      return fallback
    }
    case "reply_time_closed": {
      const next = typeof params.next_open_at === "string" ? params.next_open_at : ""
      if (!next) return t("prompt.reply_time_closed_unknown")
      return t("prompt.reply_time_closed", { time: formatNextOpen(next, language, timeZone) })
    }
    case "collect_contact":
      return t("prompt.collect_contact")
    default:
      return fallback
  }
}
