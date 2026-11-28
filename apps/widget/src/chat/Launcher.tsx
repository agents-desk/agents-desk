import { ActionIcon, Indicator } from "@mantine/core"
import { IconChevronDown, IconMessageCircle } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui/core"
import type { ReactNode } from "react"

/** Launcher 是嵌入网页时右下角的启动按钮：收起时是聊天图标，有未读时带角标（超过 99 显示 99+）；展开时是收起的箭头。 */
export function Launcher({ color, open, unread, onClick }: { color: string; open: boolean; unread: number; onClick: () => void }): ReactNode {
  const t = useT()
  const badge = unread > 99 ? "99+" : unread
  return (
    <Indicator label={badge} size={20} color="red" offset={6} disabled={open || unread === 0} withBorder styles={{ indicator: { fontVariantNumeric: "tabular-nums" } }}>
      <ActionIcon
        size={56}
        radius="xl"
        color={color}
        variant="filled"
        className="w-launcher"
        onClick={onClick}
        aria-label={t(open ? "panel.collapse" : "launcher.open")}
      >
        {open ? <IconChevronDown size={26} /> : <IconMessageCircle size={28} />}
      </ActionIcon>
    </Indicator>
  )
}
