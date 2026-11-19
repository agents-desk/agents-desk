import { Button, Stack, Text } from "@mantine/core"
import { notifyError, useApp } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"

/** TestAccountSignIn 的选项。 */
export interface TestAccountSignInProps {
  /** 调用登录接口，与 LoginForm 的 login 相同。 */
  login: (values: { email: string; password: string }) => Promise<unknown>
  email: string // seed 命令写入的测试账号
  password: string
  redirect?: string // 登录前想去的页面，登录后回到这里
}

/**
 * 开发环境中“用测试账号登录”的按钮：点一下用 seed 写入的账号登录，回到登录前想去的页面；下面用小字写着账号和密码。
 * 只在开发环境中使用：调用处写 {import.meta.env.DEV && <TestAccountSignIn … />}，构建时连同账号一起去掉。
 */
export function TestAccountSignIn({ login, email, password, redirect }: TestAccountSignInProps): ReactNode {
  const app = useApp()
  const [pending, setPending] = useState(false)
  const signIn = async () => {
    setPending(true)
    try {
      await login({ email, password })
      await app.session.signIn()
      await app.redirectAfterSignIn(redirect)
    } catch (err) {
      notifyError(err, "测试账号登录失败，先运行 go run ./cmd/agents-desk seed")
    } finally {
      setPending(false)
    }
  }
  return (
    <Stack gap={4} mt="md">
      <Button variant="light" fullWidth loading={pending} onClick={() => void signIn()}>
        用测试账号登录
      </Button>
      <Text size="xs" c="dimmed" ta="center">
        {email} ／ {password}
      </Text>
    </Stack>
  )
}
