import { type ContactSummary, isApiError, type WidgetBusinessIntakeConfig } from "@agents-desk/client/widget"
import { Box, Button, Center, Paper, PasswordInput, Stack, Text, Textarea, TextInput, ThemeIcon } from "@mantine/core"
import { IconClockOff, IconLock } from "@tabler/icons-react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { describeError } from "@go-tenon/client"
import { useT } from "@go-tenon/ui"
import { type FormEvent, type ReactNode, useState } from "react"
import { authed, widgetApi } from "../api"
import { identity } from "../identity"

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

/** needsIntake 判断当前联系人是否需要先完成商务链接的开场留资表单。 */
export function needsIntake(contact: ContactSummary | null, intake: WidgetBusinessIntakeConfig | undefined, skipped: boolean, hasMessages: boolean): boolean {
  if (!intake?.enabled || !contact || hasMessages) return false
  const hasRequired =
    (intake.name === "required" && !contact.name.trim()) ||
    (intake.company === "required" && !(contact.company ?? "").trim()) ||
    (intake.email === "required" && !contact.email.trim()) ||
    intake.note === "required"
  if (hasRequired) return true
  if (skipped) return false
  const hasAnyField = intake.name !== "off" || intake.company !== "off" || intake.email !== "off" || intake.note !== "off"
  const alreadyProvided = Boolean(contact.name.trim() || (contact.company ?? "").trim() || contact.email.trim())
  return hasAnyField && !alreadyProvided
}

/** ExpiredView 在网页商务链接已超过有效期时向新访客展示清晰提示。 */
export function ExpiredView(): ReactNode {
  const t = useT()
  return (
    <Center style={{ flex: 1 }} p="xl">
      <Stack gap="sm" align="center" maw={300}>
        <ThemeIcon size={48} radius="xl" variant="light" color="gray">
          <IconClockOff size={24} />
        </ThemeIcon>
        <Text size="md" fw={600} ta="center">
          {t("business.expired_title")}
        </Text>
        <Text size="sm" c="dimmed" ta="center">
          {t("business.expired_desc")}
        </Text>
      </Stack>
    </Center>
  )
}

/** PasscodeGate 是受口令保护的商务沟通链接入口：校验通过后才建立访客会话。 */
export function PasscodeGate({
  color,
  preview,
  onUnlock,
}: {
  color: string
  preview?: boolean
  onUnlock: (passcode: string) => Promise<void>
}): ReactNode {
  const t = useT()
  const [code, setCode] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    const trimmed = code.trim()
    if (!trimmed) {
      setError(t("business.intake_required"))
      return
    }
    if (preview) {
      setError(null)
      await onUnlock(trimmed)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await onUnlock(trimmed)
    } catch (err) {
      if (isApiError(err) && err.code === "widget.invalid_passcode") {
        setError(t("business.passcode_invalid"))
      } else {
        setError(describeError(err).message ?? t("business.passcode_invalid"))
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Center style={{ flex: 1 }} p="lg">
      <Paper withBorder radius="lg" p="lg" w="100%" maw={340}>
        <form noValidate onSubmit={(e) => void handleSubmit(e)}>
          <Stack gap="sm">
            <Center>
              <ThemeIcon size={44} radius="xl" variant="light" color={color}>
                <IconLock size={20} />
              </ThemeIcon>
            </Center>
            <Text size="sm" fw={600} ta="center">
              {t("business.passcode_title")}
            </Text>
            <Text size="xs" c="dimmed" ta="center">
              {t("business.passcode_desc")}
            </Text>
            <PasswordInput
              value={code}
              onChange={(e) => {
                setCode(e.currentTarget.value)
                if (error) setError(null)
              }}
              placeholder={t("business.passcode_placeholder")}
              error={error ?? undefined}
              autoFocus={!preview}
            />
            <Button type="submit" color={color} loading={busy} fullWidth>
              {t("business.passcode_submit")}
            </Button>
          </Stack>
        </form>
      </Paper>
    </Center>
  )
}

/** IntakeForm 是网页商务链接的开场留资表单，按字段配置动态展示必填/选填项。 */
export function IntakeForm({
  color,
  intake,
  contact,
  preview,
  onDone,
  onSkip,
}: {
  color: string
  intake: WidgetBusinessIntakeConfig
  contact?: ContactSummary | null
  preview?: boolean
  onDone: () => void
  onSkip: () => void
}): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [name, setName] = useState(contact?.name ?? "")
  const [company, setCompany] = useState(contact?.company ?? "")
  const [email, setEmail] = useState(contact?.email ?? "")
  const [note, setNote] = useState("")
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [submitError, setSubmitError] = useState<string | null>(null)

  const canSkip =
    intake.name !== "required" &&
    intake.company !== "required" &&
    intake.email !== "required" &&
    intake.note !== "required"

  const submit = useMutation({
    mutationFn: () =>
      authed(() =>
        widgetApi.contact.intake({
          name: name.trim() || undefined,
          company: company.trim() || undefined,
          email: email.trim() || undefined,
          note: note.trim() || undefined,
        }),
      ),
    onSuccess: (updated) => {
      identity.updateContact(updated)
      void queryClient.invalidateQueries({ queryKey: ["widget", contact?.id] })
      onDone()
    },
    onError: (err) => {
      if (isApiError(err)) {
        if (err.code === "widget.intake_required") {
          setSubmitError(t("business.intake_required"))
          return
        }
        if (err.code === "contact.invalid_email") {
          setSubmitError(t("business.intake_invalid_email"))
          return
        }
      }
      setSubmitError(describeError(err).message ?? t("chat.failed"))
    },
  })

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault()
    const nextErr: Record<string, string> = {}
    if (intake.name === "required" && !name.trim()) nextErr.name = t("business.intake_required")
    if (intake.company === "required" && !company.trim()) nextErr.company = t("business.intake_required")
    if (intake.email === "required" && !email.trim()) nextErr.email = t("business.intake_required")
    if (email.trim() && !EMAIL_RE.test(email.trim())) nextErr.email = t("business.intake_invalid_email")
    if (intake.note === "required" && !note.trim()) nextErr.note = t("business.intake_required")
    setErrors(nextErr)
    if (Object.keys(nextErr).length > 0) return
    if (preview) {
      onDone()
      return
    }
    setSubmitError(null)
    submit.mutate()
  }

  return (
    <Box className="w-scroll" p="md">
      <Paper withBorder radius="lg" p="md" maw={420} mx="auto">
        <form noValidate onSubmit={handleSubmit}>
          <Stack gap="sm">
            <Stack gap={2}>
              <Text size="sm" fw={600}>
                {t("business.intake_title")}
              </Text>
              <Text size="xs" c="dimmed">
                {t("business.intake_desc")}
              </Text>
            </Stack>

            {intake.name !== "off" && (
              <TextInput
                size="sm"
                label={t("business.intake_name")}
                placeholder={t("business.intake_name_placeholder")}
                required={intake.name === "required"}
                value={name}
                onChange={(e) => setName(e.currentTarget.value)}
                error={errors.name}
              />
            )}
            {intake.company !== "off" && (
              <TextInput
                size="sm"
                label={t("business.intake_company")}
                placeholder={t("business.intake_company_placeholder")}
                required={intake.company === "required"}
                value={company}
                onChange={(e) => setCompany(e.currentTarget.value)}
                error={errors.company}
              />
            )}
            {intake.email !== "off" && (
              <TextInput
                size="sm"
                type="email"
                label={t("business.intake_email")}
                placeholder={t("business.intake_email_placeholder")}
                required={intake.email === "required"}
                value={email}
                onChange={(e) => setEmail(e.currentTarget.value)}
                error={errors.email}
              />
            )}
            {intake.note !== "off" && (
              <Textarea
                size="sm"
                label={t("business.intake_note")}
                placeholder={t("business.intake_note_placeholder")}
                required={intake.note === "required"}
                autosize
                minRows={2}
                maxRows={3}
                value={note}
                onChange={(e) => setNote(e.currentTarget.value)}
                error={errors.note}
              />
            )}

            {submitError && (
              <Text size="xs" c="red">
                {submitError}
              </Text>
            )}

            <Button type="submit" color={color} loading={submit.isPending} fullWidth mt={4}>
              {t("business.intake_submit")}
            </Button>
            {canSkip && (
              <Button variant="subtle" color="gray" size="xs" onClick={onSkip} fullWidth>
                {t("business.intake_skip")}
              </Button>
            )}
          </Stack>
        </form>
      </Paper>
    </Box>
  )
}
