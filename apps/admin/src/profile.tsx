import { Button, Group, Stack, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  applyServerErrors,
  AvatarField,
  FormError,
  notifySuccess,
  PasswordChangeForm,
  useT,
  useUnsavedChanges,
} from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { api } from "./api"
import { me } from "./auth"

// 个人资料：基本资料（头像、名字）和修改密码各自独立提交。

/** BasicProfileForm 读取当前平台管理员资料并渲染基本资料表单。 */
export function BasicProfileForm(): ReactNode {
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  if (!profile.data) return null
  return <BasicForm profile={profile.data} />
}

/** 头像和名字：选择图片后立即上传为临时文件，点“保存”后才换上。 */
function BasicForm({ profile }: { profile: { name: string; avatar_file_id?: string; avatar_url: string } }): ReactNode {
  const queryClient = useQueryClient()
  const t = useT()
  const form = useForm({ initialValues: { name: profile.name, avatar_file_id: profile.avatar_file_id ?? "" } })
  const [preview, setPreview] = useState(profile.avatar_url)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const dirty = form.isDirty() || preview !== profile.avatar_url
  const unsaved = useUnsavedChanges(dirty, { ignoreSearch: true })

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await api.auth.profileUpdate({ name: values.name, avatar_file_id: values.avatar_file_id || undefined })
      unsaved.release()
      await queryClient.invalidateQueries({ queryKey: ["me"] })
      form.resetDirty()
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />
        <AvatarField
          label={t("profile.avatar")}
          name={form.values.name}
          previewUrl={preview}
          onUpload={(file) => api.file.upload({ file })}
          onChange={({ id, url }) => {
            form.setFieldValue("avatar_file_id", id)
            form.clearFieldError("avatar_file_id")
            setPreview(url)
          }}
          hint={t("profile.avatar_hint")}
          error={form.errors.avatar_file_id}
        />
        <TextInput label={t("profile.name")} required maxLength={60} {...form.getInputProps("name")} />
        <Group justify="flex-end">
          <Button type="submit" loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}

/** ChangePasswordForm 渲染平台管理员修改密码表单，改完后使其他设备登录失效。 */
export function ChangePasswordForm(): ReactNode {
  return <PasswordChangeForm submit={api.auth.passwordChange} />
}
