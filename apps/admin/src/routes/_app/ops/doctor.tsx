import { Alert, Badge, Button, Card, Code, Group, Loader, Stack, Text } from "@mantine/core"
import { IconRefresh } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { type MessageId, Page, useT } from "@go-tenon/ui"
import type { DoctorResult } from "@agents-desk/client/admin"
import { api } from "../../../api"

// 运行环境自检页面：调用 /admin/api/system/doctor，按依赖分组展示检查结果和修复指引。
export const Route = createFileRoute("/_app/ops/doctor")({
  staticData: { title: "nav.doctor", icon: "shield", order: 3, permission: "system:read" },
  component: SystemDoctor,
})

const statusColor: Record<DoctorResult["status"], string> = {
  passed: "green",
  warning: "yellow",
  failed: "red",
  skipped: "gray",
}

const statusLabel: Record<DoctorResult["status"], MessageId> = {
  passed: "doctor.status.passed",
  warning: "doctor.status.warning",
  failed: "doctor.status.failed",
  skipped: "doctor.status.skipped",
}

function SystemDoctor() {
  const t = useT()
  const report = useQuery({
    queryKey: ["system", "doctor"],
    queryFn: () => api.system.doctor(),
  })

  const results = report.data?.results ?? []
  const failedCount = results.filter((r) => r.status === "failed").length
  const warningCount = results.filter((r) => r.status === "warning").length

  const groups: { name: string; items: DoctorResult[] }[] = []
  for (const item of results) {
    let g = groups.find((x) => x.name === item.group)
    if (!g) {
      g = { name: item.group, items: [] }
      groups.push(g)
    }
    g.items.push(item)
  }

  return (
    <Page
      title={t("nav.doctor")}
      description={t("doctor.description")}
      actions={
        <Button
          variant="default"
          size="xs"
          leftSection={<IconRefresh size={14} />}
          loading={report.isFetching}
          onClick={() => void report.refetch()}
        >
          {t("doctor.refresh")}
        </Button>
      }
    >
      <Stack gap="md">
        {report.isPending && <Loader size="sm" />}
        {report.isSuccess && failedCount > 0 && (
          <Alert color="red">{t("doctor.failed_summary", { count: failedCount })}</Alert>
        )}
        {report.isSuccess && failedCount === 0 && warningCount > 0 && (
          <Alert color="yellow">{t("doctor.warning_summary", { count: warningCount })}</Alert>
        )}
        {report.isSuccess && failedCount === 0 && warningCount === 0 && (
          <Alert color="green">{t("doctor.all_passed")}</Alert>
        )}
        {groups.map((group) => (
          <Card key={group.name} withBorder padding="md">
            <Stack gap="sm">
              <Text fw={600} size="sm">
                {group.name}
              </Text>
              {group.items.map((item) => (
                <Stack key={`${item.group}:${item.check}`} gap={4}>
                  <Group justify="space-between" wrap="nowrap">
                    <Text size="sm" ff="monospace">
                      {item.check}
                    </Text>
                    <Badge variant="light" color={statusColor[item.status]}>
                      {t(statusLabel[item.status])}
                    </Badge>
                  </Group>
                  {item.summary && (
                    <Text size="sm" c="dimmed">
                      {item.summary}
                    </Text>
                  )}
                  {item.fix && (
                    <Stack gap={2}>
                      <Text size="xs" c="dimmed">
                        {t("doctor.fix")}
                      </Text>
                      <Code block>{item.fix}</Code>
                    </Stack>
                  )}
                </Stack>
              ))}
            </Stack>
          </Card>
        ))}
      </Stack>
    </Page>
  )
}
