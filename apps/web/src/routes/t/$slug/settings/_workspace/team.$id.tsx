import { ActionIcon, Avatar, Badge, Button, Checkbox, Group, Select, Stack, Switch, Table, Text, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconTrash } from "@tabler/icons-react"
import { confirm, notifyError, notifySuccess, Page } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"

// 一个团队的成员：从工作区的成员中选人加入，切换组长，移出团队。只有组长、管理员和主管理员能当组长。
export const Route = createFileRoute("/t/$slug/settings/_workspace/team/$id")({
  staticData: { title: "团队成员", permission: "teams:manage" },
  component: TeamMembers,
})

function TeamMembers(): ReactNode {
  const { id } = Route.useParams()
  const queryClient = useQueryClient()
  const team = useQuery({ queryKey: ["team", id], queryFn: () => tenantApi.team.get(id) })
  const members = useQuery({ queryKey: ["team", id, "members"], queryFn: () => tenantApi.team.members(id) })
  // 可以加入的人：工作区的成员中还不在这个团队里的
  const everyone = useQuery({ queryKey: ["members", "all"], queryFn: () => tenantApi.member.list({ per_page: 100 }) })
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["team", id] })

  const put = useMutation({
    mutationFn: ({ memberID, lead }: { memberID: string; lead: boolean }) => tenantApi.team.memberPut(id, memberID, { lead }),
    onSuccess: refresh,
    onError: (err) => notifyError(err),
  })
  const remove = useMutation({
    mutationFn: (memberID: string) => tenantApi.team.memberRemove(id, memberID),
    onSuccess: refresh,
    onError: (err) => notifyError(err),
  })

  const [adding, setAdding] = useState<string | null>(null)
  const [addAsLead, setAddAsLead] = useState(false)
  const inTeam = new Set((members.data ?? []).map((m) => m.member_id))
  const candidates = (everyone.data?.items ?? []).filter((m) => !inTeam.has(m.id))
  const candidateRole = candidates.find((m) => m.id === adding)?.role
  const add = async () => {
    if (!adding) return
    await put.mutateAsync({ memberID: adding, lead: addAsLead })
    notifySuccess("已加入团队")
    setAdding(null)
    setAddAsLead(false)
  }

  return (
    <Page title={team.data ? `团队：${team.data.name}` : "团队成员"} description={team.data?.description || undefined}>
      <Stack gap="md">
        <Group align="flex-end" gap="sm">
          <Select
            label="加入成员"
            placeholder="选择工作区的成员"
            searchable
            data={candidates.map((m) => ({ value: m.id, label: `${m.name}（${kit.label("Role", m.role)}）` }))}
            value={adding}
            onChange={(v) => {
              setAdding(v)
              setAddAsLead(false)
            }}
            nothingFoundMessage="没有可以加入的成员"
            style={{ flex: 1 }}
          />
          <Tooltip label="坐席不能当组长" disabled={candidateRole !== "agent"}>
            <Checkbox
              label="设为组长"
              checked={addAsLead}
              disabled={!adding || candidateRole === "agent"}
              onChange={(e) => setAddAsLead(e.currentTarget.checked)}
              mb={8}
            />
          </Tooltip>
          <Button onClick={() => void add()} disabled={!adding} loading={put.isPending && !!adding}>
            加入
          </Button>
        </Group>

        <Table verticalSpacing="sm">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>成员</Table.Th>
              <Table.Th>角色</Table.Th>
              <Table.Th>组长</Table.Th>
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
                    {kit.label("Role", m.role)}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Tooltip label="坐席不能当组长，先把他的角色改为组长" disabled={m.role !== "agent"}>
                    <Switch
                      aria-label={`${m.name} 是组长`}
                      checked={m.lead}
                      disabled={m.role === "agent" && !m.lead}
                      onChange={(e) => put.mutate({ memberID: m.member_id, lead: e.currentTarget.checked })}
                    />
                  </Tooltip>
                </Table.Td>
                <Table.Td align="right">
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    aria-label={`把 ${m.name} 移出团队`}
                    onClick={async () => {
                      if (await confirm({ title: `把 ${m.name} 移出团队？`, message: "他仍是工作区的成员。", danger: true, confirmLabel: "移出" }))
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
                    团队中还没有成员
                  </Text>
                </Table.Td>
              </Table.Tr>
            )}
          </Table.Tbody>
        </Table>
      </Stack>
    </Page>
  )
}
