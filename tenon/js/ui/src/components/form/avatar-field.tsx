// 头像上传字段：展示头像预览、选择上传图片与可选移除按钮，表单只提交返回的文件 ID。

import { Avatar, Button, FileButton, Group, type MantineColor, type MantineRadius, Stack, Text } from "@mantine/core"
import { type ReactNode, useState } from "react"
import { useText } from "../../i18n/i18n"
import { notifyError } from "../feedback"

/** 上传头像文件后返回的结果。 */
export interface AvatarUploadResult {
  id: string
  url: string
}

/** AvatarField 的选项。 */
export interface AvatarFieldProps {
  /** 字段标题，可选。 */
  label?: ReactNode
  /** 姓名或名称，用于未上传头像时取首字符作兜底头像。 */
  name?: string
  /** 当前展示的头像图片地址。 */
  previewUrl?: string | null
  /** 执行文件上传，返回文件 ID 与可预览地址。 */
  onUpload: (file: File) => Promise<AvatarUploadResult>
  /** 选择并上传成功、或点击移除时调用。 */
  onChange: (result: { id: string; url: string }) => void
  /** 允许的 MIME 类型，默认 "image/png,image/jpeg,image/webp"。 */
  accept?: string
  /** 说明文字（无错误时显示）。 */
  hint?: ReactNode
  /** 校验或服务端错误提示。 */
  error?: ReactNode
  /** 头像尺寸，默认 72。 */
  size?: number
  /** 头像圆角，默认 "xl"。 */
  radius?: MantineRadius
  /** 无头像图时首字符占位底色，默认 "brand"。 */
  color?: MantineColor
}

/** 头像上传与预览字段。 */
export function AvatarField({
  label,
  name = "",
  previewUrl,
  onUpload,
  onChange,
  accept = "image/png,image/jpeg,image/webp",
  hint,
  error,
  size = 72,
  radius = "xl",
  color = "brand",
}: AvatarFieldProps): ReactNode {
  const t = useText()
  const [uploading, setUploading] = useState(false)

  const handleFile = async (file: File | null) => {
    if (!file) return
    setUploading(true)
    try {
      const uploaded = await onUpload(file)
      onChange({ id: uploaded.id, url: uploaded.url })
    } catch (err) {
      notifyError(err)
    } finally {
      setUploading(false)
    }
  }

  return (
    <Stack gap={6}>
      {label && (
        <Text size="sm" fw={500}>
          {label}
        </Text>
      )}
      <Group gap="md">
        <Avatar src={previewUrl || null} size={size} radius={radius} color={color}>
          {name.slice(0, 1)}
        </Avatar>
        <Stack gap={6}>
          <Group gap="xs">
            <FileButton onChange={(f) => void handleFile(f)} accept={accept}>
              {(props) => (
                <Button {...props} variant="default" size="xs" loading={uploading}>
                  {previewUrl ? t("tenon.avatar.choose") : t("tenon.avatar.upload")}
                </Button>
              )}
            </FileButton>
            {previewUrl && (
              <Button variant="subtle" color="gray" size="xs" onClick={() => onChange({ id: "", url: "" })}>
                {t("tenon.avatar.clear")}
              </Button>
            )}
          </Group>
          {(error || hint) && (
            <Text size="xs" c={error ? "red" : "dimmed"}>
              {error ?? hint}
            </Text>
          )}
        </Stack>
      </Group>
    </Stack>
  )
}
