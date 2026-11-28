import { Button, Group, Stack, Textarea, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { applyServerErrors, AvatarField, FormError, notifySuccess, PasswordChangeForm, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { api } from "./api"
import { me } from "./auth"
import { useUnsavedGuard } from "./components/unsaved-guard"

// 个人资料表单组件：基本资料（头像、名字、职务、简介）与修改密码各自独立提交，嵌入个人设置页的标签中。

/** BasicProfileForm 是基本资料表单入口，读取当前账号资料后渲染表单。 */
export function BasicProfileForm(): ReactNode {
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  if (!profile.data) return null
  return <BasicForm profile={profile.data} />
}

/** 头像、名字与商务名片：选择图片后立即上传为临时文件，点“保存”后才换上。 */
function BasicForm({ profile }: { profile: { name: string; title?: string; bio?: string; avatar_file_id?: string; avatar_url: string } }): ReactNode {
  const queryClient = useQueryClient()
  const t = useT()
  const form = useForm({
    initialValues: {
      name: profile.name,
      title: profile.title ?? "",
      bio: profile.bio ?? "",
      avatar_file_id: profile.avatar_file_id ?? "",
    },
  })
  const [preview, setPreview] = useState(profile.avatar_url)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const dirty = form.isDirty() || preview !== profile.avatar_url
  const unsaved = useUnsavedGuard(dirty)

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await api.auth.profileUpdate({
        name: values.name,
        title: values.title,
        bio: values.bio,
        avatar_file_id: values.avatar_file_id || undefined,
      })
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
        <TextInput
          label={t("profile.title")}
          placeholder={t("profile.title_placeholder")}
          description={t("profile.title_hint")}
          maxLength={60}
          {...form.getInputProps("title")}
        />
        <Textarea
          label={t("profile.bio")}
          placeholder={t("profile.bio_placeholder")}
          description={t("profile.bio_hint")}
          autosize
          minRows={2}
          maxRows={4}
          maxLength={240}
          {...form.getInputProps("bio")}
        />
        <Group justify="flex-end">
          <Button type="submit" disabled={!dirty} loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}

/** PasswordForm 是修改密码表单：改完后其他设备上的登录失效，当前登录保留。 */
export function PasswordForm(): ReactNode {
  const t = useT()
  return <PasswordChangeForm submit={api.auth.passwordChange} submitLabel={t("profile.change_password")} />
}
