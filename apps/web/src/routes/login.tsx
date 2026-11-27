import { Alert, Anchor, Box, Button, Group, PasswordInput, Stack, Text, TextInput, ThemeIcon, Title } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, redirect } from "@tanstack/react-router"
import {
  IconAlertCircle,
  IconArrowLeft,
  IconInbox,
  IconLanguage,
  IconLayersSubtract,
  IconLock,
  IconMail,
} from "@tabler/icons-react"
import { host } from "@go-tenon/host"
import {
  BrowserSignIn,
  ColorSchemeToggle,
  errorMessage,
  LanguageMenu,
  notifySuccess,
  safeRedirect,
  serverErrors,
  SplitAuthLayout,
  useApp,
  useFavicon,
  useLanguage,
  useT,
} from "@go-tenon/ui"
import { type FormEvent, useState } from "react"
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

function WebForgotPassword({ initialEmail, onBack }: { initialEmail: string; onBack: () => void }) {
  const t = useT()
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
      setErrors({ fields: { email: t("login.email_required") } })
      return
    }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalized)) {
      setErrors({ fields: { email: t("login.email_invalid") } })
      return
    }
    setPending(true)
    setErrors({ fields: {} })
    try {
      const res = await api.auth.passwordForgot({ email: normalized })
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
      await api.auth.passwordReset({ request_id: requestId, code: code.trim(), password })
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
            placeholder={t("login.email_placeholder")}
            type="email"
            leftSection={<IconMail size={16} stroke={1.6} />}
            required
            autoFocus
            value={email}
            onChange={(e) => setEmail(e.currentTarget.value)}
            error={errors.fields.email}
          />
          <Button type="submit" loading={pending} fullWidth size="md">
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
          leftSection={<IconLock size={16} stroke={1.6} />}
          required
          value={password}
          onChange={(e) => setPassword(e.currentTarget.value)}
          error={errors.fields.password}
        />
        <PasswordInput
          label={t("profile.confirm_password")}
          leftSection={<IconLock size={16} stroke={1.6} />}
          required
          value={confirmPassword}
          onChange={(e) => setConfirmPassword(e.currentTarget.value)}
          error={errors.fields.confirm}
        />
        <Button type="submit" loading={pending} fullWidth size="md">
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
  const { redirect: redirectTarget } = Route.useSearch()
  const app = useApp()
  const t = useT()
  const language = useLanguage()
  const site = useQuery(apiKit.queries.site.get()) // 平台名称与 Logo 来自系统设置，免登录读取
  const brandName = site.data?.site_name || t("app.web")
  useFavicon(site.data?.logo_url || undefined)
  const desktop = host()

  const [forgot, setForgot] = useState(false)
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [errors, setErrors] = useState<ReturnType<typeof serverErrors>>({ fields: {} })
  const [pending, setPending] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const normalizedEmail = email.trim().toLowerCase()
    const nextFields: Record<string, string> = {}
    if (!normalizedEmail) {
      nextFields.email = t("login.email_required")
    } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalizedEmail)) {
      nextFields.email = t("login.email_invalid")
    }
    if (!password) {
      nextFields.password = t("login.password_required")
    }
    if (Object.keys(nextFields).length > 0) {
      setErrors({ fields: nextFields })
      return
    }
    setPending(true)
    setErrors({ fields: {} })
    try {
      await login({ email: normalizedEmail, password })
      await app.session.signIn()
      await app.redirectAfterSignIn(redirectTarget)
    } catch (err) {
      setErrors(serverErrors(err))
    } finally {
      setPending(false)
    }
  }

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
          <WebForgotPassword initialEmail={email} onBack={() => setForgot(false)} />
        ) : (
          <>
            <form onSubmit={submit} noValidate>
              <Stack gap="md">
                {errors.form && (
                  <Alert color="red" variant="light" icon={<IconAlertCircle size={18} />} role="alert">
                    {errorMessage(errors.form)}
                  </Alert>
                )}
                <TextInput
                  label={t("login.email")}
                  placeholder={t("login.email_placeholder")}
                  type="email"
                  autoComplete="username"
                  leftSection={<IconMail size={16} stroke={1.6} />}
                  required
                  autoFocus
                  value={email}
                  onChange={(e) => {
                    setEmail(e.currentTarget.value)
                    if (errors.fields.email) setErrors((prev) => ({ ...prev, fields: { ...prev.fields, email: "" } }))
                  }}
                  error={errors.fields.email}
                />
                <Stack gap={6}>
                  <PasswordInput
                    label={t("login.password")}
                    placeholder={t("login.password_placeholder")}
                    autoComplete="current-password"
                    leftSection={<IconLock size={16} stroke={1.6} />}
                    required
                    value={password}
                    onChange={(e) => {
                      setPassword(e.currentTarget.value)
                      if (errors.fields.password) setErrors((prev) => ({ ...prev, fields: { ...prev.fields, password: "" } }))
                    }}
                    error={errors.fields.password}
                  />
                  <Group justify="flex-end">
                    <Anchor component="button" type="button" size="xs" c="dimmed" onClick={() => setForgot(true)}>
                      {t("login.forgot_password")}
                    </Anchor>
                  </Group>
                </Stack>
                <Button type="submit" loading={pending} fullWidth mt="xs" size="md">
                  {t("login.submit")}
                </Button>
              </Stack>
            </form>
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
