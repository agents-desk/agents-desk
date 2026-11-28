import { Anchor, Box, Button, Code, Container, Group, Paper, Select, Skeleton, Stack, Text, Textarea, TextInput, Title } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { NotFoundPage, useT } from "@go-tenon/ui/core"
import { type ReactNode, useEffect, useState } from "react"
import { languageEndonym, WIDGET_LANGUAGES } from "../i18n"

// 演示页：/widget/demo?channel=<聊天插件>，只在开发环境中有。它模拟产品网站：照嵌入代码那样设置 window.agentsDesk、
// 加载 loader.js，右下角出现聊天窗口；可以用 dev user-token 签出的身份以用户登录、退出，也可以切换宿主语言或独立模式打开。
export const Route = createFileRoute("/demo")({
  validateSearch: (s: Record<string, unknown>) => ({ channel: typeof s.channel === "string" ? s.channel : "" }),
  component: import.meta.env.DEV ? Demo : NotFoundPage,
})

function Demo(): ReactNode {
  const t = useT()
  const { channel } = Route.useSearch()
  const navigate = Route.useNavigate()
  const [draft, setDraft] = useState(channel)
  const [userToken, setUserToken] = useState("")
  const [lang, setLang] = useState("")

  // 像产品网站一样嵌入：声明渠道，加载 loader.js
  useEffect(() => {
    if (!channel) return
    window.agentsDesk = { channel, ...(lang ? { lang } : {}) }
    const script = document.createElement("script")
    script.async = true
    script.src = "/widget/loader.js"
    document.body.appendChild(script)
  }, [channel])

  const command = (name: string, arg?: { user_token?: string; lang?: string }) => window.AgentsDesk?.(name, arg)
  const standaloneHref = `/widget/?channel=${encodeURIComponent(channel)}${lang ? `&lang=${encodeURIComponent(lang)}` : ""}`

  return (
    <Box mih="100vh" bg="var(--mantine-color-gray-0)">
      <Group px="xl" h={56} bg="var(--mantine-color-body)" style={{ borderBottom: "1px solid var(--mantine-color-gray-3)" }}>
        <Text fw={700}>{t("demo.shop")}</Text>
      </Group>
      <Container size="md" py="xl">
        <Stack gap="lg">
          <Title order={2}>{t("demo.title")}</Title>
          <Paper withBorder p="lg" radius="md">
            <Stack gap="sm">
              <Text size="sm" c="dimmed">
                {t("demo.channel_hint")}
              </Text>
              <Code block>go run ./cmd/agents-desk dev user-token --sub u-42 --name "Rahul Sharma"</Code>
              <TextInput label={t("demo.channel")} value={draft} onChange={(e) => setDraft(e.currentTarget.value.trim())} />
              <Group justify="flex-end">
                <Button onClick={() => void navigate({ search: { channel: draft } })} disabled={!draft || draft === channel}>
                  {t("demo.embed")}
                </Button>
              </Group>
            </Stack>
          </Paper>
          {channel && (
            <Paper withBorder p="lg" radius="md">
              <Stack gap="sm">
                <Group gap="xs" align="flex-end" wrap="wrap">
                  <Button variant="default" onClick={() => command("open")}>
                    {t("demo.open")}
                  </Button>
                  <Button variant="default" onClick={() => command("close")}>
                    {t("demo.close")}
                  </Button>
                  <Select
                    size="sm"
                    w={180}
                    aria-label={t("demo.lang")}
                    value={lang}
                    onChange={(next) => {
                      const val = next ?? ""
                      setLang(val)
                      if (val) command("setLanguage", { lang: val })
                    }}
                    data={[
                      { value: "", label: t("demo.lang_auto") },
                      ...WIDGET_LANGUAGES.map((code) => ({ value: code, label: `${languageEndonym(code)} (${code})` })),
                    ]}
                  />
                  <Anchor href={standaloneHref} target="_blank" ml="auto" size="sm">
                    {t("demo.standalone")}
                  </Anchor>
                </Group>
                <Textarea
                  label={t("demo.user_token")}
                  description={t("demo.user_token_hint")}
                  value={userToken}
                  onChange={(e) => setUserToken(e.currentTarget.value.trim())}
                  autosize
                  minRows={2}
                />
                <Group justify="flex-end" gap="xs">
                  <Button variant="default" onClick={() => command("logout")}>
                    {t("demo.logout")}
                  </Button>
                  <Button onClick={() => command("login", { user_token: userToken })} disabled={!userToken}>
                    {t("demo.login")}
                  </Button>
                </Group>
              </Stack>
            </Paper>
          )}
          <Skeleton height={14} animate={false} />
          <Skeleton height={14} width="70%" animate={false} />
          <Skeleton height={180} animate={false} />
          <Skeleton height={14} width="50%" animate={false} />
          <Skeleton height={600} animate={false} />
        </Stack>
      </Container>
    </Box>
  )
}
