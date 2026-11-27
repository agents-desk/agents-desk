import type { MemberStatus } from "@agents-desk/client/tenant"
import { Checkbox, PasswordInput, Radio, Stack } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, Page, preloadResourceList, ResourcePage, tabbedListSearch, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { members, roles } from "../../../../resources"
import { StatusBadge } from "../../../../status"

// 成员与角色：合并工作区成员管理和角色权限配置两个页内标签，共用 members:manage 权限。
export const Route = createFileRoute("/$slug/settings/_workspace/members")({
  staticData: { title: "nav.members_roles", icon: "users", order: 1, permission: "members:manage" },
  validateSearch: tabbedListSearch({ members: members.listSearch, roles: roles.listSearch }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => {
    const { tab, ...rest } = deps as Record<string, unknown>
    return preloadResourceList(context.app.queryClient, tab === "roles" ? roles : members, rest)
  },
  component: MembersAndRolesPage,
})

/** PasswordField 是新账号初始密码的输入框。 */
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

/** ScopeInput 是会话可见范围单选组。 */
const ScopeInput: FieldInput = ({ field, label, value, onChange, error, disabled }) => (
  <Radio.Group label={label} required={field.required} error={error} value={(value as string) ?? ""} onChange={onChange}>
    <Stack gap="xs" mt="xs">
      {(field.options ?? []).map((o) => (
        <Radio key={o.value} value={o.value} label={o.label} disabled={disabled} />
      ))}
    </Stack>
  </Radio.Group>
)

/** PermissionsInput 是角色操作权限复选组。 */
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

function MembersAndRolesPage(): ReactNode {
  const t = useT()

  return (
    <Page
      title={t("nav.members_roles")}
      tabs={[
        {
          value: "members",
          label: t("nav.members"),
          content: (
            <ResourcePage
              title={null}
              description={t("members.description")}
              model={members}
              route={Route}
              form="modal"
              inputs={{ password: PasswordField }}
              render={{ status: (v) => <StatusBadge status={v as MemberStatus} /> }}
            />
          ),
        },
        {
          value: "roles",
          label: t("nav.roles"),
          content: (
            <ResourcePage
              title={null}
              description={t("roles.description")}
              model={roles}
              route={Route}
              form="modal"
              inputs={{ conversation_scope: ScopeInput, permissions: PermissionsInput }}
            />
          ),
        },
      ]}
    />
  )
}
