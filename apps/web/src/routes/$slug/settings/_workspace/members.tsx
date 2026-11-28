import type { MemberStatus } from "@agents-desk/client/tenant"
import { Button, Checkbox, Group, Modal, PasswordInput, Radio, Select, Stack, Text } from "@mantine/core"
import { IconArrowsExchange, IconKey } from "@tabler/icons-react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import {
  applyServerErrors,
  type FieldInput,
  FormError,
  notifySuccess,
  Page,
  preloadResourceList,
  ResourcePage,
  tabbedListSearch,
  useT,
} from "@go-tenon/ui"
import { useForm } from "@mantine/form"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../api"
import { useMembers, useTeams } from "../../../../inbox/data"
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
  const queryClient = useQueryClient()
  const teams = useTeams()
  const { list: allMembers } = useMembers()
  const [resetTarget, setResetTarget] = useState<{ id: string; name: string; email: string } | null>(null)
  const [resetError, setResetError] = useState<ReturnType<typeof applyServerErrors>>()
  const resetForm = useForm<{ mode: "password" | "email"; password: string }>({
    initialValues: { mode: "password", password: "" },
  })
  const [handoverTarget, setHandoverTarget] = useState<{ id: string; name: string; owner: boolean } | null>(null)
  const [handoverError, setHandoverError] = useState<ReturnType<typeof applyServerErrors>>()
  const handoverForm = useForm<{ target_member_id: string | null; target_team_id: string | null; remove_after: boolean }>({
    initialValues: { target_member_id: null, target_team_id: null, remove_after: false },
  })

  const handover = useMutation({
    mutationFn: (v: { id: string; target_member_id: string | null; target_team_id: string | null; remove_after: boolean }) =>
      tenantApi.member.handover(v.id, {
        target_member_id: v.target_member_id || undefined,
        target_team_id: v.target_team_id || undefined,
        remove_after: v.remove_after || undefined,
      }),
    onSuccess: (res) => {
      setHandoverTarget(null)
      setHandoverError(undefined)
      handoverForm.reset()
      void queryClient.invalidateQueries({ queryKey: kit.queries.member.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      notifySuccess(
        t("members.handover_done", {
          convs: res.transferred_conversations,
          tickets: res.transferred_tickets,
        }),
      )
    },
    onError: (err) => {
      setHandoverError(applyServerErrors(handoverForm, err))
    },
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
                rowActions={(row) => {
                  if (row.self === true) return []
                  const actions = [
                    {
                      label: t("members.handover"),
                      icon: <IconArrowsExchange size={14} />,
                      onClick: () => {
                        setHandoverError(undefined)
                        handoverForm.setValues({ target_member_id: null, target_team_id: null, remove_after: false })
                        setHandoverTarget({
                          id: String(row.id ?? ""),
                          name: String(row.name ?? ""),
                          owner: row.owner === true,
                        })
                      },
                    },
                  ]
                  if (row.owner !== true) {
                    actions.push({
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
                    })
                  }
                  return actions
                }}
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
        closeOnClickOutside={false}
        title={t("members.reset_password_title", { name: resetTarget?.name ?? "" })}
      >
        <form
          noValidate
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
                data-autofocus
                {...resetForm.getInputProps("password")}
              />
            )}
            <Group justify="flex-end" gap="xs">
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
      <Modal
        opened={Boolean(handoverTarget)}
        onClose={() => setHandoverTarget(null)}
        closeOnClickOutside={false}
        title={t("members.handover_title", { name: handoverTarget?.name ?? "" })}
      >
        <form
          noValidate
          onSubmit={handoverForm.onSubmit((v) => {
            if (!handoverTarget) return
            handover.mutate({ id: handoverTarget.id, ...v })
          })}
        >
          <Stack gap="md">
            <Text size="sm" c="dimmed">
              {t("members.handover_desc", { name: handoverTarget?.name ?? "" })}
            </Text>
            <FormError error={handoverError} />
            <Select
              label={t("members.handover_target_member")}
              placeholder={t("members.handover_target_member_placeholder")}
              searchable
              clearable
              data-autofocus
              data={allMembers
                .filter((m) => m.id !== handoverTarget?.id)
                .map((m) => ({ value: m.id, label: m.name }))}
              value={handoverForm.values.target_member_id}
              onChange={(v) => handoverForm.setFieldValue("target_member_id", v)}
              error={handoverForm.errors.target_member_id}
            />
            <Select
              label={t("members.handover_target_team")}
              placeholder={t("members.handover_target_team_placeholder")}
              searchable
              clearable
              data={teams.filter((x) => x.status === "active").map((x) => ({ value: x.id, label: x.name }))}
              value={handoverForm.values.target_team_id}
              onChange={(v) => handoverForm.setFieldValue("target_team_id", v)}
              error={handoverForm.errors.target_team_id}
            />
            {!handoverTarget?.owner && (
              <Checkbox
                label={t("members.handover_remove_after")}
                description={t("members.handover_remove_after_hint")}
                checked={handoverForm.values.remove_after}
                onChange={(e) => handoverForm.setFieldValue("remove_after", e.currentTarget.checked)}
              />
            )}
            <Group justify="flex-end" gap="xs">
              <Button variant="default" onClick={() => setHandoverTarget(null)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={handover.isPending}>
                {t("members.handover_submit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </>
  )
}
