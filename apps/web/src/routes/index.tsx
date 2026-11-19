import { Badge, Button, Group, Paper, Stack, Text } from "@mantine/core"
import { createFileRoute, Link, redirect } from "@tanstack/react-router"
import { CenteredAuthLayout, requireAuth } from "@go-tenon/ui"
import { api } from "../api"
import { logout } from "../auth"

// 登录后的去处：只属于一个可以进入的工作区时直接进入；属于多个时在这里选择；一个都没有时提示联系管理员。
export const Route = createFileRoute("/")({
  beforeLoad: requireAuth,
  loader: async () => {
    const mine = await api.tenant.mine()
    const active = mine.filter((t) => t.status === "active")
    if (active.length === 1 && mine.length === 1) throw redirect({ to: "/t/$slug", params: { slug: active[0].slug } })
    return mine
  },
  component: ChooseTenant,
})

function ChooseTenant() {
  const mine = Route.useLoaderData()
  return (
    <CenteredAuthLayout brand="agents-desk" title="选择工作区">
      <Stack gap="sm">
        {mine.length === 0 && <Text c="dimmed">你还不属于任何工作区，请联系工作区的管理员把你加入。</Text>}
        {mine.map((t) =>
          t.status === "active" ? (
            <Link key={t.slug} to="/t/$slug" params={{ slug: t.slug }} style={{ color: "inherit", textDecoration: "none" }}>
              <Paper withBorder p="md">
                <Text fw={500}>{t.name}</Text>
                <Text size="sm" c="dimmed">
                  /t/{t.slug}/
                </Text>
              </Paper>
            </Link>
          ) : (
            <Paper key={t.slug} withBorder p="md">
              <Group justify="space-between">
                <div>
                  <Text fw={500} c="dimmed">
                    {t.name}
                  </Text>
                  <Text size="sm" c="dimmed">
                    /t/{t.slug}/
                  </Text>
                </div>
                <Badge variant="light" color="gray">
                  已停用
                </Badge>
              </Group>
            </Paper>
          ),
        )}
        <Group justify="flex-end">
          <Button variant="subtle" color="gray" onClick={() => void logout()}>
            退出登录
          </Button>
        </Group>
      </Stack>
    </CenteredAuthLayout>
  )
}
