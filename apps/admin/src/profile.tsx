import { Avatar, Button, FileButton, Group, PasswordInput, Stack, Tabs, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { applyServerErrors, FormError, notifyError, notifySuccess } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { api } from "./api"
import { me } from "./auth"

// 个人资料：基本资料（头像、名字）和修改密码两个标签页，各自提交。按表单的约定：宽度不限，一个输入框一行，保存按钮在右侧。

/** 个人资料的两个标签页。 */
export function ProfileTabs(): ReactNode {
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  if (!profile.data) return null
  return (
    <Tabs defaultValue="basic" keepMounted={false}>
      <Tabs.List>
        <Tabs.Tab value="basic">基本资料</Tabs.Tab>
        <Tabs.Tab value="password">修改密码</Tabs.Tab>
      </Tabs.List>
      <Tabs.Panel value="basic" pt="md">
        <BasicForm profile={profile.data} />
      </Tabs.Panel>
      <Tabs.Panel value="password" pt="md">
        <PasswordForm />
      </Tabs.Panel>
    </Tabs>
  )
}

/** 头像和名字：选择图片后立即上传为临时文件，点“保存”后才换上。 */
function BasicForm({ profile }: { profile: { name: string; avatar_file_id?: string; avatar_url: string } }): ReactNode {
  const queryClient = useQueryClient()
  const form = useForm({ initialValues: { name: profile.name, avatar_file_id: profile.avatar_file_id ?? "" } })
  const [preview, setPreview] = useState(profile.avatar_url)
  const [uploading, setUploading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const choose = async (file: File | null) => {
    if (!file) return
    setUploading(true)
    try {
      const uploaded = await api.file.upload({ file })
      form.setFieldValue("avatar_file_id", uploaded.id)
      form.clearFieldError("avatar_file_id")
      setPreview(uploaded.url)
    } catch (err) {
      notifyError(err, "上传失败")
    } finally {
      setUploading(false)
    }
  }
  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await api.auth.profileUpdate({ name: values.name, avatar_file_id: values.avatar_file_id || undefined })
      await queryClient.invalidateQueries({ queryKey: ["me"] })
      form.resetDirty()
      notifySuccess("已保存")
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
        <Stack gap={6}>
          <Text size="sm" fw={500}>
            头像
          </Text>
          <Group gap="md">
            <Avatar src={preview || null} size={72} radius="xl">
              {form.values.name.slice(0, 1)}
            </Avatar>
            <Stack gap={6}>
              <Group gap="xs">
                <FileButton onChange={(f) => void choose(f)} accept="image/png,image/jpeg,image/webp">
                  {(props) => (
                    <Button {...props} variant="default" size="xs" loading={uploading}>
                      上传图片
                    </Button>
                  )}
                </FileButton>
                {preview && (
                  <Button
                    variant="subtle"
                    color="gray"
                    size="xs"
                    onClick={() => {
                      form.setFieldValue("avatar_file_id", "")
                      setPreview("")
                    }}
                  >
                    移除
                  </Button>
                )}
              </Group>
              <Text size="xs" c={form.errors.avatar_file_id ? "red" : "dimmed"}>
                {form.errors.avatar_file_id ?? "PNG、JPEG 或 WebP，不超过 2 MB"}
              </Text>
            </Stack>
          </Group>
        </Stack>
        <TextInput label="名字" required maxLength={60} {...form.getInputProps("name")} />
        <Group justify="flex-end">
          <Button type="submit" loading={saving}>
            保存
          </Button>
        </Group>
      </Stack>
    </form>
  )
}

/** 修改密码：改完后其他设备上的登录失效，这次登录保留。 */
function PasswordForm(): ReactNode {
  const form = useForm({
    initialValues: { current_password: "", new_password: "", confirm: "" },
    validate: { confirm: (value, values) => (value === values.new_password ? null : "两次输入的新密码不一致") },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()
  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await api.auth.passwordChange({ current_password: values.current_password, new_password: values.new_password })
      form.reset()
      notifySuccess("密码已修改，其他设备上的登录已退出")
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
        <PasswordInput label="当前密码" required autoComplete="current-password" {...form.getInputProps("current_password")} />
        <PasswordInput
          label="新密码"
          description="8 到 128 个字符"
          required
          autoComplete="new-password"
          {...form.getInputProps("new_password")}
        />
        <PasswordInput label="再输入一次新密码" required autoComplete="new-password" {...form.getInputProps("confirm")} />
        <Group justify="flex-end">
          <Button type="submit" loading={saving}>
            修改密码
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
