// 开发环境测试账号快捷登录按钮：仅在开发环境按需挂载，点击直接调用登录并跳转目标页面。

import { Button, type ButtonProps, Stack, Text } from "@mantine/core"
import { type ReactNode, useState } from "react"
import { useApp } from "../app/app"
import { notifyError } from "../components/feedback"
import { useText } from "../i18n/i18n"

/** DevSignIn 的选项。 */
export interface DevSignInProps {
  /** 调用登录接口，与 LoginForm 的 login 相同。 */
  login: (values: { email: string; password: string }) => Promise<unknown>
  /** seed 写入的测试账号邮箱。 */
  email: string
  /** 测试账号密码。 */
  password: string
  /** 登录成功后跳回的地址（通常来自 ?redirect=）。 */
  redirect?: string
  /** 可选按钮文案，默认 tenon.dev_sign_in.submit。 */
  label?: ReactNode
  /** 按钮上方的分隔内容（例如 Divider）。 */
  divider?: ReactNode
  /** 下方提示是否附带明文密码，默认 true。 */
  showPassword?: boolean
  /** 按钮尺寸。 */
  size?: ButtonProps["size"]
}

/** 开发环境单键填充并登录测试账号按钮。 */
export function DevSignIn({
  login,
  email,
  password,
  redirect,
  label,
  divider,
  showPassword = true,
  size,
}: DevSignInProps): ReactNode {
  const app = useApp()
  const t = useText()
  const [pending, setPending] = useState(false)

  const signIn = async () => {
    setPending(true)
    try {
      await login({ email, password })
      await app.session.signIn()
      await app.redirectAfterSignIn(redirect)
    } catch (err) {
      notifyError(err, t("tenon.dev_sign_in.failed"))
    } finally {
      setPending(false)
    }
  }

  return (
    <Stack gap="xs" mt="md">
      {divider}
      <Button variant="light" fullWidth size={size} loading={pending} onClick={() => void signIn()}>
        {label ?? t("tenon.dev_sign_in.submit")}
      </Button>
      <Text size="xs" c="dimmed" ta="center">
        {showPassword ? `${email} ／ ${password}` : email}
      </Text>
    </Stack>
  )
}
