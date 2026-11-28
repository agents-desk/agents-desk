// 登录表单（LoginForm）与两步邮箱验证码重置密码表单（ForgotPasswordForm）。
// 放在 CenteredAuthLayout、SplitAuthLayout 等认证页布局中，字段错误显示在输入框下，全局错误显示在表单顶部。

import { Alert, Anchor, Button, Group, PasswordInput, Stack, Text, TextInput } from "@mantine/core"
import { IconAlertCircle, IconLock, IconMail } from "@tabler/icons-react"
import { type FormEvent, type ReactNode, useState } from "react"
import { useApp } from "../app/app"
import { errorMessage, notifySuccess } from "../components/feedback"
import { serverErrors } from "../components/form/rules"
import { useText } from "../i18n/i18n"

/** LoginForm 的选项。 */
export interface LoginFormProps {
  /** 调用应用的登录接口：服务端写入会话 cookie。成功后用会话的 load 读取身份。 */
  login: (values: { email: string; password: string }) => Promise<unknown>
  /** 登录前想去的页面（登录页的 ?redirect=），登录后回到这里。 */
  redirect?: string
  /** “忘记密码”链接，例如 <AnchorLink to="/forgot">忘记密码？</AnchorLink>。 */
  forgot?: ReactNode
  /** 自定义邮箱输入框标签，默认 tenon.login.email。 */
  emailLabel?: string
  /** 邮箱输入框占位文字。 */
  emailPlaceholder?: string
  /** 自定义提交按钮文案，默认 tenon.login.submit。 */
  submitLabel?: string
  /** 提交按钮尺寸，默认使用 Mantine 默认尺寸。 */
  submitSize?: "xs" | "sm" | "md" | "lg"
  /** 是否在邮箱与密码输入框左侧显示图标（邮箱与锁图标）。 */
  leftIcons?: boolean
}

/** 邮箱和密码登录：字段错误显示在输入框下面，其他错误（例如密码不对）显示在表单上方。 */
export function LoginForm({
  login,
  redirect,
  forgot,
  emailLabel,
  emailPlaceholder,
  submitLabel,
  submitSize,
  leftIcons = false,
}: LoginFormProps): ReactNode {
  const app = useApp()
  const t = useText()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [errors, setErrors] = useState<ReturnType<typeof serverErrors>>({ fields: {} })
  const [pending, setPending] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setPending(true)
    setErrors({ fields: {} })
    try {
      await login({ email, password })
      await app.session.signIn()
      await app.redirectAfterSignIn(redirect)
    } catch (err) {
      setErrors(serverErrors(err))
    } finally {
      setPending(false)
    }
  }
  return (
    <form onSubmit={submit} noValidate>
      <Stack gap="md">
        {errors.form && (
          <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
            {errorMessage(errors.form)}
          </Alert>
        )}
        <TextInput
          label={emailLabel ?? t("tenon.login.email")}
          placeholder={emailPlaceholder}
          type="email"
          autoComplete="username"
          leftSection={leftIcons ? <IconMail size={16} stroke={1.6} /> : undefined}
          required
          autoFocus
          value={email}
          onChange={(e) => {
            setEmail(e.currentTarget.value)
            if (errors.fields.email) setErrors((prev) => ({ ...prev, fields: { ...prev.fields, email: "" } }))
          }}
          error={errors.fields.email}
        />
        <PasswordInput
          label={t("tenon.login.password")}
          autoComplete="current-password"
          leftSection={leftIcons ? <IconLock size={16} stroke={1.6} /> : undefined}
          required
          value={password}
          onChange={(e) => {
            setPassword(e.currentTarget.value)
            if (errors.fields.password) setErrors((prev) => ({ ...prev, fields: { ...prev.fields, password: "" } }))
          }}
          error={errors.fields.password}
        />
        {forgot && (
          <Text size="sm" ta="right" mt={-8}>
            {forgot}
          </Text>
        )}
        <Button type="submit" loading={pending} fullWidth size={submitSize} mt="xs">
          {submitLabel ?? t("tenon.login.submit")}
        </Button>
      </Stack>
    </form>
  )
}

/** ForgotPasswordForm 的选项。 */
export interface ForgotPasswordFormProps {
  /** 第一步：向邮箱发送重置密码验证码，返回用于校验的 request_id。 */
  sendCode: (values: { email: string }) => Promise<{ request_id: string }>
  /** 第二步：提交 request_id、验证码与新密码完成重置。 */
  reset: (values: { request_id: string; code: string; password: string }) => Promise<unknown>
  /** 重置成功后触发（默认已弹出成功提示）；未传时回退调用 onBack。 */
  onSuccess?: () => void
  /** 点击“返回登录”回调。 */
  onBack?: () => void
  /** 预设邮箱地址。 */
  initialEmail?: string
  /** 邮箱输入框占位文字。 */
  emailPlaceholder?: string
  /** 提交按钮尺寸。 */
  submitSize?: "xs" | "sm" | "md" | "lg"
  /** 是否在输入框左侧显示图标。 */
  leftIcons?: boolean
}

/** 两步忘记密码表单：先发送邮箱验证码，再输入验证码与新密码完成重置。 */
export function ForgotPasswordForm({
  sendCode,
  reset,
  onSuccess,
  onBack,
  initialEmail = "",
  emailPlaceholder,
  submitSize,
  leftIcons = true,
}: ForgotPasswordFormProps): ReactNode {
  const t = useText()
  const [step, setStep] = useState<"email" | "reset">("email")
  const [email, setEmail] = useState(initialEmail)
  const [requestId, setRequestId] = useState("")
  const [code, setCode] = useState("")
  const [password, setPassword] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [pending, setPending] = useState(false)
  const [errors, setErrors] = useState<ReturnType<typeof serverErrors>>({ fields: {} })

  const submitEmail = async (e: FormEvent) => {
    e.preventDefault()
    const normalized = email.trim().toLowerCase()
    if (!normalized) {
      setErrors({ fields: { email: t("tenon.forgot_password.email_required") } })
      return
    }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalized)) {
      setErrors({ fields: { email: t("tenon.forgot_password.email_invalid") } })
      return
    }
    setPending(true)
    setErrors({ fields: {} })
    try {
      const res = await sendCode({ email: normalized })
      setRequestId(res.request_id)
      setStep("reset")
    } catch (err) {
      setErrors(serverErrors(err))
    } finally {
      setPending(false)
    }
  }

  const submitReset = async (e: FormEvent) => {
    e.preventDefault()
    const nextFields: Record<string, string> = {}
    if (!code.trim()) nextFields.code = t("tenon.forgot_password.code_required")
    if (password.length < 8) nextFields.password = t("tenon.password.rule")
    if (password !== confirmPassword) nextFields.confirm = t("tenon.password.mismatch")
    if (Object.keys(nextFields).length > 0) {
      setErrors({ fields: nextFields })
      return
    }
    setPending(true)
    setErrors({ fields: {} })
    try {
      await reset({ request_id: requestId, code: code.trim(), password })
      notifySuccess(t("tenon.forgot_password.success"))
      if (onSuccess) {
        onSuccess()
      } else {
        onBack?.()
      }
    } catch (err) {
      setErrors(serverErrors(err))
    } finally {
      setPending(false)
    }
  }

  const clearFieldError = (field: string) => {
    setErrors((prev) => {
      if (!prev.fields[field]) return prev
      const next = { ...prev.fields }
      delete next[field]
      return { ...prev, fields: next }
    })
  }

  if (step === "email") {
    return (
      <form onSubmit={submitEmail} noValidate>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("tenon.forgot_password.description")}
          </Text>
          {errors.form && (
            <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
              {errorMessage(errors.form)}
            </Alert>
          )}
          <TextInput
            label={t("tenon.forgot_password.email")}
            placeholder={emailPlaceholder}
            type="email"
            autoComplete="email"
            leftSection={leftIcons ? <IconMail size={16} stroke={1.6} /> : undefined}
            required
            autoFocus
            value={email}
            onChange={(e) => {
              setEmail(e.currentTarget.value)
              clearFieldError("email")
            }}
            error={errors.fields.email}
          />
          <Button type="submit" loading={pending} fullWidth size={submitSize}>
            {t("tenon.forgot_password.submit_email")}
          </Button>
          {onBack && (
            <Group justify="center">
              <Anchor component="button" type="button" size="sm" c="dimmed" onClick={onBack}>
                {t("tenon.forgot_password.back")}
              </Anchor>
            </Group>
          )}
        </Stack>
      </form>
    )
  }

  return (
    <form onSubmit={submitReset} noValidate>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("tenon.forgot_password.sent", { email: email.trim().toLowerCase() })}
        </Text>
        {errors.form && (
          <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
            {errorMessage(errors.form)}
          </Alert>
        )}
        <TextInput
          label={t("tenon.forgot_password.code")}
          autoComplete="one-time-code"
          required
          autoFocus
          value={code}
          onChange={(e) => {
            setCode(e.currentTarget.value)
            clearFieldError("code")
          }}
          error={errors.fields.code}
        />
        <PasswordInput
          label={t("tenon.password.new")}
          description={t("tenon.password.rule")}
          autoComplete="new-password"
          leftSection={leftIcons ? <IconLock size={16} stroke={1.6} /> : undefined}
          required
          value={password}
          onChange={(e) => {
            setPassword(e.currentTarget.value)
            clearFieldError("password")
          }}
          error={errors.fields.password}
        />
        <PasswordInput
          label={t("tenon.password.confirm")}
          autoComplete="new-password"
          leftSection={leftIcons ? <IconLock size={16} stroke={1.6} /> : undefined}
          required
          value={confirmPassword}
          onChange={(e) => {
            setConfirmPassword(e.currentTarget.value)
            clearFieldError("confirm")
          }}
          error={errors.fields.confirm}
        />
        <Button type="submit" loading={pending} fullWidth size={submitSize}>
          {t("tenon.forgot_password.submit_reset")}
        </Button>
        <Group justify="space-between">
          <Anchor
            component="button"
            type="button"
            size="sm"
            c="dimmed"
            onClick={() => {
              setErrors({ fields: {} })
              setStep("email")
            }}
          >
            {t("tenon.forgot_password.resend")}
          </Anchor>
          {onBack && (
            <Anchor component="button" type="button" size="sm" c="dimmed" onClick={onBack}>
              {t("tenon.forgot_password.back")}
            </Anchor>
          )}
        </Group>
      </Stack>
    </form>
  )
}
