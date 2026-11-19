import { createFileRoute } from "@tanstack/react-router"
import { host } from "@go-tenon/host"
import { BrowserSignIn, CenteredAuthLayout, LoginForm } from "@go-tenon/ui"
import { login } from "../auth"

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
    <CenteredAuthLayout brand="agents-desk" title="登录" description="用工作区管理员给你的邮箱和密码登录">
      {desktop ? <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirect} /> : <LoginForm login={login} redirect={redirect} />}
    </CenteredAuthLayout>
  )
}
