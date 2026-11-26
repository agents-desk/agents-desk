import { ActionIcon, Box, Group, Text, Title } from "@mantine/core"
import { IconChevronDown, IconChevronLeft, IconX } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { useBrandForeground } from "./contrast"

/** HeaderProps 是聊天窗口顶部的内容。 */
interface HeaderProps {
  color: string // 主题色
  title: string // 聊天插件的名称或子页面标题
  greeting?: string // 首次进入时的欢迎问候语
  status?: string // 对话标题旁的当前服务状态
  onBack?: () => void // 子页面返回按钮回调
  extra?: ReactNode // 标题栏右侧的附加按钮（如我的工单入口）
  close?: "collapse" | "close" // 右上角的按钮：电脑上收起，手机全屏和 App 中关闭
  onClose?: () => void
}

/** Header 是聊天窗口的顶部：主题色的底，首次进入展示名称和问候语，对话及子页面展示标题、状态和返回操作。顶部留出刘海的安全区域。 */
export function Header({ color, title, greeting, status, onBack, extra, close, onClose }: HeaderProps): ReactNode {
  const t = useT()
  const fg = useBrandForeground()
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
          {extra}
          {closeButton}
        </Group>
      ) : (
        <Box px="lg" pt="lg" pb={34} pos="relative">
          <Text fw={600} size="sm">
            {title}
          </Text>
          <Group gap={4} pos="absolute" top={10} right={8} wrap="nowrap">
            {extra}
            {closeButton}
          </Group>
          <Title order={2} size="h3" mt="sm" c={fg}>
            {greeting}
          </Title>
        </Box>
      )}
    </Box>
  )
}
