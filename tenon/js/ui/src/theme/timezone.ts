// IANA 时区列表、旧名称规范化与当地时间计算工具。

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

/** 将浏览器仍保留的旧 IANA 时区名规范化为现行名称。 */
export function normalizeTimeZone(zone: string): string {
  return renamed[zone] ?? zone
}

/** 当前浏览器所在地的规范化 IANA 时区名（例如 Asia/Shanghai）。 */
export function browserTimeZone(): string {
  return normalizeTimeZone(Intl.DateTimeFormat().resolvedOptions().timeZone)
}

/** 列出浏览器支持的全部规范化 IANA 时区并按字母序排列。 */
export function listTimeZones(): string[] {
  const zones = new Set(Intl.supportedValuesOf("timeZone").map(normalizeTimeZone))
  return [...zones].sort()
}

/** 返回时区 zone 当前的 UTC 偏移量，例如 UTC+08:00。 */
export function formatTimeZoneOffset(zone: string): string {
  const name = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "longOffset" })
    .formatToParts(new Date())
    .find((p) => p.type === "timeZoneName")?.value
  return !name || name === "GMT" ? "UTC+00:00" : name.replace("GMT", "UTC")
}

/** 返回时区 zone 的当前本地时刻（24 小时制 HH:mm）；时区无效或为空时返回空字符串。 */
export function formatTimeZoneLocal(zone: string | undefined, now: Date = new Date()): string {
  if (!zone) return ""
  try {
    return new Intl.DateTimeFormat("en-GB", {
      timeZone: zone,
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    }).format(now)
  } catch {
    return ""
  }
}
