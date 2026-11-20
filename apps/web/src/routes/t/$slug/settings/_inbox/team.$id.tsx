import { ActionIcon, Avatar, Badge, Button, Group, Select, Stack, Table, Text } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconTrash } from "@tabler/icons-react"
import { confirm, notifyError, notifySuccess, Page } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../api"

// 一个团队收件箱的成员：从工作区的成员中选人加入，移出团队收件箱。
export const Route = createFileRoute("/t/$slug/settings/_inbox/team/$id")({
  staticData: { title: "团队收件箱成员", permission: "teams:manage" },
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
                <Table.Td colSpan={3}>
                  <Text c="dimmed" size="sm" ta="center">
                    团队收件箱中还没有成员
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
