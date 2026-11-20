import { Anchor, Radio, Stack } from "@mantine/core"
import { createFileRoute, Link } from "@tanstack/react-router"
import { type FieldInput, ResourcePage } from "@go-tenon/ui"
import { teams } from "../../../../../../resources"

// 团队收件箱：有 teams:manage 权限的成员新建、修改、删除团队收件箱；点名称进入团队收件箱，管理它的成员。
// 还有没关闭的会话、或者是渠道的默认团队收件箱时不能删除。
export const Route = createFileRoute("/t/$slug/settings/_workspace/teams/")({
  validateSearch: teams.listSearch,
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
            renderRoot={(props) => <Link {...props} to="/t/$slug/settings/teams/$id" params={{ slug, id: String(row.id) }} />}
          >
            {String(v)}
          </Anchor>
        ),
      }}
    />
  )
}
