import { PasswordInput, Select } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage } from "@go-tenon/ui"
import { tenantApi } from "../../../../../api"
import { members } from "../../../../../resources"

// 成员：管理员和主管理员添加成员、修改角色、移出成员。坐席没有 members:manage，看不到这一页。
export const Route = createFileRoute("/t/$slug/settings/_workspace/members")({
  staticData: { title: "成员", icon: "users", order: 1, permission: "members:manage" },
  validateSearch: members.listSearch,
  component: Members,
})

// 新账号的初始密码用密码框输入。
const PasswordField: FieldInput = ({ field, label, value, onChange, error, disabled }) => (
  <PasswordInput
    label={label}
    description="这个邮箱还没有账号时必填，由你告诉本人"
    required={field.required}
    error={error}
    disabled={disabled}
    value={(value as string) ?? ""}
    onChange={(e) => onChange?.(e.currentTarget.value)}
  />
)

// 角色从这个工作区的角色中选择；还没有角色时提示先新建。框架的资源支持引用字段之后（开发计划 F6），改用引用字段。
const RoleField: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const roles = useQuery({ queryKey: ["roles", "all"], queryFn: () => tenantApi.role.list({ per_page: 100 }) })
  const items = roles.data?.items ?? []
  return (
    <Select
      label={label}
      required={field.required}
      error={error}
      disabled={disabled}
      data={items.map((r) => ({ value: r.id, label: r.name }))}
      value={(value as string | undefined) ?? null}
      onChange={(v) => onChange?.(v ?? undefined)}
      description={roles.isSuccess && items.length === 0 ? "还没有角色，先在“角色”中新建" : undefined}
    />
  )
}

// 成员的列表、添加和修改角色按 Go 中的声明渲染；主管理员那一行没有“修改”和“移出”。
function Members() {
  return <ResourcePage model={members} route={Route} form="modal" inputs={{ password: PasswordField, role_id: RoleField }} />
}
