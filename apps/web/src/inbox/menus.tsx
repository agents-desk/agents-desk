import type { ConversationDTO } from "@agents-desk/client/tenant"
import { ActionIcon, Button, Group, Menu, Modal, Stack, Tooltip } from "@mantine/core"
import { DateTimePicker } from "@mantine/dates"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconChevronDown, IconClock, IconUserPlus } from "@tabler/icons-react"
import { inTimeZone, notifyError, useCan, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../api"
import { useMe, useMembers, useTeams } from "./data"

/** invalidate 让会话分组的查询失效：列表、数量、会话和时间线重新读取。 */
function useInvalidate() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
}

/**
 * AssignMenu 是指派的菜单：指派给我，其他成员，团队收件箱（转到别的团队收件箱时服务端清空负责人），取消负责人。
 * 没有“把会话改派给别人”的权限时只能领取；服务端仍会检查。
 */
export function AssignMenu({ c, compact }: { c: ConversationDTO; compact?: boolean }): ReactNode {
  const t = useT()
  const can = useCan()
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams().filter((x) => x.status === "active")
  const invalidate = useInvalidate()
  const assign = useMutation({
    mutationFn: (body: { assignee_id?: string; team_id?: string }) => tenantApi.conversation.assign(c.id, body),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const others = can("conversations:assign")
  const mine = c.assignee?.type === "member" && c.assignee.id === me.data?.id
  return (
    <Menu position="bottom-end" withinPortal>
      <Menu.Target>
        {compact ? (
          <Tooltip label={t("inbox.assign")}>
            <ActionIcon variant="subtle" color="gray" size="input-sm" loading={assign.isPending} aria-label={t("inbox.assign")}>
              <IconUserPlus size={16} />
            </ActionIcon>
          </Tooltip>
        ) : (
          <Button variant="subtle" color="gray" leftSection={<IconUserPlus size={14} />} rightSection={<IconChevronDown size={12} />} loading={assign.isPending}>
            {t("inbox.assign")}
          </Button>
        )}
      </Menu.Target>
      <Menu.Dropdown mah={420} style={{ overflowY: "auto" }}>
        {me.data && !mine && <Menu.Item onClick={() => assign.mutate({ assignee_id: me.data.id })}>{t("inbox.assign_me")}</Menu.Item>}
        {others && (
          <>
            <Menu.Label>{t("inbox.assign_members")}</Menu.Label>
            {members
              .filter((m) => m.id !== me.data?.id)
              .map((m) => (
                <Menu.Item
                  key={m.id}
                  disabled={c.assignee?.id === m.id}
                  rightSection={m.status === "online" ? undefined : t("inbox.away")}
                  onClick={() => assign.mutate({ assignee_id: m.id })}
                >
                  {m.name}
                </Menu.Item>
              ))}
            <Menu.Label>{t("inbox.assign_teams")}</Menu.Label>
            {teams.map((team) => (
              <Menu.Item key={team.id} disabled={c.team_id === team.id} onClick={() => assign.mutate({ team_id: team.id })}>
                {team.name}
              </Menu.Item>
            ))}
            {c.assignee && (
              <>
                <Menu.Divider />
                <Menu.Item onClick={() => assign.mutate({ assignee_id: "" })}>{t("inbox.unassign")}</Menu.Item>
              </>
            )}
          </>
        )}
      </Menu.Dropdown>
    </Menu>
  )
}

/** snoozeOptions 返回稍后处理的选项：今天晚些时候（3 小时后）、明天 9 点、下周一 9 点、一周后、一个月后，按成员的时区算出。 */
export function snoozeOptions(zone: string | undefined): { key: "later" | "tomorrow" | "monday" | "week" | "month"; until: Date }[] {
  const now = inTimeZone(new Date(), zone)
  const at9 = (d: typeof now) => d.hour(9).minute(0).second(0).millisecond(0)
  const daysToMonday = ((8 - now.day()) % 7) || 7
  return [
    { key: "later", until: now.add(3, "hour").toDate() },
    { key: "tomorrow", until: at9(now.add(1, "day")).toDate() },
    { key: "monday", until: at9(now.add(daysToMonday, "day")).toDate() },
    { key: "week", until: now.add(1, "week").toDate() },
    { key: "month", until: now.add(1, "month").toDate() },
  ]
}

/** useSnooze 返回把会话 id 稍后处理到 until 的操作。 */
export function useSnooze(id: string) {
  const invalidate = useInvalidate()
  return useMutation({
    mutationFn: (until: Date) => tenantApi.conversation.snooze(id, { until: until.toISOString() }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
}

/** SnoozeMenu 是稍后处理的菜单：几个常用的时间，以及自定义（选择日期和时间）。 */
export function SnoozeMenu({ id, compact }: { id: string; compact?: boolean }): ReactNode {
  const t = useT()
  const zone = useTimeZone()
  const snooze = useSnooze(id)
  const [custom, setCustom] = useState(false)
  const [until, setUntil] = useState<string | null>(null)
  return (
    <>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          {compact ? (
            <Tooltip label={t("inbox.snooze")}>
              <ActionIcon variant="subtle" color="gray" size="input-sm" loading={snooze.isPending} aria-label={t("inbox.snooze")}>
                <IconClock size={16} />
              </ActionIcon>
            </Tooltip>
          ) : (
            <Button variant="subtle" color="gray" leftSection={<IconClock size={14} />} rightSection={<IconChevronDown size={12} />} loading={snooze.isPending}>
              {t("inbox.snooze")}
            </Button>
          )}
        </Menu.Target>
        <Menu.Dropdown>
          {snoozeOptions(zone).map((o) => (
            <Menu.Item key={o.key} onClick={() => snooze.mutate(o.until)}>
              {t(`inbox.snooze_${o.key}` as const)}
            </Menu.Item>
          ))}
          <Menu.Divider />
          <Menu.Item onClick={() => setCustom(true)}>{t("inbox.snooze_custom")}</Menu.Item>
        </Menu.Dropdown>
      </Menu>
      <Modal opened={custom} onClose={() => setCustom(false)} title={t("inbox.snooze_custom")}>
        <Stack>
          <DateTimePicker value={until} onChange={setUntil} minDate={new Date()} label={t("inbox.snooze_until")} timePickerProps={{ withDropdown: true }} />
          <Group justify="flex-end">
            <Button
              disabled={!until}
              loading={snooze.isPending}
              onClick={() => {
                if (!until) return
                snooze.mutate(new Date(until), { onSuccess: () => setCustom(false) })
              }}
            >
              {t("inbox.snooze")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  )
}
