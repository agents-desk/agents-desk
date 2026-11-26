import type { ChannelSummary } from "@agents-desk/client/tenant"
import { Badge, Group, Text, Tooltip } from "@mantine/core"
import { IconBrandTelegram, IconFlask, IconMessageCircle, IconWorld } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

/** ChannelIcon 根据渠道类型返回统一的轻量图标。 */
export function ChannelIcon({ kind, size = 12 }: { kind: ChannelSummary["kind"] | string; size?: number }): ReactNode {
  switch (kind) {
    case "widget":
      return <IconWorld size={size} stroke={1.8} style={{ flex: "none" }} />
    case "telegram":
      return <IconBrandTelegram size={size} stroke={1.8} style={{ flex: "none" }} />
    case "test":
      return <IconFlask size={size} stroke={1.8} style={{ flex: "none" }} />
    default:
      return <IconMessageCircle size={size} stroke={1.8} style={{ flex: "none" }} />
  }
}

/** channelColor 返回渠道类型的语义主题色名。 */
export function channelColor(kind: ChannelSummary["kind"] | string): string {
  switch (kind) {
    case "telegram":
      return "bright-blue"
    case "widget":
      return "brand"
    default:
      return "gray"
  }
}

/** ChannelChip 在时间线消息头部以紧凑胶囊展示该消息来自或发往的渠道。 */
export function ChannelChip({ channel }: { channel: ChannelSummary }): ReactNode {
  return (
    <Badge
      size="xs"
      variant="light"
      color={channelColor(channel.kind)}
      radius="sm"
      leftSection={<ChannelIcon kind={channel.kind} size={10} />}
      style={{ flex: "none", textTransform: "none", fontWeight: 500 }}
    >
      {channel.name}
    </Badge>
  )
}

/** MultiChannelBadge 在会话列表行中提示该会话跨多个渠道合流，悬停显示全部参与渠道名。 */
export function MultiChannelBadge({ channels }: { channels: ChannelSummary[] }): ReactNode {
  const t = useT()
  if (channels.length <= 1) return null
  return (
    <Tooltip label={t("inbox.channel_multi_tip", { channels: channels.map((ch) => ch.name).join(" · ") })} openDelay={250}>
      <Group
        gap={3}
        wrap="nowrap"
        px={5}
        py={1}
        style={{
          flex: "none",
          borderRadius: "var(--mantine-radius-sm)",
          backgroundColor: "var(--mantine-color-bright-blue-light)",
          color: "var(--mantine-color-bright-blue-light-color)",
        }}
      >
        {channels.slice(0, 3).map((ch) => (
          <ChannelIcon key={ch.id} kind={ch.kind} size={11} />
        ))}
        {channels.length > 3 && (
          <Text span size="xs" fw={600} lh={1}>
            +{channels.length - 3}
          </Text>
        )}
      </Group>
    </Tooltip>
  )
}
