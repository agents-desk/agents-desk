import { Checkbox, Radio, Stack } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage, useT } from "@go-tenon/ui"
import { roles } from "../../../../../resources"

// 角色：有“管理成员和角色”权限的成员新建、修改、删除角色。角色包括会话的可见范围和可以做的操作；
// 每个角色都能查看成员和团队收件箱、查看和回复看得到的会话。主管理员拥有全部权限，不在列表中。
export const Route = createFileRoute("/t/$slug/settings/_workspace/roles")({
  staticData: { title: "nav.roles", icon: "shield", order: 2, permission: "members:manage" },
  validateSearch: roles.listSearch,
  component: Roles,
})

// ScopeInput 是会话的可见范围：三个单选项，每项一行。
const ScopeInput: FieldInput = ({ field, label, value, onChange, error, disabled }) => (
  <Radio.Group label={label} required={field.required} error={error} value={(value as string) ?? ""} onChange={onChange}>
    <Stack gap="xs" mt="xs">
      {(field.options ?? []).map((o) => (
        <Radio key={o.value} value={o.value} label={o.label} disabled={disabled} />
      ))}
    </Stack>
  </Radio.Group>
)

// PermissionsInput 是可以做的操作：每个操作一个复选框，每项一行。
const PermissionsInput: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const t = useT()
  return (
    <Checkbox.Group
      label={label}
      description={t("roles.permissions_hint")}
      error={error}
      value={(value as string[]) ?? []}
      onChange={onChange}
    >
      <Stack gap="xs" mt="xs">
        {(field.options ?? []).map((o) => (
          <Checkbox key={o.value} value={o.value} label={o.label} disabled={disabled} />
        ))}
      </Stack>
    </Checkbox.Group>
  )
}

// 角色的列表和表单按 Go 中的声明渲染，可见范围和操作改用单选和复选框。
function Roles() {
  const t = useT()
  return (
    <ResourcePage
      model={roles}
      route={Route}
      form="modal"
      description={t("roles.description")}
      inputs={{ conversation_scope: ScopeInput, permissions: PermissionsInput }}
    />
  )
}
