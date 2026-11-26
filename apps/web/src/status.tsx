import { Badge, Checkbox, Group, SegmentedControl, Skeleton, Stack, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useParams } from "@tanstack/react-router"
import { notifyError, useLanguage, useT } from "@go-tenon/ui"
import type { MemberStatus, MemberStatusSetting } from "@agents-desk/client/tenant"
import type { ReactNode } from "react"
import { kit, tenantApi } from "./api"
import { StatusMark } from "./components/status-mark"

const statuses: MemberStatus[] = ["online", "away"]

// statusColor 是在线状态的颜色：在线是 green，离开是 gray（没有状态）。
const statusColor: Record<MemberStatus, string> = { online: "green", away: "gray" }

/** StatusBadge 显示一位成员的在线状态，成员页和团队收件箱的成员表格用它。 */
export function StatusBadge({ status }: { status: MemberStatus }): ReactNode {
  useLanguage() // 枚举的显示文字按当前语言，切换语言时重新渲染
  return (
    <Badge variant="dot" color={statusColor[status]}>
      {kit.label("MemberStatus", status)}
    </Badge>
  )
}

/** StatusLabel 是在线状态的文字，前面是一个小圆点：在线是 green，离开是 gray。 */
function StatusLabel({ status }: { status: MemberStatus }): ReactNode {
  useLanguage() // 枚举的显示文字按当前语言，切换语言时重新渲染
  return (
    <Group gap={6} wrap="nowrap" justify="center">
      <StatusMark color={statusColor[status]} />
      {kit.label("MemberStatus", status)}
    </Group>
  )
}

/**
 * StatusControl 是我在当前工作区的在线状态：在线、离开两段切换；离开时下面是“把我的会话重新分配”。修改后立即保存，
 * 并刷新成员列表和团队收件箱的成员，让其中的状态跟着变。放在个人菜单中。
 */
export function StatusControl(): ReactNode {
  const { slug } = useParams({ from: "/t/$slug" })
  const queryClient = useQueryClient()
  const t = useT()
  const queryKey = ["me", "status", slug]
  const current = useQuery({ queryKey, queryFn: () => tenantApi.member.myStatus() })
  const save = useMutation({
    mutationFn: (s: MemberStatusSetting) => tenantApi.member.myStatusUpdate(s),
    onSuccess: (s) => {
      queryClient.setQueryData(queryKey, s)
      void queryClient.invalidateQueries({ queryKey: kit.queries.member.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.team.key() })
    },
    onError: (err) => notifyError(err),
  })
  const value = save.isPending ? save.variables : current.data
  if (!value) return <Skeleton h={30} radius="md" />
  return (
    <Stack gap="xs">
      <Tooltip label={t("status.description")} multiline w={220} withArrow openDelay={300}>
        <SegmentedControl
          fullWidth
          size="xs"
          value={value.status}
          onChange={(s) => save.mutate({ ...value, status: s as MemberStatus })}
          data={statuses.map((s) => ({ value: s, label: <StatusLabel status={s} /> }))}
        />
      </Tooltip>
      {value.status === "away" && (
        <Checkbox
          size="xs"
          label={t("status.reassign")}
          description={t("status.reassign_hint")}
          checked={value.reassign}
          onChange={(e) => save.mutate({ ...value, reassign: e.currentTarget.checked })}
        />
      )}
    </Stack>
  )
}
