import { Avatar } from "@mantine/core"
import { IconUser } from "@tabler/icons-react"
import type { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

/** ContactLabel 是展示联系人名字和头像所需的最小字段集合，兼容 ContactSummary 和 ContactDTO。 */
export interface ContactLabel {
  id?: string
  number?: number
  name?: string
  email?: string
}

/** AVATAR_COLORS 是主题预设的彩色色板：按联系人编号或 ID 稳定映射，避免匿名访客千篇一律同色。 */
const AVATAR_COLORS = [
  "brand",
  "bright-blue",
  "sky-blue",
  "green",
  "yellow",
  "deep-orange",
  "red",
  "magenta",
  "purple",
  "violet",
  "brown",
  "blue-gray",
] as const

/** contactDisplayName 返回联系人的展示名：真实姓名优先，其次邮箱，匿名访客显示“访客 #编号”。 */
export function contactDisplayName(c: ContactLabel | null | undefined, t: ReturnType<typeof useT>): string {
  const name = c?.name?.trim()
  if (name) return name
  const email = c?.email?.trim()
  if (email) return email
  if (c?.number && c.number > 0) return t("inbox.visitor_numbered", { n: c.number })
  return t("inbox.visitor")
}

/** contactAvatarColor 按联系人编号或 ID 返回稳定的主题色板名。 */
export function contactAvatarColor(c: ContactLabel | null | undefined): string {
  if (c?.number && c.number > 0) {
    return AVATAR_COLORS[(c.number - 1) % AVATAR_COLORS.length]
  }
  const seed = c?.id ?? c?.name ?? c?.email ?? ""
  if (!seed) return "gray"
  let h = 0
  for (let i = 0; i < seed.length; i++) {
    h = (h * 31 + seed.charCodeAt(i)) >>> 0
  }
  return AVATAR_COLORS[h % AVATAR_COLORS.length]
}

/** shortReferrer 从完整来源 URL 中提取精简的域名与路径（例如 shop.example.com/pricing），用于匿名访客的副标题辨识。 */
export function shortReferrer(raw?: string): string {
  if (!raw) return ""
  try {
    const u = new URL(raw)
    const path = u.pathname === "/" ? "" : u.pathname.replace(/\/+$/, "")
    return `${u.host}${path}`
  } catch {
    return ""
  }
}

/** contactSubline 返回联系人列表和命令面板中的辅助副标题：不重复已作为主标题展示的邮箱，匿名访客回退展示来源页面路径。 */
export function contactSubline(c: { name?: string; email?: string; external_user_id?: string; referrer?: string }): string {
  const hasName = Boolean(c.name?.trim())
  const parts = [hasName ? c.email : "", c.external_user_id].filter(Boolean)
  if (parts.length > 0) return parts.join(" · ")
  return shortReferrer(c.referrer)
}

/**
 * ContactAvatar 渲染联系人头像：
 *   - 有真实姓名或邮箱时按稳定色板显示首字母/首字；
 *   - 纯匿名访客显示带稳定主题色的访客图标，避免整屏都是同一个颜色的“访”字。
 */
export function ContactAvatar({ contact, size }: { contact: ContactLabel | null | undefined; size: number }): ReactNode {
  const color = contactAvatarColor(contact)
  const label = contact?.name?.trim() || contact?.email?.trim()
  if (label) {
    return <Avatar size={size} name={label} color={color} />
  }
  return (
    <Avatar size={size} color={color} variant="light">
      <IconUser size={Math.round(size * 0.54)} stroke={1.8} />
    </Avatar>
  )
}
