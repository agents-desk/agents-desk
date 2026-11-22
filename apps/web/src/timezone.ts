// 时区工具：规范化浏览器里的旧 IANA 时区名、列出全部可选时区、计算 UTC 偏移和对方当地时间。

/**
 * 浏览器（例如 Chrome）的时区列表中仍用旧名字的时区，换成 IANA 现在的名字：用户按现在的名字搜索（例如 Kolkata）也能找到，
 * 保存的也是现在的名字。服务端两种名字都认识。
 */
const renamed: Record<string, string> = {
  "Asia/Calcutta": "Asia/Kolkata",
  "Asia/Saigon": "Asia/Ho_Chi_Minh",
  "Asia/Katmandu": "Asia/Kathmandu",
  "Asia/Rangoon": "Asia/Yangon",
  "Europe/Kiev": "Europe/Kyiv",
  "Africa/Asmera": "Africa/Asmara",
  "Atlantic/Faeroe": "Atlantic/Faroe",
  "America/Godthab": "America/Nuuk",
  "America/Buenos_Aires": "America/Argentina/Buenos_Aires",
  "America/Catamarca": "America/Argentina/Catamarca",
  "America/Cordoba": "America/Argentina/Cordoba",
  "America/Jujuy": "America/Argentina/Jujuy",
  "America/Mendoza": "America/Argentina/Mendoza",
  "America/Indianapolis": "America/Indiana/Indianapolis",
  "America/Louisville": "America/Kentucky/Louisville",
  "Pacific/Truk": "Pacific/Chuuk",
  "Pacific/Ponape": "Pacific/Pohnpei",
  "Pacific/Enderbury": "Pacific/Kanton",
}

/** normalizeZone 把旧 IANA 时区名换成现在的名字。 */
export function normalizeZone(zone: string): string {
  return renamed[zone] ?? zone
}

/** 浏览器所在的时区，例如 Asia/Shanghai。 */
export const browserZone = (() => {
  const z = Intl.DateTimeFormat().resolvedOptions().timeZone
  return normalizeZone(z)
})()

/** allZones 返回浏览器支持的全部时区，旧名字换成现在的名字，按名字排序。 */
export function allZones(): string[] {
  const zones = new Set(Intl.supportedValuesOf("timeZone").map(normalizeZone))
  return [...zones].sort()
}

/** offsetOf 返回时区 zone 现在的 UTC 偏移，例如 UTC+05:30。 */
export function offsetOf(zone: string): string {
  const name = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "longOffset" })
    .formatToParts(new Date())
    .find((p) => p.type === "timeZoneName")?.value
  return !name || name === "GMT" ? "UTC+00:00" : name.replace("GMT", "UTC")
}

/** formatLocalTime 返回时区 zone 的当前钟点（24 小时制 HH:mm）；时区无效或为空时返回空字符串。 */
export function formatLocalTime(zone: string | undefined, now: Date = new Date()): string {
  if (!zone) return ""
  try {
    return new Intl.DateTimeFormat("en-GB", { timeZone: zone, hour: "2-digit", minute: "2-digit", hour12: false }).format(now)
  } catch {
    return ""
  }
}
