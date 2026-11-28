import { Anchor, Box, Divider, Group } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, redirect } from "@tanstack/react-router"
import { host } from "@go-tenon/host"
import {
  BrowserSignIn,
  CenteredAuthLayout,
  ColorSchemeToggle,
  DevSignIn,
  ForgotPasswordForm,
  LanguageMenu,
  type LanguageOption,
  LoginForm,
  safeRedirect,
  useFavicon,
  useLanguage,
  useT,
} from "@go-tenon/ui"
import { useState } from "react"
import { api, kit } from "../api"
import { login } from "../auth"
import { brandLogo } from "../brand-logo"
import { i18n } from "../i18n"

const ADMIN_LANGUAGES: LanguageOption[] = [
  { value: "zh-CN", label: "简体中文" }, // i18n-ignore
  { value: "en", label: "English" },
]

export const Route = createFileRoute("/login")({
  // ?redirect= 是登录前想去的页面
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
  const site = useQuery(kit.queries.site.get()) // 平台名称与 Logo 来自系统设置，免登录读取
  useFavicon(site.data?.logo_url || undefined)
  const [forgot, setForgot] = useState(false)
  // 在桌面端（Electron 等宿主）中，用户在系统浏览器中登录；在网页中用邮箱和密码登录。
  const desktop = host()
  return (
    <Box pos="relative">
      <Group pos="fixed" top={14} right={20} gap="xs" wrap="nowrap" style={{ zIndex: 20 }}>
        <LanguageMenu languages={ADMIN_LANGUAGES} value={language} onChange={(v) => i18n.setLanguage(v)} />
        <ColorSchemeToggle />
      </Group>

      <CenteredAuthLayout
        logo={brandLogo(site.data?.logo_url, site.data?.site_name, 32)}
        brand={site.data?.site_name ?? ""}
        title={forgot ? t("login.forgot_title") : t("login.title")}
        description={forgot ? undefined : t("login.description")}
      >
        {desktop ? (
          <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirectTarget} />
        ) : forgot ? (
          <ForgotPasswordForm
            sendCode={api.admin.passwordForgot}
            reset={api.admin.passwordReset}
            submitSize="md"
            onBack={() => setForgot(false)}
          />
        ) : (
          <>
            <LoginForm
              login={login}
              redirect={redirectTarget}
              submitSize="md"
              forgot={
                <Anchor component="button" type="button" size="xs" c="dimmed" onClick={() => setForgot(true)}>
                  {t("login.forgot_password")}
                </Anchor>
              }
            />
            {/* 只在开发环境中：构建时整段连同测试账号一起去掉 */}
            {import.meta.env.DEV && (
              <DevSignIn
                login={login}
                email="admin@example.com"
                password="password"
                redirect={redirectTarget}
                divider={<Divider my="xs" />}
              />
            )}
          </>
        )}
      </CenteredAuthLayout>
    </Box>
  )
}
