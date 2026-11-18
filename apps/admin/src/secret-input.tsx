import { ActionIcon, TextInput, type TextInputProps } from "@mantine/core"
import { IconEye, IconEyeOff } from "@tabler/icons-react"
import { useState } from "react"

// mask 遮住密钥的中间部分：保留开头 3 位和末尾 4 位，中间的每个字符换成一个圆点，长度与原文相同，
// 例如 sk-1234567890abcd 显示为 sk-••••••••••abcd；不超过 8 位的密钥全部换成圆点。
export function mask(value: string): string {
  if (value.length <= 8) return "•".repeat(value.length)
  return value.slice(0, 3) + "•".repeat(value.length - 7) + value.slice(-4)
}

// latinFont 是密钥使用的西文字体：主题的字体栈把中文字体排在 Roboto、Arial 之前，在没有苹果和微软系统字体的电脑上
// 圆点会落到中文字体的全角字形上，显得又宽又疏。密钥里没有中文，只用西文字体，圆点紧凑排列。
const latinFont = '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Liberation Sans", sans-serif'

// SecretInputProps 是密钥输入框的属性：与 TextInput 相同，value 和 onChange 是密钥本身。
type SecretInputProps = Omit<TextInputProps, "value" | "onChange"> & {
  value?: string
  onChange?: (event: React.ChangeEvent<HTMLInputElement>) => void
}

// SecretInput 是密钥的输入框：平时只显示首尾、中间的字符换成圆点（西文字体，圆点紧凑排列），不能编辑；点眼睛图标显示完整的密钥并可以修改。
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
      styles={{ input: { fontFamily: latinFont } }}
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
