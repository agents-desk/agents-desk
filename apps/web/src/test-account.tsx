import { Button, Divider, Group, Stack, Text } from "@mantine/core"
import { notifyError, useApp, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"

/** TestAccountOption 描述一个开发环境快捷登录账号。 */
export interface TestAccountOption {
  label: string
  email: string
  password: string
}

/** TestAccountSignIn 的选项。 */
export interface TestAccountSignInProps {
  /** 调用登录接口，与登录表单相同。 */
  login: (values: { email: string; password: string }) => Promise<unknown>
  /** seed 写入的测试账号列表，支持按角色一键登录。 */
  accounts: TestAccountOption[]
  redirect?: string
}

/**
 * 开发环境多角色快捷登录条：点击对应角色立即用 seed 账号登录并回跳。
 * 仅在 import.meta.env.DEV 分支中渲染，生产构建时连同测试账号一起移除。
 */
export function TestAccountSignIn({ login, accounts, redirect }: TestAccountSignInProps): ReactNode {
  const app = useApp()
  const t = useT()
  const [pendingEmail, setPendingEmail] = useState<string | null>(null)

  const signIn = async (account: TestAccountOption) => {
    setPendingEmail(account.email)
    try {
      await login({ email: account.email, password: account.password })
      await app.session.signIn()
      await app.redirectAfterSignIn(redirect)
    } catch (err) {
      notifyError(err, t("test_account.failed"))
    } finally {
      setPendingEmail(null)
    }
  }

  return (
    <Stack gap="xs" mt="lg">
      <Divider label={t("test_account.dev_heading")} labelPosition="center" />
      <Group grow gap="xs">
        {accounts.map((acc) => (
          <Button
            key={acc.email}
            size="xs"
            variant="light"
            loading={pendingEmail === acc.email}
            disabled={pendingEmail !== null && pendingEmail !== acc.email}
            onClick={() => void signIn(acc)}
          >
            {acc.label}
          </Button>
        ))}
      </Group>
      <Text size="xs" c="dimmed" ta="center">
        {accounts.map((acc) => acc.email).join(" · ")}
      </Text>
    </Stack>
  )
}
