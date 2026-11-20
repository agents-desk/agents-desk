import { Anchor } from "@mantine/core"
import { createFileRoute, Link } from "@tanstack/react-router"
import { ResourcePage } from "@go-tenon/ui"
import { teams } from "../../../../../resources"

// 团队：主管理员和管理员新建、修改、删除团队；点名称进入团队，管理它的成员。
export const Route = createFileRoute("/t/$slug/settings/_workspace/teams")({
  staticData: { title: "团队", icon: "team", order: 2, permission: "teams:manage" },
  validateSearch: teams.listSearch,
  component: Teams,
})

function Teams() {
  const { slug } = Route.useParams()
  return (
    <ResourcePage
      model={teams}
      route={Route}
      form="modal"
      render={{
        name: (v, row) => (
          <Anchor
            renderRoot={(props) => <Link {...props} to="/t/$slug/settings/team/$id" params={{ slug, id: String(row.id) }} />}
          >
            {String(v)}
          </Anchor>
        ),
      }}
    />
  )
}
