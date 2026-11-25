import { Badge, Button, Group, Paper, Stack, Text } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, redirect } from "@tanstack/react-router"
import { IconChevronRight } from "@tabler/icons-react"
import { CenteredAuthLayout, requireAuth, useApp, useT } from "@go-tenon/ui"
import { api, apiKit } from "../api"
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
  const app = useApp()
  const t = useT()
  const site = useQuery(apiKit.queries.site.get())
  const signOut = async () => {
    try {
      await logout()
    } finally {
      app.session.signOut()
    }
  }
  return (
    <CenteredAuthLayout brand={site.data?.site_name ?? ""} title={t("choose_tenant.title")}>
      <Stack gap="sm">
        {mine.length === 0 && <Text c="dimmed">{t("choose_tenant.none")}</Text>}
        {mine.map((tn) =>
          tn.status === "active" ? (
            <Link key={tn.slug} to="/t/$slug" params={{ slug: tn.slug }} className="desk-tenant-card">
              <Paper withBorder p="md" className="desk-tenant-paper">
                <Group justify="space-between" wrap="nowrap">
                  <div>
                    <Text fw={500}>{tn.name}</Text>
                    <Text size="sm" c="dimmed">
                      /t/{tn.slug}/
                    </Text>
                  </div>
                  <IconChevronRight size={16} className="desk-tenant-arrow" style={{ flex: "none" }} />
                </Group>
              </Paper>
            </Link>
          ) : (
            <Paper key={tn.slug} withBorder p="md">
              <Group justify="space-between">
                <div>
                  <Text fw={500} c="dimmed">
                    {tn.name}
                  </Text>
                  <Text size="sm" c="dimmed">
                    /t/{tn.slug}/
                  </Text>
                </div>
                <Badge variant="light" color="gray">
                  {t("choose_tenant.disabled")}
                </Badge>
              </Group>
            </Paper>
          ),
        )}
        <Group justify="flex-end">
          <Button variant="subtle" color="gray" onClick={() => void signOut()}>
            {t("choose_tenant.sign_out")}
          </Button>
        </Group>
      </Stack>
    </CenteredAuthLayout>
  )
}
