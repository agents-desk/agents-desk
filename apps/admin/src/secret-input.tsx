import { ActionIcon, TextInput, type TextInputProps } from "@mantine/core"
import { IconEye, IconEyeOff } from "@tabler/icons-react"
import { useState } from "react"

// mask 遮住密钥的中间部分：保留开头 3 位和末尾 4 位，例如 sk-••••••••abcd；太短的密钥全部遮住。
export function mask(value: string): string {
  if (value.length <= 8) return "•".repeat(value.length)
  return `${value.slice(0, 3)}${"•".repeat(8)}${value.slice(-4)}`
}

// SecretInputProps 是密钥输入框的属性：与 TextInput 相同，value 和 onChange 是密钥本身。
type SecretInputProps = Omit<TextInputProps, "value" | "onChange"> & {
  value?: string
  onChange?: (event: React.ChangeEvent<HTMLInputElement>) => void
}

// SecretInput 是密钥的输入框：平时只显示首尾、中间用圆点遮住，不能编辑；点眼睛图标显示完整的密钥并可以修改。
// 还没有填写密钥时直接可以输入。value 和 onChange 与 form.getInputProps 的结果相同。
export function SecretInput({ value = "", onChange, ...props }: SecretInputProps) {
  const [shown, setShown] = useState(false)
  const editable = shown || value === ""
  return (
    <TextInput
      {...props}
      value={editable ? value : mask(value)}
      onChange={editable ? onChange : undefined}
      readOnly={!editable}
      autoComplete="off"
      spellCheck={false}
      rightSection={
        value !== "" && (
          <ActionIcon variant="subtle" color="gray" aria-label={shown ? "隐藏密钥" : "查看和修改密钥"} onClick={() => setShown((s) => !s)}>
            {shown ? <IconEyeOff size={16} stroke={1.6} /> : <IconEye size={16} stroke={1.6} />}
          </ActionIcon>
        )
      }
    />
  )
}
