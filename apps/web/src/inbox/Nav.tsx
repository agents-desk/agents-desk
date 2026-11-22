import type { ConversationSummary } from "@agents-desk/client/tenant"
import { NavLink, Text } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { IconAt, IconInbox, IconListDetails, IconUserQuestion } from "@tabler/icons-react"
import { Fill, RailSection, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit } from "../api"
import { type Thresholds, useThresholds } from "./data"
import { waitColor, waitLevel } from "./format"
import type { InboxSearch } from "./search"

/** useNow 返回现在的时间，每 30 秒更新一次：等待时长随之变化。 */
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  return now
}

/** Count 是收件箱列表中一项的数量：等得最久的会话超过时限时，数字用等待时长的颜色。 */
function Count({ s, now, th }: { s?: ConversationSummary; now: number; th?: Thresholds }): ReactNode {
  if (!s) return null
  const color = s.oldest_waiting_since ? waitColor[waitLevel(s.oldest_waiting_since, now, th)] : undefined
  return (
    <Text size="xs" c={color ?? "dimmed"} fw={color ? 600 : undefined} style={{ fontVariantNumeric: "tabular-nums" }}>
      {s.count}
    </Text>
  )
}

/**
 * InboxNav 是放进二级导航（nav 插槽）的收件箱列表：我的、@我、未指派、全部，各团队收件箱，视图（V1 中加入），
 * 每一项带数量，随实时推送更新。团队收件箱使用独立的 RailSection 分组与首字小方块，和上方系统视图清晰区分。
 */
export function InboxNav({ search, go }: { search: InboxSearch; go: (s: Partial<InboxSearch>) => void }): ReactNode {
  const t = useT()
  const now = useNow()
  const th = useThresholds()
  const inbox = useQuery(kit.queries.conversation.inbox())
  const d = inbox.data
  const item = (box: InboxSearch["box"], label: string, icon: ReactNode, s?: ConversationSummary) => (
    <NavLink
      key={box}
      label={label}
      leftSection={icon}
      rightSection={<Count s={s} now={now} th={th} />}
      active={search.box === box && !search.team}
      onClick={() => go({ box, team: undefined })}
    />
  )
  return (
    <Fill name="nav">
      {item("mine", t("inbox.box_mine"), <IconInbox size={16} stroke={1.6} />, d?.mine)}
      {item("mentions", t("inbox.box_mentions"), <IconAt size={16} stroke={1.6} />, d?.mentions)}
      {item("unassigned", t("inbox.box_unassigned"), <IconUserQuestion size={16} stroke={1.6} />, d?.unassigned)}
      {item("all", t("inbox.box_all"), <IconListDetails size={16} stroke={1.6} />, d?.all)}
      {(d?.teams.length ?? 0) > 0 && (
        <RailSection title={t("inbox.teams")} collapsible>
          {d?.teams.map((team) => {
            const active = search.team === team.id
            return (
              <NavLink
                key={team.id}
                label={team.name}
                leftSection={
                  <span className="inbox-team-badge" data-active={active || undefined}>
                    {team.name.slice(0, 1)}
                  </span>
                }
                rightSection={<Count s={team} now={now} th={th} />}
                active={active}
                onClick={() => go({ box: "all", team: team.id })}
              />
            )
          })}
        </RailSection>
      )}
    </Fill>
  )
}
