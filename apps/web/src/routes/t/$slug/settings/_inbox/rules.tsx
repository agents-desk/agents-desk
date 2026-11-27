import type { AutomationBuiltin, AutomationRuleDTO } from "@agents-desk/client/tenant"
import { Anchor, Badge, Group, Loader, Paper, Stack, Switch, Text } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useParams } from "@tanstack/react-router"
import { notifyError, notifySuccess, Page, preloadQueries, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit, tenantApi } from "../../../../../api"

// 内置自动化：管理员可以逐条开关。触发、条件和动作由服务端引擎按规则执行。
export const Route = createFileRoute("/t/$slug/settings/_inbox/rules")({
  staticData: { title: "automations.title", icon: "bolt", order: 4, permission: "tenant-settings:manage" },
  // 预取页面的数据，最多等一小会儿：切换到这一页时内容一次完整出现，不先空白再出现
  loader: ({ context }) => preloadQueries(context.app.queryClient, [kit.queries.automation.list()]),
  component: Automations,
})

/** builtinCopy 返回内置规则的标题和说明消息 ID。 */
function builtinCopy(key: AutomationBuiltin): {
  title: "automations.reply_time_title" | "automations.collect_lead_contact_title" | "automations.request_rating_title" | "automations.ticket_status_notify_title"
  desc: "automations.reply_time_desc" | "automations.collect_lead_contact_desc" | "automations.request_rating_desc" | "automations.ticket_status_notify_desc"
} {
  switch (key) {
    case "reply_time":
      return { title: "automations.reply_time_title", desc: "automations.reply_time_desc" }
    case "collect_lead_contact":
      return { title: "automations.collect_lead_contact_title", desc: "automations.collect_lead_contact_desc" }
    case "request_rating":
      return { title: "automations.request_rating_title", desc: "automations.request_rating_desc" }
    case "ticket_status_notify":
      return { title: "automations.ticket_status_notify_title", desc: "automations.ticket_status_notify_desc" }
  }
}

function Automations(): ReactNode {
  const t = useT()
  const { slug } = useParams({ from: "/t/$slug" })
  const queryClient = useQueryClient()
  const list = useQuery(kit.queries.automation.list())

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => tenantApi.automation.update(id, { enabled }),
    onSuccess: (updated) => {
      queryClient.setQueryData<AutomationRuleDTO[]>(kit.queries.automation.list().queryKey, (prev) =>
        prev?.map((r) => (r.id === updated.id ? updated : r)),
      )
      notifySuccess(t(updated.enabled ? "automations.enabled" : "automations.disabled"))
    },
    onError: (err) => notifyError(err),
  })

  return (
    <Page title={t("automations.title")} description={t("automations.description")}>
      {list.isPending ? (
        <Loader size="sm" />
      ) : (
        <Stack gap="sm">
          {(list.data ?? []).map((rule) => {
            const copy = rule.builtin_key ? builtinCopy(rule.builtin_key) : undefined
            const title = copy ? t(copy.title) : rule.name
            const pending = toggle.isPending && toggle.variables?.id === rule.id
            return (
              <Paper key={rule.id} withBorder radius="md" p="md">
                <Group justify="space-between" align="flex-start" wrap="nowrap" gap="md">
                  <Stack gap={6} style={{ flex: 1, minWidth: 0 }}>
                    <Group gap="xs">
                      <Text fw={600}>{title}</Text>
                      <Badge size="sm" variant="light" color="gray">
                        {kit.label("AutomationTrigger", rule.trigger)}
                      </Badge>
                    </Group>
                    {copy && (
                      <Text size="sm" c="dimmed">
                        {t(copy.desc)}
                      </Text>
                    )}
                    {rule.builtin_key === "reply_time" && (
                      <Anchor component={Link} to={`/t/${slug}/settings/business-hours`} size="sm">
                        {t("automations.business_hours_link")}
                      </Anchor>
                    )}
                  </Stack>
                  <Switch
                    checked={rule.enabled}
                    disabled={pending}
                    aria-label={title}
                    onChange={(e) => toggle.mutate({ id: rule.id, enabled: e.currentTarget.checked })}
                  />
                </Group>
              </Paper>
            )
          })}
        </Stack>
      )}
    </Page>
  )
}
