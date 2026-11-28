import { Badge } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { ResourcePage } from "@go-tenon/ui"
import { members } from "../../resources"

// 工作区成员：管理员端查看全平台成员账号、所属工作区数并支持全局停用与启用。
export const Route = createFileRoute("/_app/members")({
  staticData: { title: "nav.members", icon: "users", order: 2, permission: "tenants:manage" },
  validateSearch: members.listSearch,
  component: GlobalMembers,
})

function GlobalMembers() {
  return (
    <ResourcePage
      model={members}
      route={Route}
      render={{
        status: (v) => (
          <Badge variant="light" color={v === "active" ? "green" : "gray"}>
            {String(members.display("status", v))}
          </Badge>
        ),
      }}
    />
  )
}
