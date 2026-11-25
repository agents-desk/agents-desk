import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { host } from "@go-tenon/host"
import { BrowserSignIn, CenteredAuthLayout, LoginForm, useFavicon, useT } from "@go-tenon/ui"
import { apiKit } from "../api"
import { login } from "../auth"
import { brandLogo } from "../brand-logo"
import { TestAccountSignIn } from "../test-account"

export const Route = createFileRoute("/login")({
  // ?redirect= 是登录前想去的页面
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  component: Login,
})

function Login() {
  const { redirect } = Route.useSearch()
  const t = useT()
  const site = useQuery(apiKit.queries.site.get()) // 平台名称与 Logo 来自系统设置，免登录读取
  useFavicon(site.data?.logo_url || undefined)
  // 在桌面端（Electron 等宿主）中，用户在系统浏览器中登录；在网页中用邮箱和密码登录。
  const desktop = host()
  return (
    <CenteredAuthLayout
      logo={brandLogo(site.data?.logo_url, site.data?.site_name, 32)}
      brand={site.data?.site_name ?? ""}
      title={t("login.title")}
      description={t("login.description")}
    >
      {desktop ? (
        <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirect} />
      ) : (
        <>
          <LoginForm login={login} redirect={redirect} />
          {/* 只在开发环境中：构建时整段连同测试账号一起去掉 */}
          {import.meta.env.DEV && (
            <TestAccountSignIn login={login} email="owner@example.com" password="password" redirect={redirect} />
          )}
        </>
      )}
    </CenteredAuthLayout>
  )
}
