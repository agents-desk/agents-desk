import { Anchor, Box, Group, Stack, Text, ThemeIcon, Title } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, redirect } from "@tanstack/react-router"
import { IconArrowLeft, IconInbox, IconLanguage, IconLayersSubtract } from "@tabler/icons-react"
import { host } from "@go-tenon/host"
import {
  BrowserSignIn,
  ColorSchemeToggle,
  ForgotPasswordForm,
  LanguageMenu,
  LoginForm,
  safeRedirect,
  SplitAuthLayout,
  useFavicon,
  useLanguage,
  useT,
} from "@go-tenon/ui"
import { useState } from "react"
import { api, apiKit } from "../api"
import { login } from "../auth"
import { brandLogo } from "../brand-logo"
import { i18n } from "../i18n"
import { TestAccountSignIn } from "../test-account"
import { UI_LANGUAGES } from "../workspace"

// 工作台登录页 /app/login：
// 已登录访问时自动按 ?redirect= 或默认工作台入口重定向，不再重复展示登录表单。
export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  beforeLoad: async ({ context, search }) => {
    await context.app.ready()
    if (context.app.signedIn()) {
      const target = safeRedirect(search.redirect, "/")
      throw redirect({ href: target.startsWith(context.app.loginPath) ? "/" : target })
    }
  },
  component: Login,
})

function Login() {
  const { redirect: redirectTarget } = Route.useSearch()
  const t = useT()
  const language = useLanguage()
  const site = useQuery(apiKit.queries.site.get()) // 平台名称与 Logo 来自系统设置，免登录读取
  const brandName = site.data?.site_name || t("app.web")
  useFavicon(site.data?.logo_url || undefined)
  const desktop = host()

  const [forgot, setForgot] = useState(false)

  const aside = (
    <Stack gap="xl">
      <Stack gap="sm">
        <Title order={1} className="desk-login-aside-title">
          {t("login.aside_headline")}
        </Title>
        <Text size="sm" className="desk-login-aside-sub">
          {t("login.aside_subline")}
        </Text>
      </Stack>

      <Stack gap="sm">
        {[
          { icon: IconInbox, title: t("login.feature_inbox_title"), desc: t("login.feature_inbox_desc") },
          { icon: IconLanguage, title: t("login.feature_ai_title"), desc: t("login.feature_ai_desc") },
          { icon: IconLayersSubtract, title: t("login.feature_workspace_title"), desc: t("login.feature_workspace_desc") },
        ].map((item) => (
          <Group key={item.title} align="flex-start" gap="sm" wrap="nowrap" className="desk-login-feature">
            <ThemeIcon size={34} radius="md" className="desk-login-feature-icon">
              <item.icon size={18} stroke={1.7} />
            </ThemeIcon>
            <Box style={{ minWidth: 0 }}>
              <Text size="sm" fw={600} className="desk-login-feature-title">
                {item.title}
              </Text>
              <Text size="xs" className="desk-login-feature-desc">
                {item.desc}
              </Text>
            </Box>
          </Group>
        ))}
      </Stack>
    </Stack>
  )

  return (
    <div className="desk-login-page">
      <div className="desk-login-topbar">
        <Anchor href="/" size="sm" c="dimmed" className="desk-auth-back">
          <Group gap={4} wrap="nowrap">
            <IconArrowLeft size={15} stroke={1.8} />
            <span>{t("login.back_home")}</span>
          </Group>
        </Anchor>
        <Group gap="xs" wrap="nowrap">
          <LanguageMenu languages={UI_LANGUAGES} value={language} onChange={(v) => i18n.setLanguage(v)} />
          <ColorSchemeToggle />
        </Group>
      </div>

      <SplitAuthLayout
        logo={brandLogo(site.data?.logo_url, brandName, 28)}
        brand={brandName}
        aside={aside}
        asideFooter={`${brandName} · ${t("login.aside_footer")}`}
        title={forgot ? t("login.forgot_title") : t("login.title")}
        description={forgot ? undefined : t("login.description")}
        footer={t("login.footer_hint")}
      >
        {desktop ? (
          <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirectTarget} />
        ) : forgot ? (
          <ForgotPasswordForm
            sendCode={api.auth.passwordForgot}
            reset={api.auth.passwordReset}
            emailPlaceholder={t("login.email_placeholder")}
            submitSize="md"
            onBack={() => setForgot(false)}
          />
        ) : (
          <>
            <LoginForm
              login={(values) => login({ email: values.email.trim().toLowerCase(), password: values.password })}
              redirect={redirectTarget}
              emailPlaceholder={t("login.email_placeholder")}
              submitSize="md"
              leftIcons
              forgot={
                <Anchor component="button" type="button" size="xs" c="dimmed" onClick={() => setForgot(true)}>
                  {t("login.forgot_password")}
                </Anchor>
              }
            />
            {/* 仅在开发环境提供多角色快捷登录，生产构建自动移除 */}
            {import.meta.env.DEV && (
              <TestAccountSignIn
                login={login}
                redirect={redirectTarget}
                accounts={[
                  { label: t("test_account.role_owner"), email: "owner@example.com", password: "password" },
                  { label: t("test_account.role_manager"), email: "manager@example.com", password: "password" },
                  { label: t("test_account.role_agent"), email: "agent@example.com", password: "password" },
                ]}
              />
            )}
          </>
        )}
      </SplitAuthLayout>
    </div>
  )
}
