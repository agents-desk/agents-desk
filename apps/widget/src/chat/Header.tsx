import { ActionIcon, Box, Group, Text, Title } from "@mantine/core"
import { IconChevronDown, IconChevronLeft, IconX } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { onBrandColor } from "./contrast"

/** HeaderProps 是聊天窗口顶部的内容。 */
interface HeaderProps {
  color: string // 主题色
  title: string // 聊天插件的名称
  greeting?: string // 首页的问候语；会话页没有
  status?: string // 会话页标题旁的状态
  onBack?: () => void // 会话页的返回首页
  close?: "collapse" | "close" // 右上角的按钮：电脑上收起，手机全屏和 App 中关闭；没有时不显示
  onClose?: () => void
}

/** Header 是聊天窗口的顶部：主题色的底，首页是名称和问候语，会话页是返回、名称和会话的状态。顶部留出刘海的安全区域。 */
export function Header({ color, title, greeting, status, onBack, close, onClose }: HeaderProps): ReactNode {
  const t = useT()
  const fg = onBrandColor(color)
  const closeButton = close && (
    <ActionIcon variant="transparent" c={fg} size="lg" onClick={onClose} aria-label={t(close === "collapse" ? "panel.collapse" : "panel.close")}>
      {close === "collapse" ? <IconChevronDown size={20} /> : <IconX size={20} />}
    </ActionIcon>
  )
  return (
    <Box bg={`var(--mantine-color-${color}-filled)`} c={fg} className="w-safe-top" style={{ flex: "none" }}>
      {greeting === undefined ? (
        <Group gap={4} px={6} py={8} wrap="nowrap">
          {onBack && (
            <ActionIcon variant="transparent" c={fg} size="lg" onClick={onBack} aria-label={t("panel.back")}>
              <IconChevronLeft size={22} />
            </ActionIcon>
          )}
          <Text fw={600} size="sm" style={{ flex: 1, minWidth: 0 }} truncate>
            {title}
            {status && (
              <Text span size="xs" fw={400} opacity={0.85}>
                {" · "}
                {status}
              </Text>
            )}
          </Text>
          {closeButton}
        </Group>
      ) : (
        <Box px="lg" pt="lg" pb={34} pos="relative">
          <Text fw={600} size="sm">
            {title}
          </Text>
          <Box pos="absolute" top={10} right={8}>
            {closeButton}
          </Box>
          <Title order={2} size="h3" mt="sm" c={fg}>
            {greeting}
          </Title>
        </Box>
      )}
    </Box>
  )
}
