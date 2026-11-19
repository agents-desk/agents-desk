import { Avatar, Badge, Card, Group, SimpleGrid, Stack, Text } from "@mantine/core"
import { useQueries, useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { tenantApi } from "../../../../../api"

// 我的团队：我所在的团队、每个团队的组长和成员，只读。团队由管理员在“工作区 → 团队”中管理。
export const Route = createFileRoute("/t/$slug/settings/_personal/my-teams")({
  staticData: { title: "我的团队", icon: "team", order: 2 },
  component: MyTeams,
})

function MyTeams(): ReactNode {
  const mine = useQuery({ queryKey: ["teams", "mine"], queryFn: () => tenantApi.team.list({ mine: true, per_page: 100 }) })
  const teams = mine.data?.items ?? []
  const members = useQueries({
    queries: teams.map((t) => ({ queryKey: ["team", t.id, "members"], queryFn: () => tenantApi.team.members(t.id) })),
  })
  return (
    <Page title="我的团队">
      {mine.isSuccess && teams.length === 0 && <Text c="dimmed">你还不在任何团队中，需要时请联系工作区的管理员。</Text>}
      <SimpleGrid cols={{ base: 1, md: 2 }}>
        {teams.map((t, i) => (
          <Card key={t.id} withBorder>
            <Stack gap="sm">
              <div>
                <Text fw={600}>{t.name}</Text>
                {t.description && (
                  <Text size="sm" c="dimmed">
                    {t.description}
                  </Text>
                )}
              </div>
              {(members[i]?.data ?? []).map((m) => (
                <Group key={m.member_id} gap="sm" wrap="nowrap">
                  <Avatar src={m.avatar_url || null} size={28} radius="xl">
                    {m.name.slice(0, 1)}
                  </Avatar>
                  <Text size="sm" style={{ flex: 1 }}>
                    {m.name}
                  </Text>
                  {m.lead && <Badge variant="light">组长</Badge>}
                </Group>
              ))}
            </Stack>
          </Card>
        ))}
      </SimpleGrid>
    </Page>
  )
}
