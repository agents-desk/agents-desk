// 修改密码表单：包含当前密码、新密码、确认密码前端校验、服务端字段错误绑定与成功提示。

import { Button, Group, PasswordInput, Stack } from "@mantine/core"
import { useForm } from "@mantine/form"
import { type ReactNode, useState } from "react"
import { confirm, notifySuccess } from "../components/feedback"
import { applyServerErrors, FormError } from "../components/form/form"
import { useUnsavedChanges } from "../components/form/rules"
import { useText } from "../i18n/i18n"

/** PasswordChangeForm 的选项。 */
export interface PasswordChangeFormProps {
  /** 调用修改密码接口。 */
  submit: (values: { current_password: string; new_password: string }) => Promise<unknown>
  /** 修改成功回调（默认已弹出成功提示并重置表单）。 */
  onSuccess?: () => void
  /** 提交按钮文案，默认 tenon.password.change。 */
  submitLabel?: string
}

/** 个人设置或账号安全页中的修改密码表单。 */
export function PasswordChangeForm({ submit, onSuccess, submitLabel }: PasswordChangeFormProps): ReactNode {
  const t = useText()
  const form = useForm({
    initialValues: { current_password: "", new_password: "", confirm: "" },
    validate: {
      confirm: (value, values) => (value === values.new_password ? null : t("tenon.password.mismatch")),
    },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()
  const unsaved = useUnsavedChanges(form.isDirty(), {
    ignoreSearch: true,
    confirm: () =>
      confirm({
        title: t("tenon.unsaved.discard_title"),
        message: t("tenon.unsaved.discard"),
        confirmLabel: t("tenon.unsaved.discard_confirm"),
        danger: true,
      }),
  })

  const handleSubmit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await submit({ current_password: values.current_password, new_password: values.new_password })
      unsaved.release()
      form.reset()
      notifySuccess(t("tenon.password.changed"))
      onSuccess?.()
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  return (
    <form noValidate onSubmit={handleSubmit}>
      <Stack gap="md">
        <FormError error={error} />
        <PasswordInput
          label={t("tenon.password.current")}
          required
          autoComplete="current-password"
          {...form.getInputProps("current_password")}
        />
        <PasswordInput
          label={t("tenon.password.new")}
          description={t("tenon.password.rule")}
          required
          autoComplete="new-password"
          {...form.getInputProps("new_password")}
        />
        <PasswordInput
          label={t("tenon.password.confirm")}
          required
          autoComplete="new-password"
          {...form.getInputProps("confirm")}
        />
        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty()} loading={saving}>
            {submitLabel ?? t("tenon.password.change")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
