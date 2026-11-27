import { Badge, Box, Button, Group, Paper, SegmentedControl, Stack, Switch, Text, ThemeIcon, Title } from "@mantine/core"
import {
  IconBook,
  IconFileText,
  IconMessageChatbot,
  IconPencil,
  IconRobot,
  IconSparkles,
  IconTicket,
  IconTool,
} from "@tabler/icons-react"
import { useNavigate, useParams } from "@tanstack/react-router"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

/** DetailsAsideTab 是会话、工单与聊天右侧栏的页签：详情或 AI 助手。 */
export type DetailsAsideTab = "details" | "ai"

/** DetailsAsideTabs 渲染右侧详情栏顶部的「详情 | AI 助手」切换条。 */
export function DetailsAsideTabs({
  value,
  onChange,
}: {
  value: DetailsAsideTab
  onChange: (next: DetailsAsideTab) => void
}): ReactNode {
  const t = useT()
  return (
    <Box px="var(--tn-pane-px)" py={8} style={{ borderBottom: "1px solid var(--tn-border)" }}>
      <SegmentedControl
        fullWidth
        size="xs"
        value={value}
        onChange={(v) => onChange(v === "ai" ? "ai" : "details")}
        data={[
          { value: "details", label: t("ai_placeholder.tab_details") },
          {
            value: "ai",
            label: (
              <Group gap={4} justify="center" wrap="nowrap">
                <IconSparkles size={13} />
                <span>{t("ai_placeholder.tab_assistant")}</span>
              </Group>
            ),
          },
        ]}
      />
    </Box>
  )
}

/** AiReceptionCard 在各渠道与外部群配置页展示「AI Agent 优先接待 + 人工团队兜底」的预留配置区块。 */
export function AiReceptionCard({ scene }: { scene: "customer" | "partner" | "internal" | "group" }): ReactNode {
  const t = useT()
  const { slug } = useParams({ from: "/$slug" })
  const navigate = useNavigate()

  const descKey =
    scene === "internal"
      ? "ai_placeholder.reception_desc_internal"
      : scene === "partner"
        ? "ai_placeholder.reception_desc_partner"
        : scene === "group"
          ? "ai_placeholder.reception_desc_group"
          : "ai_placeholder.reception_desc_customer"

  return (
    <Paper withBorder p="md" radius="md" style={{ borderStyle: "dashed" }}>
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Group gap="xs" wrap="nowrap">
            <ThemeIcon size={28} radius="md" variant="light" color="brand">
              <IconRobot size={16} />
            </ThemeIcon>
            <Box>
              <Group gap={6} wrap="nowrap">
                <Title order={5}>{t("ai_placeholder.reception_title")}</Title>
                <Badge size="xs" variant="light" color="brand">
                  {t("ai_placeholder.coming_soon")}
                </Badge>
              </Group>
              <Text size="xs" c="dimmed" mt={2}>
                {t(descKey)}
              </Text>
            </Box>
          </Group>
          <Button
            size="compact-xs"
            variant="subtle"
            onClick={() => void navigate({ to: "/$slug/ai", params: { slug } })}
          >
            {t("ai_placeholder.view_roadmap")}
          </Button>
        </Group>

        <Switch
          disabled
          checked={false}
          label={t("ai_placeholder.reception_switch_label")}
          description={t("ai_placeholder.reception_switch_hint")}
        />
      </Stack>
    </Paper>
  )
}

/** AiAssistantPanel 是收件箱、工单与聊天右侧栏的「AI 助手」占位视图。 */
export function AiAssistantPanel({ scene }: { scene: "inbox" | "ticket" | "chat" }): ReactNode {
  const t = useT()
  const { slug } = useParams({ from: "/$slug" })
  const navigate = useNavigate()

  const capabilities =
    scene === "ticket"
      ? [
          { icon: <IconFileText size={15} />, title: t("ai_placeholder.cap_ticket_summary"), desc: t("ai_placeholder.cap_ticket_summary_desc") },
          { icon: <IconBook size={15} />, title: t("ai_placeholder.cap_kb_search"), desc: t("ai_placeholder.cap_kb_search_desc") },
          { icon: <IconPencil size={15} />, title: t("ai_placeholder.cap_ticket_update"), desc: t("ai_placeholder.cap_ticket_update_desc") },
        ]
      : scene === "chat"
        ? [
            { icon: <IconFileText size={15} />, title: t("ai_placeholder.cap_chat_summary"), desc: t("ai_placeholder.cap_chat_summary_desc") },
            { icon: <IconPencil size={15} />, title: t("ai_placeholder.cap_draft_reply"), desc: t("ai_placeholder.cap_draft_reply_desc") },
            { icon: <IconBook size={15} />, title: t("ai_placeholder.cap_kb_search"), desc: t("ai_placeholder.cap_kb_search_desc") },
          ]
        : [
            { icon: <IconFileText size={15} />, title: t("ai_placeholder.cap_conv_summary"), desc: t("ai_placeholder.cap_conv_summary_desc") },
            { icon: <IconBook size={15} />, title: t("ai_placeholder.cap_kb_search"), desc: t("ai_placeholder.cap_kb_search_desc") },
            { icon: <IconPencil size={15} />, title: t("ai_placeholder.cap_draft_reply"), desc: t("ai_placeholder.cap_draft_reply_desc") },
            { icon: <IconTicket size={15} />, title: t("ai_placeholder.cap_fill_ticket"), desc: t("ai_placeholder.cap_fill_ticket_desc") },
          ]

  return (
    <Stack gap="sm" px="var(--tn-pane-px)" py="sm">
      <Paper withBorder p="sm" radius="md" style={{ borderStyle: "dashed" }}>
        <Group gap="xs" wrap="nowrap" align="flex-start">
          <ThemeIcon size={30} radius="md" variant="light" color="brand" style={{ flex: "none" }}>
            <IconSparkles size={16} />
          </ThemeIcon>
          <Stack gap={4} style={{ minWidth: 0 }}>
            <Group gap={6} wrap="nowrap">
              <Text size="sm" fw={600}>
                {t("ai_placeholder.assistant_title")}
              </Text>
              <Badge size="xs" variant="light" color="brand">
                {t("ai_placeholder.coming_soon")}
              </Badge>
            </Group>
            <Text size="xs" c="dimmed" lh={1.5}>
              {t("ai_placeholder.assistant_desc")}
            </Text>
          </Stack>
        </Group>
      </Paper>

      <Stack gap="xs">
        {capabilities.map((cap) => (
          <Paper key={cap.title} withBorder p="xs" radius="md">
            <Group gap="xs" wrap="nowrap" align="flex-start">
              <ThemeIcon size={24} radius="sm" variant="light" color="gray" style={{ flex: "none", marginTop: 2 }}>
                {cap.icon}
              </ThemeIcon>
              <Box style={{ flex: 1, minWidth: 0 }}>
                <Text size="xs" fw={600}>
                  {cap.title}
                </Text>
                <Text size="xs" c="dimmed" mt={2} lh={1.45}>
                  {cap.desc}
                </Text>
              </Box>
            </Group>
          </Paper>
        ))}
      </Stack>

      <Paper withBorder p="xs" radius="md" bg="var(--tn-canvas)">
        <Group gap="xs" wrap="nowrap">
          <IconTool size={14} style={{ flex: "none", color: "var(--tn-muted)" }} />
          <Text size="xs" c="dimmed" style={{ flex: 1 }}>
            {t("ai_placeholder.assistant_private_hint")}
          </Text>
        </Group>
      </Paper>

      <Button
        variant="light"
        size="xs"
        leftSection={<IconMessageChatbot size={14} />}
        onClick={() => void navigate({ to: "/$slug/ai", params: { slug } })}
      >
        {t("ai_placeholder.open_ai_hub")}
      </Button>
    </Stack>
  )
}
