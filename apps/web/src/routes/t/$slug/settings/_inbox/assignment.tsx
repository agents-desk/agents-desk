import type { ConversationAssignmentSettings, ConversationDefaultTarget } from "@agents-desk/client/tenant"
import { Alert, Button, Group, List, Paper, Select, Stack, Text } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconAlertTriangle, IconInfoCircle } from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { applyServerErrors, FormError, notifySuccess, Page, useCan, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { useMembers, useTeams } from "../../../../../inbox/data"
import { useUnsavedGuard } from "../../../../../components/unsaved-guard"

// 指派设置：配置工作区的新会话默认指派目标（团队收件箱或指定成员），当渠道未单独指定默认团队收件箱时生效。
export const Route = createFileRoute("/t/$slug/settings/_inbox/assignment")({
  staticData: { title: "nav.assignment", icon: "task", order: 2, permission: "tenant-settings:manage" },
  loader: () => tenantApi.assignment.settingsGet(),
  component: AssignmentPage,
})

function AssignmentPage(): ReactNode {
  const initial = Route.useLoaderData()
  const { slug } = Route.useParams()
  const t = useT()
  const settingsQuery = useQuery({ ...kit.queries.assignment.settingsGet(), initialData: initial })
  const data = settingsQuery.data

  return (
    <Page title={t("nav.assignment")} description={t("assignment_page.description")}>
      <AssignmentForm key={`${data.default_target}:${data.default_team_id}:${data.default_member_id}`} initial={data} slug={slug} />
    </Page>
  )
}

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
                <Button size="xs" variant="default" onClick={() => void navigate({ to: "/t/$slug/settings/teams", params: { slug } })}>
                  {t("assignment_page.go_teams")}
                </Button>
              )}
              {can("channels:manage") && (
                <Button size="xs" variant="default" onClick={() => void navigate({ to: "/t/$slug/settings/widgets", params: { slug } })}>
                  {t("assignment_page.go_widgets")}
                </Button>
              )}
            </Group>
          )}
        </Stack>
      </Paper>
    </Stack>
  )
}
