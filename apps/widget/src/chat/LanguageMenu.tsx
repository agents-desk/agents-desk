import { ActionIcon, Menu } from "@mantine/core"
import { IconCheck, IconWorld } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { languageEndonym, normalizeLang } from "../i18n"
import { useBrandForeground } from "./contrast"

/** LanguageMenuProps 是聊天窗口顶栏访客语言切换器的参数。 */
interface LanguageMenuProps {
  value: string // 当前已选语言（requestedLanguage 或 uiLanguage）
  languages: readonly string[] // 渠道允许访客选择的语言列表
  onChange: (lang: string) => void
}

/**
 * LanguageMenu 是聊天窗口顶栏的极简访客语言切换器：
 * 仅在可选语言多于一种时渲染地球图标按钮，下拉菜单使用各语言原生自称（Endonym）以便任意母语访客一眼识别。
 */
export function LanguageMenu({ value, languages, onChange }: LanguageMenuProps): ReactNode {
  const t = useT()
  const fg = useBrandForeground()
  if (languages.length <= 1) return null
  const current = normalizeLang(value).toLowerCase()

  return (
    <Menu position="bottom-end" withinPortal shadow="md" width={164}>
      <Menu.Target>
        <ActionIcon variant="transparent" c={fg} size="md" aria-label={t("language.switch")}>
          <IconWorld size={18} />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown>
        {languages.map((lang) => {
          const active = normalizeLang(lang).toLowerCase() === current
          return (
            <Menu.Item
              key={lang}
              rightSection={active ? <IconCheck size={14} /> : null}
              fw={active ? 600 : 400}
              onClick={() => {
                if (!active) onChange(lang)
              }}
            >
              {languageEndonym(lang)}
            </Menu.Item>
          )
        })}
      </Menu.Dropdown>
    </Menu>
  )
}
