import {
  Anchor,
  Avatar,
  Badge,
  Box,
  Button,
  Divider,
  Group,
  Paper,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { IconArrowLeft, IconChevronRight, IconLogout, IconSearch } from "@tabler/icons-react"
import {
  ColorSchemeToggle,
  LanguageMenu,
  requireAuth,
  useApp,
  useDocumentTitle,
  useFavicon,
  useLanguage,
  useT,
} from "@go-tenon/ui"
import { useMemo, useState } from "react"
import { api, apiKit } from "../api"
import { logout, me } from "../auth"
import { brandLogo } from "../brand-logo"
import { i18n } from "../i18n"
import { readLastWorkspace, UI_LANGUAGES } from "../workspace"

// 独立工作区总览与选择页 /app/workspaces：
// 允许随时主动进入查看所有活跃与停用工作区并切换，不强制重定向离开。
export const Route = createFileRoute("/workspaces")({
  beforeLoad: async (ctx) => {
    await requireAuth(ctx)
    // 回到账号级选区页时清除会话中残留的旧工作区上下文
    const identity = ctx.context.app.session.identity()
    if (identity?.tenant) {
      ctx.context.app.session.setIdentity({ account: identity.account })
    }
  },
  loader: () => api.tenant.mine(),
  component: WorkspacesPage,
})

function WorkspacesPage() {
  const mine = Route.useLoaderData()
  const app = useApp()
  const t = useT()
  const language = useLanguage()
  const site = useQuery(apiKit.queries.site.get())
  const user = useQuery({ queryKey: ["me"], queryFn: me })
  const brandName = site.data?.site_name || t("app.web")
  useFavicon(site.data?.logo_url || undefined)
  useDocumentTitle(`${t("choose_tenant.title")} - ${brandName}`)

  const [search, setSearch] = useState("")
  const lastSlug = readLastWorkspace(app, user.data?.id ?? app.session.identity()?.account)

  const { activeList, disabledList } = useMemo(() => {
    const kw = search.trim().toLowerCase()
    const filtered = mine.filter(
      (tn) => !kw || tn.name.toLowerCase().includes(kw) || tn.slug.toLowerCase().includes(kw),
    )
    return {
      activeList: filtered.filter((tn) => tn.status === "active"),
      disabledList: filtered.filter((tn) => tn.status !== "active"),
    }
  }, [mine, search])

  const signOut = async () => {
    try {
      await logout()
    } finally {
      app.session.signOut()
    }
  }

  return (
    <div className="tn-auth desk-auth-shell">
      <div className="desk-auth-topbar">
        <Anchor href="/" size="sm" c="dimmed" className="desk-auth-back">
          <Group gap={4} wrap="nowrap">
            <IconArrowLeft size={15} stroke={1.8} />
            <span>{t("workspace.back_home")}</span>
          </Group>
        </Anchor>
        <Group gap="xs" wrap="nowrap">
          <LanguageMenu languages={UI_LANGUAGES} value={language} onChange={(v) => i18n.setLanguage(v)} />
          <ColorSchemeToggle />
        </Group>
      </div>

      <Stack gap="lg" w="100%" maw={480} align="stretch">
        <Group justify="center" gap="xs">
          {brandLogo(site.data?.logo_url, brandName, 32)}
          <Text fw={600} size="lg">
            {brandName}
          </Text>
        </Group>

        <div className="tn-auth-card desk-workspaces-card">
          <Stack gap="md">
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <Stack gap={2}>
                <Title order={3}>{t("choose_tenant.title")}</Title>
                {mine.length > 0 && (
                  <Text size="sm" c="dimmed">
                    {t("workspace.count", { n: mine.length })}
                  </Text>
                )}
              </Stack>
              {user.data && (
                <Group gap="xs" wrap="nowrap" className="desk-account-pill">
                  <Avatar src={user.data.avatar_url || null} size={22} radius="xl" color="brand">
                    {user.data.name.slice(0, 1)}
                  </Avatar>
                  <Text size="xs" c="dimmed" truncate maw={160}>
                    {user.data.email}
                  </Text>
                </Group>
              )}
            </Group>

            {mine.length > 5 && (
              <TextInput
                placeholder={t("workspace.search_placeholder")}
                leftSection={<IconSearch size={15} stroke={1.6} />}
                value={search}
                onChange={(e) => setSearch(e.currentTarget.value)}
              />
            )}

            {mine.length === 0 ? (
              <Paper withBorder p="lg" radius="md" className="desk-workspaces-empty">
                <Text size="sm" c="dimmed" ta="center">
                  {t("choose_tenant.none")}
                </Text>
              </Paper>
            ) : activeList.length === 0 && disabledList.length === 0 ? (
              <Text size="sm" c="dimmed" ta="center" py="md">
                {t("workspace.no_match")}
              </Text>
            ) : (
              <Stack gap="sm">
                {activeList.map((tn) => {
                  const isLast = tn.slug === lastSlug
                  return (
                    <Link key={tn.slug} to="/$slug" params={{ slug: tn.slug }} className="desk-tenant-card">
                      <Paper withBorder p="md" radius="md" className="desk-tenant-paper">
                        <Group justify="space-between" wrap="nowrap">
                          <Group gap="sm" wrap="nowrap" style={{ minWidth: 0 }}>
                            <Avatar src={tn.logo_url || null} size={38} radius="md" color="brand" variant="filled">
                              {tn.name.slice(0, 1)}
                            </Avatar>
                            <Box style={{ minWidth: 0 }}>
                              <Group gap="xs" wrap="nowrap">
                                <Text fw={600} size="sm" truncate>
                                  {tn.name}
                                </Text>
                                {isLast && (
                                  <Badge size="xs" variant="light" color="brand" style={{ flex: "none" }}>
                                    {t("workspace.last_visited")}
                                  </Badge>
                                )}
                              </Group>
                              <Text size="xs" c="dimmed" truncate>
                                /app/{tn.slug}/
                              </Text>
                            </Box>
                          </Group>
                          <IconChevronRight size={16} className="desk-tenant-arrow" style={{ flex: "none" }} />
                        </Group>
                      </Paper>
                    </Link>
                  )
                })}

                {disabledList.length > 0 && (
                  <>
                    {activeList.length > 0 && <Divider label={t("workspace.disabled_section")} labelPosition="left" />}
                    {disabledList.map((tn) => (
                      <Paper key={tn.slug} withBorder p="md" radius="md" className="desk-tenant-disabled">
                        <Group justify="space-between" wrap="nowrap">
                          <Group gap="sm" wrap="nowrap" style={{ minWidth: 0 }}>
                            <Avatar src={tn.logo_url || null} size={38} radius="md" color="gray" variant="light">
                              {tn.name.slice(0, 1)}
                            </Avatar>
                            <Box style={{ minWidth: 0 }}>
                              <Text fw={500} size="sm" c="dimmed" truncate>
                                {tn.name}
                              </Text>
                              <Text size="xs" c="dimmed" truncate>
                                /app/{tn.slug}/
                              </Text>
                            </Box>
                          </Group>
                          <Badge variant="light" color="gray" style={{ flex: "none" }}>
                            {t("choose_tenant.disabled")}
                          </Badge>
                        </Group>
                      </Paper>
                    ))}
                  </>
                )}
              </Stack>
            )}

            <Divider />
            <Group justify="space-between" align="center">
              <Text size="xs" c="dimmed">
                {user.data ? t("workspace.signed_in_as", { name: user.data.name }) : ""}
              </Text>
              <Button
                variant="subtle"
                color="gray"
                size="xs"
                leftSection={<IconLogout size={14} stroke={1.6} />}
                onClick={() => void signOut()}
              >
                {t("choose_tenant.sign_out")}
              </Button>
            </Group>
          </Stack>
        </div>
      </Stack>
    </div>
  )
}
