import { PasswordInput } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage } from "@go-tenon/ui"
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

// 成员的列表、添加和修改角色按 Go 中的声明渲染；主管理员那一行没有“移出”。
function Members() {
  return <ResourcePage model={members} route={Route} form="modal" inputs={{ password: PasswordField }} />
}
