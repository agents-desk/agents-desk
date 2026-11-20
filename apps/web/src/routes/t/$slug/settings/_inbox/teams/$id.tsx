import { ActionIcon, Avatar, Badge, Button, Group, Radio, Select, Stack, Table, Tabs, Text } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconTrash } from "@tabler/icons-react"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, confirm, FormError, notifyError, notifySuccess, Page } from "@go-tenon/ui"
import type { HoursSchedule, TeamDTO } from "@agents-desk/client/tenant"
import { type ReactNode, useEffect, useState } from "react"
import { tenantApi } from "../../../../../../api"
import { emptySchedule, ScheduleFields } from "../../../../../../business-hours"
import { StatusBadge } from "../../../../../../status"

// 一个团队收件箱：成员（从工作区的成员中选人加入，移出团队收件箱）和工作时间（使用工作区的，或者单独设置），两个标签各自保存。权限来自上级的 teams 布局，菜单中“团队收件箱”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_inbox/teams/$id")({
  staticData: { title: "团队收件箱成员" },
  component: TeamMembers,
})

function TeamMembers(): ReactNode {
  const { slug, id } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const team = useQuery({ queryKey: ["team", id], queryFn: () => tenantApi.team.get(id) })
  const members = useQuery({ queryKey: ["team", id, "members"], queryFn: () => tenantApi.team.members(id) })
  // 可以加入的人：工作区的成员中还不在这个团队收件箱里的
  const everyone = useQuery({ queryKey: ["members", "all"], queryFn: () => tenantApi.member.list({ per_page: 100 }) })
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["team", id] })

  const put = useMutation({
    mutationFn: (memberID: string) => tenantApi.team.memberPut(id, memberID),
    onSuccess: refresh,
    onError: (err) => notifyError(err),
  })
  const remove = useMutation({
    mutationFn: (memberID: string) => tenantApi.team.memberRemove(id, memberID),
    onSuccess: refresh,
    onError: (err) => notifyError(err),
  })

  const [adding, setAdding] = useState<string | null>(null)
  const inTeam = new Set((members.data ?? []).map((m) => m.member_id))
  const candidates = (everyone.data?.items ?? []).filter((m) => !inTeam.has(m.id))
  const add = async () => {
    if (!adding) return
    await put.mutateAsync(adding)
    notifySuccess("已加入团队收件箱")
    setAdding(null)
  }

  return (
    <Page
      title={team.data ? `团队收件箱：${team.data.name}` : "团队收件箱成员"}
      description={team.data?.description || undefined}
      // 标题左边的返回回到团队收件箱列表，而不是离开设置
      back={() => void navigate({ to: "/t/$slug/settings/teams", params: { slug } })}
    >
      <Tabs defaultValue="members" keepMounted={false}>
        <Tabs.List>
          <Tabs.Tab value="members">成员</Tabs.Tab>
          <Tabs.Tab value="hours">工作时间</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="members" pt="md">
          <Stack gap="md">
            <Group align="flex-end" gap="sm">
              <Select
                label="加入成员"
                placeholder="选择工作区的成员"
                searchable
                data={candidates.map((m) => ({ value: m.id, label: `${m.name}（${m.role_name}）` }))}
                value={adding}
                onChange={setAdding}
                nothingFoundMessage="没有可以加入的成员"
                style={{ flex: 1 }}
              />
              <Button onClick={() => void add()} disabled={!adding} loading={put.isPending && !!adding}>
                加入
              </Button>
            </Group>

            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>成员</Table.Th>
                  <Table.Th>角色</Table.Th>
                  <Table.Th>在线状态</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(members.data ?? []).map((m) => (
                  <Table.Tr key={m.member_id}>
                    <Table.Td>
                      <Group gap="sm" wrap="nowrap">
                        <Avatar src={m.avatar_url || null} size={28} radius="xl">
                          {m.name.slice(0, 1)}
                        </Avatar>
                        <div>
                          <Text size="sm">{m.name}</Text>
                          <Text size="xs" c="dimmed">
                            {m.email}
                          </Text>
                        </div>
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color="gray">
                        {m.role_name}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <StatusBadge status={m.status} />
                    </Table.Td>
                    <Table.Td align="right">
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={`把 ${m.name} 移出团队收件箱`}
                        onClick={async () => {
                          if (await confirm({ title: `把 ${m.name} 移出团队收件箱？`, message: "他仍是工作区的成员。", danger: true, confirmLabel: "移出" }))
                            remove.mutate(m.member_id)
                        }}
                      >
                        <IconTrash size={16} stroke={1.6} />
                      </ActionIcon>
                    </Table.Td>
                  </Table.Tr>
                ))}
                {members.data?.length === 0 && (
                  <Table.Tr>
                    <Table.Td colSpan={4}>
                      <Text c="dimmed" size="sm" ta="center">
                        团队收件箱中还没有成员
                      </Text>
                    </Table.Td>
                  </Table.Tr>
                )}
              </Table.Tbody>
            </Table>
          </Stack>
        </Tabs.Panel>
        <Tabs.Panel value="hours" pt="md">
          {team.data && <TeamHours team={team.data} />}
        </Tabs.Panel>
      </Tabs>
    </Page>
  )
}

/** TeamHours 是团队收件箱的工作时间：使用工作区的，或者单独设置时区和每周的工作时间。特殊日期和回复时间总是工作区的。 */
function TeamHours({ team }: { team: TeamDTO }): ReactNode {
  const queryClient = useQueryClient()
  const workspace = useQuery({ queryKey: ["business-hours"], queryFn: () => tenantApi.hours.get() })
  const initial = (own: HoursSchedule | undefined) => ({ custom: own ? "custom" : "workspace", schedule: own ?? emptySchedule })
  const form = useForm<{ custom: string; schedule: HoursSchedule }>({ initialValues: initial(team.business_hours) })
  const [error, setError] = useState<ErrorInfo>()
  const [saving, setSaving] = useState(false)
  // 第一次改为单独设置时，从工作区的工作时间开始改
  useEffect(() => {
    if (!team.business_hours && workspace.data) {
      const { time_zone, week } = workspace.data
      form.setInitialValues({ custom: "workspace", schedule: { time_zone, week } })
      form.setValues({ custom: "workspace", schedule: { time_zone, week } })
    }
  }, [workspace.data])
  const custom = form.getValues().custom === "custom"
  return (
    <form
      noValidate
      onSubmit={form.onSubmit(async (values) => {
        setSaving(true)
        setError(undefined)
        try {
          const saved = await tenantApi.team.businessHours(team.id, values.custom === "custom" ? { schedule: values.schedule } : {})
          queryClient.setQueryData(["team", team.id], saved)
          form.resetDirty()
          notifySuccess("已保存")
        } catch (err) {
          setError(applyServerErrors(form, err))
        } finally {
          setSaving(false)
        }
      })}
    >
      <Stack gap="md">
        <FormError error={error} />
        <Radio.Group label="工作时间" {...form.getInputProps("custom")}>
          <Stack gap="xs" mt="xs">
            <Radio value="workspace" label="使用工作区的工作时间" />
            <Radio value="custom" label="单独设置时区和每周的工作时间" />
          </Stack>
        </Radio.Group>
        <Text size="sm" c="dimmed">
          特殊日期和回复时间对整个工作区有效，在“设置 → 工作时间”中修改。
        </Text>
        {custom && <ScheduleFields form={form} path="schedule" />}
        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty()} loading={saving}>
            保存
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
