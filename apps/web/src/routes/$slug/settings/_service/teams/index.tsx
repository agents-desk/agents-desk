import { Anchor, Radio, Stack } from "@mantine/core"
import { createFileRoute, Link } from "@tanstack/react-router"
import { type FieldInput, preloadResourceList, ResourcePage, StatusDot } from "@go-tenon/ui"
import { teams } from "../../../../../resources"

// 团队收件箱：有 teams:manage 权限的成员新建、修改、停用/启用和删除空的团队收件箱；点名称进入团队收件箱，管理它的成员和工作时间。
export const Route = createFileRoute("/$slug/settings/_service/teams/")({
  validateSearch: teams.listSearch,
  // 预取当前条件下的列表：进入页面时表格直接显示数据
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => preloadResourceList(context.app.queryClient, teams, deps),
  component: Teams,
})

// AssignmentInput 是分配方式：每种方式一个单选项，每项一行；新建时默认手动。
const AssignmentInput: FieldInput = ({ field, label, value, onChange, error, disabled }) => (
  <Radio.Group label={label} error={error} value={(value as string | undefined) ?? "manual"} onChange={onChange}>
    <Stack gap="xs" mt="xs">
      {(field.options ?? []).map((o) => (
        <Radio key={o.value} value={o.value} label={o.label} disabled={disabled} />
      ))}
    </Stack>
  </Radio.Group>
)

function Teams() {
  const { slug } = Route.useParams()
  return (
    <ResourcePage
      model={teams}
      route={Route}
      form="modal"
      inputs={{ assignment: AssignmentInput }}
      render={{
        name: (v, row) => (
          <Anchor
            renderRoot={(props) => <Link {...props} to="/$slug/settings/teams/$id" params={{ slug, id: String(row.id) }} />}
          >
            {String(v)}
          </Anchor>
        ),
        status: (v) => (
          <StatusDot color={v === "active" ? "green" : "gray"}>{String(teams.display("status", v))}</StatusDot>
        ),
      }}
    />
  )
}
