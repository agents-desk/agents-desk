import type { AutomationBuiltin, AutomationRuleDTO, ConversationAssignmentSettings, ConversationDefaultTarget } from "@agents-desk/client/tenant"
import { Alert, Badge, Button, Group, List, Loader, Paper, Select, Stack, Switch, Text } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { AnchorLink, applyServerErrors, FormError, notifyError, notifySuccess, Page, preloadQueries, useCan, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../api"
import { useUnsavedGuard } from "../../../../components/unsaved-guard"
import { useMembers, useTeams } from "../../../../inbox/data"

// 指派与自动化：合并新会话默认指派设置和内置自动化规则开关两个页内标签，共用 tenant-settings:manage 权限。
export const Route = createFileRoute("/$slug/settings/_service/rules")({
  staticData: { title: "nav.assignment_rules", icon: "bolt", order: 4, permission: "tenant-settings:manage" },
  loader: ({ context }) =>
    preloadQueries(context.app.queryClient, [kit.queries.assignment.settingsGet(), kit.queries.automation.list()]),
  component: AssignmentAndRulesPage,
})

function AssignmentAndRulesPage(): ReactNode {
  const { slug } = Route.useParams()
  const t = useT()
  const settingsQuery = useQuery(kit.queries.assignment.settingsGet())
  const data = settingsQuery.data

  return (
    <Page
      title={t("nav.assignment_rules")}
      description={t("assignment_rules.description")}
      tabs={[
        {
          value: "assignment",
          label: t("nav.assignment"),
          content: data ? (
            <AssignmentForm key={`${data.default_target}:${data.default_team_id}:${data.default_member_id}`} initial={data} slug={slug} />
          ) : (
            <Loader size="sm" />
          ),
        },
        {
          value: "automations",
          label: t("automations.title"),
          content: <AutomationsTab slug={slug} />,
        },
      ]}
    />
  )
}

/** AssignmentForm 配置工作区默认指派目标，并展示内置指派规则与关联入口。 */
function AssignmentForm({ initial, slug }: { initial: ConversationAssignmentSettings; slug: string }): ReactNode {
  const t = useT()
  const can = useCan()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const teams = useTeams()
  const { list: members } = useMembers()
  const form = useForm({
    initialValues: {
      default_target: initial.default_target as ConversationDefaultTarget,
      default_team_id: initial.default_team_id || "",
      default_member_id: initial.default_member_id || "",
    },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const targetOptions = kit.enums.ConversationDefaultTarget.values.map((v) => ({
    value: v,
    label: kit.label("ConversationDefaultTarget", v),
  }))
  const activeTeams = teams.filter((x) => x.status === "active")
  const savedTeam = initial.default_target === "team" && initial.default_team_id ? teams.find((x) => x.id === initial.default_team_id) : undefined
  const savedTeamDisabled = savedTeam?.status === "disabled"
  const teamOptions = activeTeams.map((x) => ({ value: x.id, label: x.name }))
  const memberOptions = members.map((m) => ({
    value: m.id,
    label: m.status === "away" ? `${m.name} (${t("inbox.away")})` : m.name,
  }))

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await tenantApi.assignment.settingsUpdate({
        default_target: values.default_target,
        default_team_id: values.default_target === "team" ? values.default_team_id : undefined,
        default_member_id: values.default_target === "member" ? values.default_member_id : undefined,
      })
      await queryClient.invalidateQueries({ queryKey: kit.queries.assignment.key() })
      form.resetDirty()
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  useUnsavedGuard(form.isDirty())
  return (
    <Stack gap="lg">
      <Text size="sm" c="dimmed">
        {t("assignment_page.description")}
      </Text>

      <Alert color="bright-blue" variant="light" icon={<IconInfoCircle size={16} />}>
        {t("assignment_page.priority_notice")}
      </Alert>

      {savedTeamDisabled && (
        <Alert color="yellow" variant="light" icon={<IconAlertTriangle size={16} />}>
          {t("assignment_page.default_team_disabled_warning")}
        </Alert>
      )}

      <form noValidate onSubmit={submit}>
        <Stack gap="md">
          <FormError error={error} />
          <Select
            label={t("assignment_page.default_target")}
            description={t("assignment_page.default_target_hint")}
            data={targetOptions}
            allowDeselect={false}
            value={form.values.default_target}
            onChange={(v) => {
              if (!v) return
              form.setFieldValue("default_target", v as ConversationDefaultTarget)
            }}
          />
          {form.values.default_target === "team" && (
            <Select
              label={t("assignment_page.default_team")}
              description={t("assignment_page.default_team_hint")}
              placeholder={t("assignment_page.default_team_placeholder")}
              data={teamOptions}
              searchable
              clearable
              value={form.values.default_team_id || null}
              onChange={(v) => form.setFieldValue("default_team_id", v ?? "")}
              error={form.errors.default_team_id}
            />
          )}
          {form.values.default_target === "member" && (
            <Select
              label={t("assignment_page.default_member")}
              description={t("assignment_page.default_member_hint")}
              placeholder={t("assignment_page.default_member_placeholder")}
              data={memberOptions}
              searchable
              clearable
              value={form.values.default_member_id || null}
              onChange={(v) => form.setFieldValue("default_member_id", v ?? "")}
              error={form.errors.default_member_id}
            />
          )}
          <Group justify="flex-end">
            <Button type="submit" disabled={!form.isDirty()} loading={saving}>
              {t("common.save")}
            </Button>
          </Group>
        </Stack>
      </form>

      <Paper withBorder radius="md" p="md">
        <Stack gap="sm">
          <Text size="sm" fw={600}>
            {t("assignment_page.rules_title")}
          </Text>
          <List size="sm" spacing="xs">
            <List.Item>{t("assignment_page.rule_round_robin")}</List.Item>
            <List.Item>{t("assignment_page.rule_reply")}</List.Item>
            <List.Item>{t("assignment_page.rule_away")}</List.Item>
          </List>
          {(can("teams:manage") || can("channels:manage")) && (
            <Group gap="xs" mt={4}>
              {can("teams:manage") && (
                <Button size="xs" variant="default" onClick={() => void navigate({ to: "/$slug/settings/teams", params: { slug } })}>
                  {t("assignment_page.go_teams")}
                </Button>
              )}
              {can("channels:manage") && (
                <Button size="xs" variant="default" onClick={() => void navigate({ to: "/$slug/settings/channels", params: { slug } })}>
                  {t("assignment_page.go_channels")}
                </Button>
              )}
            </Group>
          )}
        </Stack>
      </Paper>
    </Stack>
  )
}

/** builtinCopy 返回内置自动化规则的标题和说明消息 ID。 */
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

/** AutomationsTab 列出并控制工作区的内置自动化规则。 */
function AutomationsTab({ slug }: { slug: string }): ReactNode {
  const t = useT()
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

  if (list.isPending) return <Loader size="sm" />

  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">
        {t("automations.description")}
      </Text>
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
                    <AnchorLink to="/$slug/settings/business-hours" params={{ slug }} size="sm">
                      {t("automations.business_hours_link")}
                    </AnchorLink>
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
    </Stack>
  )
}
