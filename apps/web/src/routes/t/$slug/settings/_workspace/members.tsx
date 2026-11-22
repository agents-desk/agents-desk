import { PasswordInput } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage, ResourceSelect, useT } from "@go-tenon/ui"
import { members, roles } from "../../../../../resources"
import { StatusBadge } from "../../../../../status"
import type { MemberStatus } from "@agents-desk/client/tenant"

// 成员：管理员和主管理员添加成员、修改角色、移出成员。坐席没有 members:manage，看不到这一页。
export const Route = createFileRoute("/t/$slug/settings/_workspace/members")({
  staticData: { title: "nav.members", icon: "users", order: 1, permission: "members:manage" },
  validateSearch: members.listSearch,
  component: Members,
})

// 新账号的初始密码用密码框输入。
const PasswordField: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const t = useT()
  return (
    <PasswordInput
      label={label}
      description={t("members.password_hint")}
      required={field.required}
      error={error}
      disabled={disabled}
      value={(value as string) ?? ""}
      onChange={(e) => onChange?.(e.currentTarget.value)}
    />
  )
}

// 角色从这个工作区的角色中选择；还没有角色时提示先新建。
const RoleField: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const t = useT()
  return (
    <ResourceSelect
      model={roles}
      label={label}
      required={field.required}
      error={error}
      disabled={disabled}
      value={(value as string | undefined) ?? null}
      onChange={(v) => onChange?.(v ?? undefined)}
      nothingFoundMessage={t("members.no_roles")}
    />
  )
}

// 成员的列表、添加和修改角色按 Go 中的声明渲染；主管理员那一行没有“修改”和“移出”，在线的人有“设为离开”。
function Members() {
  return <ResourcePage model={members} route={Route} form="modal" inputs={{ password: PasswordField, role_id: RoleField }}
      render={{ status: (v) => <StatusBadge status={v as MemberStatus} /> }}
    />
}
