import type { MemberStatus } from "@agents-desk/client/tenant"
import { Button, Checkbox, Group, Modal, PasswordInput, Radio, Stack, Text } from "@mantine/core"
import { IconKey } from "@tabler/icons-react"
import { useMutation } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import {
  applyServerErrors,
  type FieldInput,
  FormError,
  notifyError,
  notifySuccess,
  Page,
  preloadResourceList,
  ResourcePage,
  tabbedListSearch,
  useT,
} from "@go-tenon/ui"
import { useForm } from "@mantine/form"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../api"
import { LanguageSelect } from "../../../../LanguageSelect"
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

/** LanguageField 是添加成员表单里的沟通语言选择器。 */
const LanguageField: FieldInput = ({ label, value, onChange, error, disabled }) => {
  const t = useT()
  return (
    <LanguageSelect
      label={label}
      description={t("members.language_hint")}
      allowDeselect={false}
      includeWorkspace={false}
      disabled={disabled}
      value={(value as string) || "zh-CN"}
      error={error}
      onChange={(v) => v && onChange?.(v)}
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
  const [resetTarget, setResetTarget] = useState<{ id: string; name: string; email: string } | null>(null)
  const [resetError, setResetError] = useState<ReturnType<typeof applyServerErrors>>()
  const resetForm = useForm<{ mode: "password" | "email"; password: string }>({
    initialValues: { mode: "password", password: "" },
  })
  const resetPassword = useMutation({
    mutationFn: (v: { id: string; mode: "password" | "email"; password: string }) =>
      tenantApi.member.resetPassword(v.id, v.mode === "email" ? { send_reset_email: true } : { password: v.password }),
    onSuccess: (res) => {
      setResetTarget(null)
      setResetError(undefined)
      resetForm.reset()
      notifySuccess(res.mode === "email" ? t("members.reset_password_emailed", { email: res.email ?? "" }) : t("members.reset_password_done"))
    },
    onError: (err) => {
      setResetError(applyServerErrors(resetForm, err))
      notifyError(err)
    },
  })

  return (
    <>
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
                inputs={{ password: PasswordField, language: LanguageField }}
                render={{ status: (v) => <StatusBadge status={v as MemberStatus} /> }}
                rowActions={(row) =>
                  row.owner === true || row.self === true
                    ? []
                    : [
                        {
                          label: t("members.reset_password"),
                          icon: <IconKey size={14} />,
                          onClick: () => {
                            setResetError(undefined)
                            resetForm.setValues({ mode: "password", password: "" })
                            setResetTarget({
                              id: String(row.id ?? ""),
                              name: String(row.name ?? ""),
                              email: String(row.email ?? ""),
                            })
                          },
                        },
                      ]
                }
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
      <Modal
        opened={Boolean(resetTarget)}
        onClose={() => setResetTarget(null)}
        title={t("members.reset_password_title", { name: resetTarget?.name ?? "" })}
      >
        <form
          onSubmit={resetForm.onSubmit((v) => {
            if (!resetTarget) return
            resetPassword.mutate({ id: resetTarget.id, mode: v.mode, password: v.password })
          })}
        >
          <Stack gap="md">
            <Text size="sm" c="dimmed">
              {t("members.reset_password_desc", { email: resetTarget?.email ?? "" })}
            </Text>
            <FormError error={resetError} />
            <Radio.Group
              label={t("members.reset_password_mode")}
              value={resetForm.values.mode}
              onChange={(v) => resetForm.setFieldValue("mode", v === "email" ? "email" : "password")}
            >
              <Stack gap="xs" mt="xs">
                <Radio value="password" label={t("members.reset_password_mode_direct")} />
                <Radio value="email" label={t("members.reset_password_mode_email")} />
              </Stack>
            </Radio.Group>
            {resetForm.values.mode === "password" && (
              <PasswordInput
                label={t("profile.new_password")}
                description={t("profile.new_password_hint")}
                required
                {...resetForm.getInputProps("password")}
              />
            )}
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setResetTarget(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={resetPassword.isPending}>
                {t("common.save")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </>
  )
}
