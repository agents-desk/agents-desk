import { Alert, Anchor, Button, Group, PasswordInput, Stack, Text, TextInput } from "@mantine/core"
import { IconAlertCircle } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { host } from "@go-tenon/host"
import {
  BrowserSignIn,
  CenteredAuthLayout,
  errorMessage,
  LoginForm,
  notifySuccess,
  serverErrors,
  useFavicon,
  useT,
} from "@go-tenon/ui"
import { type FormEvent, useState } from "react"
import { api, kit } from "../api"
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

function AdminForgotPassword({ onBack }: { onBack: () => void }) {
  const t = useT()
  const [step, setStep] = useState<"email" | "reset">("email")
  const [email, setEmail] = useState("")
  const [requestId, setRequestId] = useState("")
  const [code, setCode] = useState("")
  const [password, setPassword] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [pending, setPending] = useState(false)
  const [errors, setErrors] = useState<ReturnType<typeof serverErrors>>({ fields: {} })

  const submitEmail = async (e: FormEvent) => {
    e.preventDefault()
    const normalized = email.trim().toLowerCase()
    if (!normalized || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalized)) {
      setErrors({ fields: { email: t("login.email_invalid") } })
      return
    }
    setPending(true)
    setErrors({ fields: {} })
    try {
      const res = await api.admin.passwordForgot({ email: normalized })
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
    if (!code.trim()) nextFields.code = t("login.reset_code_required")
    if (password.length < 8) nextFields.password = t("profile.new_password_hint")
    if (password !== confirmPassword) nextFields.confirm = t("profile.password_mismatch")
    if (Object.keys(nextFields).length > 0) {
      setErrors({ fields: nextFields })
      return
    }
    setPending(true)
    setErrors({ fields: {} })
    try {
      await api.admin.passwordReset({ request_id: requestId, code: code.trim(), password })
      notifySuccess(t("login.reset_success"))
      onBack()
    } catch (err) {
      setErrors(serverErrors(err))
    } finally {
      setPending(false)
    }
  }

  if (step === "email") {
    return (
      <form onSubmit={submitEmail} noValidate>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("login.forgot_description")}
          </Text>
          {errors.form && (
            <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
              {errorMessage(errors.form)}
            </Alert>
          )}
          <TextInput
            label={t("login.email")}
            type="email"
            required
            autoFocus
            value={email}
            onChange={(e) => setEmail(e.currentTarget.value)}
            error={errors.fields.email}
          />
          <Button type="submit" loading={pending} fullWidth>
            {t("login.send_reset_code")}
          </Button>
          <Anchor component="button" type="button" size="sm" c="dimmed" onClick={onBack}>
            {t("login.back_to_login")}
          </Anchor>
        </Stack>
      </form>
    )
  }

  return (
    <form onSubmit={submitReset} noValidate>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("login.reset_code_sent", { email: email.trim().toLowerCase() })}
        </Text>
        {errors.form && (
          <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
            {errorMessage(errors.form)}
          </Alert>
        )}
        <TextInput
          label={t("login.reset_code")}
          required
          autoFocus
          value={code}
          onChange={(e) => setCode(e.currentTarget.value)}
          error={errors.fields.code}
        />
        <PasswordInput
          label={t("profile.new_password")}
          required
          value={password}
          onChange={(e) => setPassword(e.currentTarget.value)}
          error={errors.fields.password}
        />
        <PasswordInput
          label={t("profile.confirm_password")}
          required
          value={confirmPassword}
          onChange={(e) => setConfirmPassword(e.currentTarget.value)}
          error={errors.fields.confirm}
        />
        <Button type="submit" loading={pending} fullWidth>
          {t("login.reset_submit")}
        </Button>
        <Group justify="space-between">
          <Anchor component="button" type="button" size="sm" c="dimmed" onClick={() => setStep("email")}>
            {t("login.resend_code")}
          </Anchor>
          <Anchor component="button" type="button" size="sm" c="dimmed" onClick={onBack}>
            {t("login.back_to_login")}
          </Anchor>
        </Group>
      </Stack>
    </form>
  )
}

function Login() {
  const { redirect } = Route.useSearch()
  const t = useT()
  const site = useQuery(kit.queries.site.get()) // 平台名称与 Logo 来自系统设置，免登录读取
  useFavicon(site.data?.logo_url || undefined)
  const [forgot, setForgot] = useState(false)
  // 在桌面端（Electron 等宿主）中，用户在系统浏览器中登录；在网页中用邮箱和密码登录。
  const desktop = host()
  return (
    <CenteredAuthLayout
      logo={brandLogo(site.data?.logo_url, site.data?.site_name, 32)}
      brand={site.data?.site_name ?? ""}
      title={forgot ? t("login.forgot_title") : t("login.title")}
      description={forgot ? undefined : t("login.description")}
    >
      {desktop ? (
        <BrowserSignIn signIn={desktop.auth.signIn} redirect={redirect} />
      ) : forgot ? (
        <AdminForgotPassword onBack={() => setForgot(false)} />
      ) : (
        <>
          <LoginForm login={login} redirect={redirect} />
          <Group justify="flex-end" mt="xs">
            <Anchor component="button" type="button" size="xs" c="dimmed" onClick={() => setForgot(true)}>
              {t("login.forgot_password")}
            </Anchor>
          </Group>
          {/* 只在开发环境中：构建时整段连同测试账号一起去掉 */}
          {import.meta.env.DEV && (
            <TestAccountSignIn login={login} email="admin@example.com" password="password" redirect={redirect} />
          )}
        </>
      )}
    </CenteredAuthLayout>
  )
}
