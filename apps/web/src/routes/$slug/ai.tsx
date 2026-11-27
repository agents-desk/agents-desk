import { Alert, Badge, Box, Button, Group, Paper, SimpleGrid, Stack, Text, ThemeIcon, Title } from "@mantine/core"
import {
  IconArrowRight,
  IconBook,
  IconBriefcase,
  IconChartBar,
  IconCheck,
  IconHeadset,
  IconRobot,
  IconShieldCheck,
  IconSparkles,
  IconTool,
  IconUsersGroup,
} from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { Page, useCan, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { tenantApi } from "../../api"

// AI Agent 模块占位页：展示面向对外客服、内部员工服务与商务沟通的 AI Agent 规划、知识库、工具接入与评测分析。
export const Route = createFileRoute("/$slug/ai")({
  staticData: { title: "nav.ai", icon: "ai", order: 4, permission: "conversations:read" },
  component: AiHubPage,
})

function AiHubPage(): ReactNode {
  const { slug } = Route.useParams()
  const t = useT()
  const can = useCan()
  const navigate = useNavigate()

  const canChannels = can("channels:manage")
  const canBusiness = can("business-channels:manage")
  const widgets = useQuery({ queryKey: ["resource", "widget", "list"], queryFn: () => tenantApi.widget.list(), enabled: canChannels })
  const tgCustomer = useQuery({
    queryKey: ["resource", "telegram", "list", "customer"],
    queryFn: () => tenantApi.telegram.list({ audience: "customer" }),
    enabled: canChannels,
  })
  const tgInternal = useQuery({
    queryKey: ["resource", "telegram", "list", "internal"],
    queryFn: () => tenantApi.telegram.list({ audience: "internal" }),
    enabled: canChannels,
  })
  const wecom = useQuery({ queryKey: ["resource", "wecom", "list"], queryFn: () => tenantApi.wecom.list(), enabled: canChannels })

  const customerChannelCount = (widgets.data?.items.length ?? 0) + (tgCustomer.data?.items.length ?? 0)
  const internalChannelCount = (wecom.data?.items.length ?? 0) + (tgInternal.data?.items.length ?? 0)

  const agents = [
    {
      key: "customer",
      icon: <IconHeadset size={20} />,
      color: "bright-blue",
      title: t("ai_hub.profile_customer_title"),
      desc: t("ai_hub.profile_customer_desc"),
      channelsLabel: canChannels ? t("ai_hub.connected_channels", { n: customerChannelCount }) : t("ai_hub.profile_customer_channels"),
      fallback: t("ai_hub.profile_customer_fallback"),
      to: canChannels ? ("/$slug/settings/channels" as const) : undefined,
      actionLabel: t("ai_hub.manage_customer_channels"),
    },
    {
      key: "internal",
      icon: <IconUsersGroup size={20} />,
      color: "green",
      title: t("ai_hub.profile_internal_title"),
      desc: t("ai_hub.profile_internal_desc"),
      channelsLabel: canChannels ? t("ai_hub.connected_channels", { n: internalChannelCount }) : t("ai_hub.profile_internal_channels"),
      fallback: t("ai_hub.profile_internal_fallback"),
      to: canChannels ? ("/$slug/settings/internal-channels" as const) : undefined,
      actionLabel: t("ai_hub.manage_internal_channels"),
    },
    {
      key: "partner",
      icon: <IconBriefcase size={20} />,
      color: "violet",
      title: t("ai_hub.profile_partner_title"),
      desc: t("ai_hub.profile_partner_desc"),
      channelsLabel: t("ai_hub.profile_partner_channels"),
      fallback: t("ai_hub.profile_partner_fallback"),
      to: canBusiness ? ("/$slug/settings/business" as const) : undefined,
      actionLabel: t("ai_hub.manage_business_channels"),
    },
  ]

  return (
    <Page
      title={t("ai_hub.title")}
      description={t("ai_hub.description")}
      tabs={[
        {
          value: "agents",
          label: t("ai_hub.tab_agents"),
          content: (
            <Stack gap="md">
              <Alert variant="light" color="brand" icon={<IconSparkles size={18} />}>
                <Stack gap={4}>
                  <Text size="sm" fw={600}>
                    {t("ai_hub.banner_title")}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {t("ai_hub.banner_desc")}
                  </Text>
                </Stack>
              </Alert>

              <Paper withBorder p="md" radius="md">
                <Stack gap="xs">
                  <Text size="sm" fw={600}>
                    {t("ai_hub.flow_title")}
                  </Text>
                  <Group gap="xs" wrap="wrap">
                    <Badge size="md" variant="light" color="gray">
                      {t("ai_hub.flow_step_1")}
                    </Badge>
                    <IconArrowRight size={14} style={{ color: "var(--tn-muted)" }} />
                    <Badge size="md" variant="light" color="brand">
                      {t("ai_hub.flow_step_2")}
                    </Badge>
                    <IconArrowRight size={14} style={{ color: "var(--tn-muted)" }} />
                    <Badge size="md" variant="light" color="bright-blue">
                      {t("ai_hub.flow_step_3")}
                    </Badge>
                    <IconArrowRight size={14} style={{ color: "var(--tn-muted)" }} />
                    <Badge size="md" variant="light" color="green">
                      {t("ai_hub.flow_step_4")}
                    </Badge>
                  </Group>
                </Stack>
              </Paper>

              <SimpleGrid cols={{ base: 1, md: 3 }} spacing="md">
                {agents.map((item) => (
                  <Paper key={item.key} withBorder p="md" radius="md" style={{ display: "flex", flexDirection: "column" }}>
                    <Stack gap="sm" style={{ flex: 1 }}>
                      <Group justify="space-between" wrap="nowrap">
                        <ThemeIcon size={36} radius="md" variant="light" color={item.color}>
                          {item.icon}
                        </ThemeIcon>
                        <Badge size="xs" variant="light" color="yellow">
                          {t("ai_placeholder.coming_soon")}
                        </Badge>
                      </Group>
                      <Box>
                        <Title order={5}>{item.title}</Title>
                        <Text size="xs" c="dimmed" mt={4} lh={1.5}>
                          {item.desc}
                        </Text>
                      </Box>
                      <Stack gap={4} mt="auto" pt="xs" style={{ borderTop: "1px solid var(--tn-border)" }}>
                        <Text size="xs" c="dimmed">
                          {item.channelsLabel}
                        </Text>
                        <Text size="xs" c="dimmed">
                          {item.fallback}
                        </Text>
                      </Stack>
                      {item.to && (
                        <Button
                          size="xs"
                          variant="light"
                          rightSection={<IconArrowRight size={13} />}
                          onClick={() => void navigate({ to: item.to!, params: { slug } })}
                        >
                          {item.actionLabel}
                        </Button>
                      )}
                    </Stack>
                  </Paper>
                ))}
              </SimpleGrid>
            </Stack>
          ),
        },
        {
          value: "knowledge",
          label: t("ai_hub.tab_knowledge"),
          content: (
            <PlaceholderGrid
              items={[
                { icon: <IconBook size={18} />, title: t("ai_hub.kb_public_title"), desc: t("ai_hub.kb_public_desc") },
                { icon: <IconShieldCheck size={18} />, title: t("ai_hub.kb_internal_title"), desc: t("ai_hub.kb_internal_desc") },
                { icon: <IconCheck size={18} />, title: t("ai_hub.kb_history_title"), desc: t("ai_hub.kb_history_desc") },
              ]}
            />
          ),
        },
        {
          value: "tools",
          label: t("ai_hub.tab_tools"),
          content: (
            <PlaceholderGrid
              items={[
                { icon: <IconTool size={18} />, title: t("ai_hub.tools_mcp_title"), desc: t("ai_hub.tools_mcp_desc") },
                { icon: <IconShieldCheck size={18} />, title: t("ai_hub.tools_approval_title"), desc: t("ai_hub.tools_approval_desc") },
                { icon: <IconRobot size={18} />, title: t("ai_hub.tools_scope_title"), desc: t("ai_hub.tools_scope_desc") },
              ]}
            />
          ),
        },
        {
          value: "eval",
          label: t("ai_hub.tab_eval"),
          content: (
            <PlaceholderGrid
              items={[
                { icon: <IconChartBar size={18} />, title: t("ai_hub.eval_metrics_title"), desc: t("ai_hub.eval_metrics_desc") },
                { icon: <IconSparkles size={18} />, title: t("ai_hub.eval_replay_title"), desc: t("ai_hub.eval_replay_desc") },
              ]}
            />
          ),
        },
      ]}
    />
  )
}

function PlaceholderGrid({ items }: { items: { icon: ReactNode; title: string; desc: string }[] }): ReactNode {
  const t = useT()
  return (
    <SimpleGrid cols={{ base: 1, md: 3 }} spacing="md">
      {items.map((it) => (
        <Paper key={it.title} withBorder p="md" radius="md" style={{ borderStyle: "dashed" }}>
          <Stack gap="sm">
            <Group justify="space-between" wrap="nowrap">
              <ThemeIcon size={34} radius="md" variant="light" color="brand">
                {it.icon}
              </ThemeIcon>
              <Badge size="xs" variant="light" color="gray">
                {t("ai_placeholder.coming_soon")}
              </Badge>
            </Group>
            <Title order={5}>{it.title}</Title>
            <Text size="xs" c="dimmed" lh={1.55}>
              {it.desc}
            </Text>
          </Stack>
        </Paper>
      ))}
    </SimpleGrid>
  )
}
