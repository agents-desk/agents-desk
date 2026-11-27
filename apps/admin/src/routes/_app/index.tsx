import { Alert, Anchor, Avatar, Badge, Card, Center, Group, Loader, SimpleGrid, Stack, Table, Text } from "@mantine/core"
import { IconAlertTriangle } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { AnchorLink, Page, StatCard, useFormatDateTime, useT, useTimeZone } from "@go-tenon/ui"
import type { AuditItem, PlatformoverviewOverview, PlatformoverviewRecentTenant } from "@agents-desk/client/admin"
import type { ReactNode } from "react"
import { kit } from "../../api"

// 管理员端首页概览：展示平台工作区、活跃会话、模型调用、队列状态以及最近工作区与操作日志。
export const Route = createFileRoute("/_app/")({
  staticData: { title: "nav.overview", icon: "dashboard", order: 0 },
  component: OverviewPage,
})

const integer = new Intl.NumberFormat()

// formatCost 显示美元金额：1 美元以上保留两位小数，更小金额保留四位有效数字。
function formatCost(v: number): string {
  if (v === 0) return "$0"
  return v >= 1 ? `$${v.toFixed(2)}` : `$${Number(v.toPrecision(4))}`
}

function OverviewPage(): ReactNode {
  const t = useT()
  const zone = useTimeZone() ?? Intl.DateTimeFormat().resolvedOptions().timeZone
  const overview = useQuery(kit.queries.overview.get({ tz: zone }))
  const data = overview.data
  const hasAnySection =
    !!data &&
    (data.tenants != null ||
      data.admins != null ||
      data.usage != null ||
      data.queue != null ||
      (data.recent_tenants?.length ?? 0) > 0 ||
      (data.recent_audits?.length ?? 0) > 0)

  return (
    <Page title={t("nav.overview")} breadcrumbs={false} description={t("overview.description", { tz: zone })}>
      {overview.isPending ? (
        <Center py="xl">
          <Loader size="sm" />
        </Center>
      ) : overview.error ? (
        <Alert color="red" variant="light" title={t("overview.load_failed")}>
          {String(overview.error instanceof Error ? overview.error.message : overview.error)}
        </Alert>
      ) : !hasAnySection ? (
        <Card withBorder padding="lg">
          <Text c="dimmed" size="sm">
            {t("overview.no_sections")}
          </Text>
        </Card>
      ) : (
        <Stack gap="md">
          {data.queue && data.queue.failed_total > 0 && (
            <Alert
              color="deep-orange"
              variant="light"
              icon={<IconAlertTriangle size={16} />}
              title={t("overview.queue_failed_alert", { count: integer.format(data.queue.failed_total) })}
            >
              <AnchorLink to="/ops/queue" size="sm">
                {t("overview.queue_failed_action")}
              </AnchorLink>
            </Alert>
          )}
          <InventoryGrid data={data} />
          <ActivityKpis data={data} />
          {(data.tenants != null || data.recent_audits != null) && (
            <SimpleGrid cols={{ base: 1, lg: data.tenants != null && data.recent_audits != null ? 2 : 1 }} spacing="md">
              {data.tenants != null && <RecentTenantsCard items={data.recent_tenants ?? []} />}
              {data.recent_audits != null && <RecentAuditsCard items={data.recent_audits} />}
            </SimpleGrid>
          )}
        </Stack>
      )}
    </Page>
  )
}

// InventoryGrid 展示工作区、打开中会话、工作区成员与平台管理员的资源卡片。
function InventoryGrid({ data }: { data: PlatformoverviewOverview }): ReactNode {
  const t = useT()
  const cards: {
    key: string
    label: string
    value: string
    hint?: string
    badge?: { text: string; color: string }
    to?: "/tenants" | "/admins"
  }[] = []

  if (data.tenants != null) {
    cards.push({
      key: "tenants",
      label: t("overview.tenants_label"),
      value: integer.format(data.tenants.total),
      hint: t("overview.tenants_hint", {
        active: integer.format(data.tenants.active),
        disabled: integer.format(data.tenants.disabled),
      }),
      badge:
        data.tenants.disabled > 0
          ? { text: t("overview.tenants_disabled_badge", { count: integer.format(data.tenants.disabled) }), color: "gray" }
          : undefined,
      to: "/tenants",
    })
  }
  if (data.usage != null) {
    cards.push({
      key: "conversations",
      label: t("overview.conversations_label"),
      value: integer.format(data.usage.conversations_open),
      hint: t("overview.conversations_hint", {
        total: integer.format(data.usage.conversations_total),
        today: integer.format(data.usage.conversations_today),
      }),
    })
    cards.push({
      key: "members",
      label: t("overview.members_label"),
      value: integer.format(data.usage.members_total),
      hint: t("overview.members_hint", { contacts: integer.format(data.usage.contacts_total) }),
    })
  }
  if (data.admins != null) {
    cards.push({
      key: "admins",
      label: t("overview.admins_label"),
      value: integer.format(data.admins.total),
      hint: t("overview.admins_hint", {
        active: integer.format(data.admins.active),
        disabled: integer.format(data.admins.disabled),
      }),
      to: "/admins",
    })
  }

  if (cards.length === 0) return null

  return (
    <SimpleGrid cols={{ base: 1, sm: 2, lg: Math.min(cards.length, 4) }} spacing="md">
      {cards.map((c) => (
        <Card key={c.key} withBorder padding="md">
          <Group justify="space-between" align="center">
            {c.to ? (
              <AnchorLink to={c.to} size="xs" c="dimmed">
                {c.label}
              </AnchorLink>
            ) : (
              <Text size="xs" c="dimmed">
                {c.label}
              </Text>
            )}
            {c.badge && (
              <Badge size="xs" color={c.badge.color} variant="light">
                {c.badge.text}
              </Badge>
            )}
          </Group>
          <Text size="xl" fw={600} mt={4}>
            {c.value}
          </Text>
          {c.hint && (
            <Text size="xs" c="dimmed" mt={2}>
              {c.hint}
            </Text>
          )}
        </Card>
      ))}
    </SimpleGrid>
  )
}

// ActivityKpis 使用框架 StatCard 展示近 7 天新增工作区、会话量、模型调用与队列运行健康度。
function ActivityKpis({ data }: { data: PlatformoverviewOverview }): ReactNode {
  const t = useT()
  const items: ReactNode[] = []

  if (data.tenants != null) {
    items.push(
      <StatCard
        key="new-tenants"
        label={t("overview.new_tenants_7d")}
        value={integer.format(data.tenants.new_last_7_days)}
        secondary={t("overview.new_tenants_7d_sub", { total: integer.format(data.tenants.total) })}
      />,
    )
  }
  if (data.usage != null) {
    items.push(
      <StatCard
        key="conv-7d"
        label={t("overview.conversations_7d")}
        value={integer.format(data.usage.conversations_last_7_days)}
        secondary={t("overview.conversations_7d_sub", {
          today: integer.format(data.usage.conversations_today),
          open: integer.format(data.usage.conversations_open),
        })}
      />,
    )
    items.push(
      <StatCard
        key="llm-7d"
        label={t("overview.llm_calls_7d")}
        value={integer.format(data.usage.llm_calls_last_7_days)}
        secondary={t("overview.llm_calls_7d_sub", {
          today: integer.format(data.usage.llm_calls_today),
          failed: integer.format(data.usage.llm_failures_last_7_days),
          cost: formatCost(data.usage.llm_cost_last_7_days),
        })}
      />,
    )
  }
  if (data.queue != null) {
    items.push(
      <StatCard
        key="queue"
        label={
          <AnchorLink to="/ops/queue" size="xs" c="dimmed">
            {t("overview.queue_label")}
          </AnchorLink>
        }
        value={
          data.queue.failed_total > 0
            ? t("overview.queue_failed_count", { count: integer.format(data.queue.failed_total) })
            : t("overview.queue_healthy")
        }
        secondary={t("overview.queue_pending_sub", {
          pending: integer.format(data.queue.outbox_pending),
          failed: integer.format(data.queue.failed_total),
        })}
      />,
    )
  }

  if (items.length === 0) return null

  return (
    <SimpleGrid cols={{ base: 1, sm: 2, lg: Math.min(items.length, 4) }} spacing="md">
      {items}
    </SimpleGrid>
  )
}

// RecentTenantsCard 展示最近创建的 5 个工作区，支持直接跳转工作区地址或进入工作区列表。
function RecentTenantsCard({ items }: { items: PlatformoverviewRecentTenant[] }): ReactNode {
  const t = useT()
  const formatDateTime = useFormatDateTime()
  return (
    <Card withBorder padding="md">
      <Group justify="space-between" mb="sm">
        <Text size="sm" fw={500}>
          {t("overview.recent_tenants_title")}
        </Text>
        <AnchorLink to="/tenants" size="xs">
          {t("overview.view_all")}
        </AnchorLink>
      </Group>
      {items.length === 0 ? (
        <Text size="sm" c="dimmed" py="md">
          {t("overview.recent_tenants_empty")}
        </Text>
      ) : (
        <Table verticalSpacing="xs">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("overview.col_workspace")}</Table.Th>
              <Table.Th>{t("overview.col_status")}</Table.Th>
              <Table.Th ta="right">{t("overview.col_created")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {items.map((tn) => (
              <Table.Tr key={tn.id}>
                <Table.Td>
                  <Group gap="xs" wrap="nowrap">
                    <Avatar src={tn.logo_url || null} size={24} radius="sm" color="brand">
                      {tn.name.slice(0, 1)}
                    </Avatar>
                    <div>
                      <Anchor href={tn.url} target="_blank" rel="noreferrer" size="sm" fw={500}>
                        {tn.name}
                      </Anchor>
                      <Text size="xs" c="dimmed">
                        /app/{tn.slug}/
                      </Text>
                    </div>
                  </Group>
                </Table.Td>
                <Table.Td>
                  <Badge variant="light" size="xs" color={tn.status === "active" ? "green" : "gray"}>
                    {t(tn.status === "active" ? "overview.status_active" : "overview.status_disabled")}
                  </Badge>
                </Table.Td>
                <Table.Td ta="right">
                  <Text size="xs" c="dimmed" style={{ whiteSpace: "nowrap" }}>
                    {formatDateTime(tn.created_at)}
                  </Text>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Card>
  )
}

// RecentAuditsCard 展示最近 5 条平台操作日志。
function RecentAuditsCard({ items }: { items: AuditItem[] }): ReactNode {
  const t = useT()
  const formatDateTime = useFormatDateTime()
  return (
    <Card withBorder padding="md">
      <Group justify="space-between" mb="sm">
        <Text size="sm" fw={500}>
          {t("overview.recent_audits_title")}
        </Text>
        <AnchorLink to="/ops/audit" size="xs">
          {t("overview.view_all")}
        </AnchorLink>
      </Group>
      {items.length === 0 ? (
        <Text size="sm" c="dimmed" py="md">
          {t("overview.recent_audits_empty")}
        </Text>
      ) : (
        <Stack gap="sm">
          {items.map((item) => (
            <Group key={item.id} justify="space-between" align="flex-start" wrap="nowrap" gap="sm">
              <div style={{ minWidth: 0 }}>
                <Text size="sm">{item.text}</Text>
                {item.actor && (
                  <Text size="xs" c="dimmed">
                    {item.actor}
                  </Text>
                )}
              </div>
              <Text size="xs" c="dimmed" style={{ whiteSpace: "nowrap", flex: "none" }}>
                {formatDateTime(item.created_at)}
              </Text>
            </Group>
          ))}
        </Stack>
      )}
    </Card>
  )
}
