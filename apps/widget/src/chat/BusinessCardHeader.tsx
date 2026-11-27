import type { WidgetPublicOwnerCard } from "@agents-desk/client/widget"
import { ActionIcon, Avatar, Box, Group, Indicator, Stack, Text, Title } from "@mantine/core"
import { IconClock, IconX } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { useBrandForeground } from "./contrast"

/** BusinessCardHeaderProps 是商务沟通链接专属顶部名片头的参数。 */
interface BusinessCardHeaderProps {
  color: string
  title: string
  owner?: WidgetPublicOwnerCard
  greeting?: string
  awayMessage?: string
  compact?: boolean
  close?: "collapse" | "close"
  onClose?: () => void
}

/**
 * BusinessCardHeader 是网页商务沟通链接的顶部名片头：
 * 展示归属成员头像、在线/离开状态圆点、姓名、头衔、公司、简介与问候语，
 * 当成员处于离开状态且配置了离开提示时在名片下方呈现温和的响应预期条。
 */
export function BusinessCardHeader({
  color,
  title,
  owner,
  greeting,
  awayMessage,
  compact,
  close,
  onClose,
}: BusinessCardHeaderProps): ReactNode {
  const t = useT()
  const fg = useBrandForeground()
  const online = owner?.status === "online"
  const subtitle = [owner?.title, owner?.company].filter(Boolean).join(" · ")

  return (
    <Box style={{ flex: "none" }}>
      <Box bg={`var(--mantine-color-${color}-filled)`} c={fg} className="w-safe-top" px={compact ? "md" : "lg"} py={compact ? "sm" : "md"} pos="relative">
        {close && onClose && (
          <ActionIcon
            variant="transparent"
            c={fg}
            size="md"
            pos="absolute"
            top={8}
            right={8}
            onClick={onClose}
            aria-label={t("panel.close")}
          >
            <IconX size={18} />
          </ActionIcon>
        )}

        {owner ? (
          <Stack gap={compact ? 4 : "xs"}>
            <Group gap="sm" wrap="nowrap" align="center" pr={close ? 28 : 0}>
              <Indicator size={10} offset={3} position="bottom-end" color={online ? "green" : "gray"} withBorder>
                <Avatar size={compact ? 36 : 46} radius="xl" src={owner.avatar_url || undefined} color="dark" variant="filled">
                  {owner.name.slice(0, 1)}
                </Avatar>
              </Indicator>
              <Box style={{ flex: 1, minWidth: 0 }}>
                <Group gap={6} wrap="nowrap">
                  <Text fw={600} size={compact ? "sm" : "md"} truncate>
                    {owner.name}
                  </Text>
                  <Text size="xs" opacity={0.85} style={{ flex: "none" }}>
                    · {t(online ? "business.status_online" : "business.status_away")}
                  </Text>
                </Group>
                {subtitle && (
                  <Text size="xs" opacity={0.88} truncate>
                    {subtitle}
                  </Text>
                )}
              </Box>
            </Group>
            {!compact && owner.bio && (
              <Text size="xs" opacity={0.9} lh={1.5}>
                {owner.bio}
              </Text>
            )}
            {!compact && greeting && (
              <Title order={3} size="h4" c={fg} mt={2} fw={600}>
                {greeting}
              </Title>
            )}
          </Stack>
        ) : (
          <Stack gap={4} pr={close ? 28 : 0}>
            <Text fw={600} size="sm">
              {title}
            </Text>
            {!compact && greeting && (
              <Title order={2} size="h3" c={fg} mt={4}>
                {greeting}
              </Title>
            )}
          </Stack>
        )}
      </Box>

      {owner && !online && awayMessage && (
        <Group
          gap="xs"
          px="md"
          py={8}
          wrap="nowrap"
          bg="var(--mantine-color-yellow-light)"
          style={{ borderBottom: "1px solid var(--mantine-color-yellow-3)" }}
        >
          <IconClock size={14} color="var(--mantine-color-yellow-7)" style={{ flex: "none" }} />
          <Text size="xs" lh={1.4}>
            {awayMessage}
          </Text>
        </Group>
      )}
    </Box>
  )
}
