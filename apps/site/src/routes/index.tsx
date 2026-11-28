import { Badge, Button, Card, Container, Group, SimpleGrid, Stack, Text, ThemeIcon, Title } from "@mantine/core"
import { IconArrowRight, IconInbox, IconLanguage, IconLayersSubtract } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import {
  ColorSchemeToggle,
  LanguageMenu,
  type LanguageOption,
  SiteLayout,
  useFavicon,
  useLanguage,
  useSignedIn,
  useT,
} from "@go-tenon/ui/core"
import { kit } from "../api"
import { i18n } from "../i18n"

const UI_LANGUAGES: LanguageOption[] = [
  { value: "zh-CN", label: "简体中文" }, // i18n-ignore
  { value: "en", label: "English" },
]

// 品牌官网落地页入口 /：通过 SiteLayout 组织页头与页脚，登录态下顶栏与主按钮直接引导进入工作台 /app/。
export const Route = createFileRoute("/")({ component: Home })

function Home() {
  const t = useT()
  const language = useLanguage()
  const signedIn = useSignedIn()
  const site = useQuery({ ...kit.queries.site.get(), enabled: typeof window !== "undefined" })
  const brandName = site.data?.site_name || t("app.site")
  useFavicon(site.data?.logo_url || undefined)

  const logo = site.data?.logo_url ? (
    <img
      src={site.data.logo_url}
      alt={brandName}
      style={{ width: 26, height: 26, borderRadius: "var(--mantine-radius-md)", objectFit: "contain", flex: "none" }}
    />
  ) : undefined

  const actions = (
    <Group gap="xs" wrap="nowrap">
      <LanguageMenu languages={UI_LANGUAGES} value={language} onChange={(v) => i18n.setLanguage(v)} />
      <ColorSchemeToggle />
      {signedIn ? (
        <Button component="a" href="/app/" size="sm" rightSection={<IconArrowRight size={15} stroke={1.8} />}>
          {t("nav.open_workspace")}
        </Button>
      ) : (
        <Button component="a" href="/app/login" size="sm">
          {t("nav.sign_in")}
        </Button>
      )}
    </Group>
  )

  const pillars = [
    { icon: IconInbox, title: t("home.pillar_inbox_title"), desc: t("home.pillar_inbox_desc") },
    { icon: IconLanguage, title: t("home.pillar_ai_title"), desc: t("home.pillar_ai_desc") },
    { icon: IconLayersSubtract, title: t("home.pillar_workspace_title"), desc: t("home.pillar_workspace_desc") },
  ]

  const brand = logo ? (
    <Group gap="xs" wrap="nowrap">
      {logo}
      <span>{brandName}</span>
    </Group>
  ) : (
    brandName
  )

  return (
    <SiteLayout
      brand={brand}
      size="lg"
      documentTitle={brandName}
      actions={actions}
      note={t("footer.note", { brand: brandName })}
    >
      <Container size="lg" py={{ base: 64, md: 104 }}>
        <Stack gap={56}>
          <Stack gap="lg" align="flex-start" maw={720}>
            <Badge variant="light" color="brand" size="md">
              {t("home.badge")}
            </Badge>
            <Title order={1}>{t("home.headline")}</Title>
            <Text size="lg" c="dimmed">
              {t("home.subline")}
            </Text>
            <Group gap="sm" mt="xs">
              <Button
                component="a"
                href={signedIn ? "/app/" : "/app/login"}
                size="md"
                rightSection={<IconArrowRight size={16} stroke={1.8} />}
              >
                {signedIn ? t("nav.open_workspace") : t("home.primary_cta")}
              </Button>
              {signedIn && (
                <Button component="a" href="/app/workspaces" size="md" variant="default">
                  {t("home.secondary_cta_workspaces")}
                </Button>
              )}
            </Group>
          </Stack>

          <SimpleGrid cols={{ base: 1, md: 3 }} spacing="lg">
            {pillars.map((item) => (
              <Card key={item.title} withBorder padding="xl" radius="md">
                <Stack gap="sm">
                  <ThemeIcon size={40} radius="md" variant="light" color="brand">
                    <item.icon size={20} stroke={1.7} />
                  </ThemeIcon>
                  <Title order={2} size="h4">{item.title}</Title>
                  <Text size="sm" c="dimmed">
                    {item.desc}
                  </Text>
                </Stack>
              </Card>
            ))}
          </SimpleGrid>
        </Stack>
      </Container>
    </SiteLayout>
  )
}
