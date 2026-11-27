import { Alert, Avatar, Badge, Button, Group, Radio, Stack, Text } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconUserMinus } from "@tabler/icons-react"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, confirm, DataTable, FormError, notifyError, notifySuccess, Page, preloadQueries, ResourceSelect, useCan, useT } from "@go-tenon/ui"
import type { HoursSchedule, TeamDTO } from "@agents-desk/client/tenant"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { emptySchedule, ScheduleFields } from "../../../../../business-hours"
import { RowActions } from "../../../../../components/row-actions"
import { useUnsavedGuard } from "../../../../../components/unsaved-guard"
import { members as memberResource } from "../../../../../resources"
import { StatusBadge } from "../../../../../status"

// 一个团队收件箱：成员（从工作区的成员中选人加入，移出团队收件箱）和工作时间（使用工作区的，或者单独设置），两个标签各自保存。权限来自上级的 teams 布局，菜单中“团队收件箱”保持选中。
export const Route = createFileRoute("/$slug/settings/_service/teams/$id")({
  staticData: { title: "team_members.title" },
  // 预取页面的数据，最多等一小会儿：切换到这一页时内容一次完整出现，不先空白再出现
  loader: ({ context, params }) =>
    preloadQueries(context.app.queryClient, [
      { queryKey: ["team", params.id], queryFn: () => tenantApi.team.get(params.id) },
      { queryKey: ["team", params.id, "members"], queryFn: () => tenantApi.team.members(params.id) },
    ]),
  component: TeamMembers,
})

function TeamMembers(): ReactNode {
  const { slug, id } = Route.useParams()
  const navigate = useNavigate()
  const t = useT()
  const can = useCan()
  const queryClient = useQueryClient()
  const team = useQuery({ queryKey: ["team", id], queryFn: () => tenantApi.team.get(id) })
  const members = useQuery({ queryKey: ["team", id, "members"], queryFn: () => tenantApi.team.members(id) })
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["team", id] })
    void queryClient.invalidateQueries({ queryKey: kit.queries.team.key() })
    void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
  }

  const disable = useMutation({
    mutationFn: () => tenantApi.team.disable(id),
    onSuccess: (saved) => {
      queryClient.setQueryData(["team", id], saved)
      refresh()
      notifySuccess(t("teams.disabled_done"))
    },
    onError: (err) => notifyError(err),
  })
  const enable = useMutation({
    mutationFn: () => tenantApi.team.enable(id),
    onSuccess: (saved) => {
      queryClient.setQueryData(["team", id], saved)
      refresh()
      notifySuccess(t("teams.enabled_done"))
    },
    onError: (err) => notifyError(err),
  })

  const put = useMutation({
    mutationFn: (memberID: string) => tenantApi.team.memberPut(id, memberID),
    onSuccess: refresh,
    onError: (err) => notifyError(err),
  })
  const remove = useMutation({
    mutationFn: (memberID: string) => tenantApi.team.memberRemove(id, memberID),
    onSuccess: refresh,
    onError: (err) => notifyError(err),
  })

  const [adding, setAdding] = useState<string | null>(null)
  const inTeam = new Set((members.data ?? []).map((m) => m.member_id))
  const add = async () => {
    if (!adding) return
    await put.mutateAsync(adding)
    notifySuccess(t("team_members.added"))
    setAdding(null)
  }

  const isDisabled = team.data?.status === "disabled"
  const toggleDisabled = async () => {
    if (!team.data) return
    if (isDisabled) {
      enable.mutate()
      return
    }
    if (
      await confirm({
        title: t("teams.disable_confirm", { name: team.data.name }),
        message: t("teams.disable_message"),
        danger: true,
        confirmLabel: t("teams.disable"),
      })
    ) {
      disable.mutate()
    }
  }

  return (
    <Page
      title={team.data ? t("team_members.heading", { name: team.data.name }) : t("team_members.title")}
      description={team.data?.description || undefined}
      // 标题左边的返回回到团队收件箱列表，而不是离开设置
      back={() => void navigate({ to: "/$slug/settings/teams", params: { slug } })}
      actions={
        can("teams:manage") && team.data ? (
          <Button
            variant={isDisabled ? "filled" : "light"}
            color={isDisabled ? undefined : "red"}
            size="xs"
            loading={disable.isPending || enable.isPending}
            onClick={() => void toggleDisabled()}
          >
            {t(isDisabled ? "teams.enable" : "teams.disable")}
          </Button>
        ) : undefined
      }
      tabs={[
        {
          value: "members",
          label: t("team_members.tab_members"),
          count: members.data?.length || undefined,
          content: (
            <Stack gap="md">
              <Group align="flex-end" gap="sm">
                <ResourceSelect
                  model={memberResource}
                  label={t("team_members.add")}
                  placeholder={t("team_members.add_placeholder")}
                  filterRow={(row) => !inTeam.has(String(row.id))}
                  getOption={(row) => ({
                    value: String(row.id),
                    label: t("team_members.add_option", { name: String(row.name ?? ""), role: String(row.role_name ?? "") }),
                  })}
                  value={adding}
                  onChange={setAdding}
                  nothingFoundMessage={t("team_members.no_candidates")}
                  style={{ flex: 1 }}
                />
                <Button onClick={() => void add()} disabled={!adding} loading={put.isPending && !!adding}>
                  {t("team_members.add_submit")}
                </Button>
              </Group>
              <DataTable
                records={members.data ?? []}
                rowKey="member_id"
                loading={members.isPending}
                empty={t("team_members.empty")}
                columns={[
                  {
                    accessor: "name",
                    title: t("team_members.member"),
                    render: (m) => (
                      <Group gap="sm" wrap="nowrap">
                        <Avatar src={m.avatar_url || null} size={32} radius="xl" name={m.name} color="initials" />
                        <div>
                          <Text size="sm">{m.name}</Text>
                          <Text size="xs" c="dimmed">
                            {m.email}
                          </Text>
                        </div>
                      </Group>
                    ),
                  },
                  {
                    accessor: "role_name",
                    title: t("team_members.role"),
                    render: (m) => (
                      <Badge variant="light" color="gray">
                        {m.role_name}
                      </Badge>
                    ),
                  },
                  { accessor: "status", title: t("team_members.status"), render: (m) => <StatusBadge status={m.status} /> },
                  {
                    accessor: "actions",
                    title: "",
                    textAlign: "right",
                    render: (m) => (
                      <RowActions
                        actions={[
                          {
                            label: t("team_members.remove"),
                            icon: <IconUserMinus size={14} />,
                            danger: true,
                            onClick: async () => {
                              if (
                                await confirm({
                                  title: t("team_members.remove_confirm", { name: m.name }),
                                  message: t("team_members.remove_message"),
                                  danger: true,
                                  confirmLabel: t("team_members.remove"),
                                })
                              )
                                remove.mutate(m.member_id)
                            },
                          },
                        ]}
                      />
                    ),
                  },
                ]}
              />
            </Stack>
          ),
        },
        { value: "hours", label: t("team_members.tab_hours"), content: team.data && <TeamHours team={team.data} /> },
      ]}
    >
      {isDisabled && (
        <Alert color="gray" variant="light">
          {t("teams.disabled_notice")}
        </Alert>
      )}
    </Page>
  )
}

/** TeamHours 是团队收件箱的工作时间：使用工作区的，或者单独设置时区和每周的工作时间。特殊日期和回复时间总是工作区的。 */
function TeamHours({ team }: { team: TeamDTO }): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const workspace = useQuery({ queryKey: ["business-hours"], queryFn: () => tenantApi.hours.get() })
  const initial = (own: HoursSchedule | undefined) => ({ custom: own ? "custom" : "workspace", schedule: own ?? emptySchedule })
  const form = useForm<{ custom: string; schedule: HoursSchedule }>({ initialValues: initial(team.business_hours) })
  const [error, setError] = useState<ErrorInfo>()
  const [saving, setSaving] = useState(false)
  // 第一次改为单独设置时，从工作区的工作时间开始改
  useEffect(() => {
    if (!team.business_hours && workspace.data) {
      const { time_zone, week } = workspace.data
      form.setInitialValues({ custom: "workspace", schedule: { time_zone, week } })
      form.setValues({ custom: "workspace", schedule: { time_zone, week } })
    }
  }, [workspace.data])
  const custom = form.getValues().custom === "custom"
  useUnsavedGuard(form.isDirty())
  return (
    <form
      noValidate
      onSubmit={form.onSubmit(async (values) => {
        setSaving(true)
        setError(undefined)
        try {
          const saved = await tenantApi.team.businessHours(team.id, values.custom === "custom" ? { schedule: values.schedule } : {})
          queryClient.setQueryData(["team", team.id], saved)
          form.resetDirty()
          notifySuccess(t("common.saved"))
        } catch (err) {
          setError(applyServerErrors(form, err))
        } finally {
          setSaving(false)
        }
      })}
    >
      <Stack gap="md">
        <FormError error={error} />
        <Radio.Group label={t("team_hours.label")} {...form.getInputProps("custom")}>
          <Stack gap="xs" mt="xs">
            <Radio value="workspace" label={t("team_hours.workspace")} />
            <Radio value="custom" label={t("team_hours.custom")} />
          </Stack>
        </Radio.Group>
        <Text size="sm" c="dimmed">
          {t("team_hours.hint")}
        </Text>
        {custom && <ScheduleFields form={form} path="schedule" />}
        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty()} loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
