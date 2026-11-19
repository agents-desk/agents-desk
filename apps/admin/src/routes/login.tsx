import { createFileRoute } from "@tanstack/react-router"
import { host } from "@go-tenon/host"
import { BrowserSignIn, CenteredAuthLayout, LoginForm } from "@go-tenon/ui"
import { login } from "../auth"
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
  // 在桌面端（Electron 等宿主）中，用户在系统浏览器中登录；在网页中用邮箱和密码登录。
  const desktop = host()
  return (
    <CenteredAuthLayout brand="agents-desk" title="管理员登录" description="平台管理员使用">
      {desktop ? (
        <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirect} />
      ) : (
        <>
          <LoginForm login={login} redirect={redirect} />
          {/* 只在开发环境中：构建时整段连同测试账号一起去掉 */}
          {import.meta.env.DEV && (
            <TestAccountSignIn login={login} email="admin@example.com" password="password" redirect={redirect} />
          )}
        </>
      )}
    </CenteredAuthLayout>
  )
}
