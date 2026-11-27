import { PasswordInput } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, preloadResourceList, ResourcePage, useLanguage, useT } from "@go-tenon/ui"
import { LanguageSelect } from "../../../../LanguageSelect"
import { languageName } from "../../../../languages"
import { members } from "../../../../resources"
import { StatusBadge } from "../../../../status"
import type { MemberStatus } from "@agents-desk/client/tenant"

// 成员：管理员和主管理员添加成员、修改角色、移出成员。坐席没有 members:manage，看不到这一页。
export const Route = createFileRoute("/$slug/settings/_workspace/members")({
  staticData: { title: "nav.members", icon: "users", order: 1, permission: "members:manage" },
  validateSearch: members.listSearch,
  // 预取当前条件下的列表：进入页面时表格直接显示数据
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => preloadResourceList(context.app.queryClient, members, deps),
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

// 新账号的沟通与译文语言用全球语言选择器输入，支持任意 BCP 47 标签。
const LanguageField: FieldInput = ({ label, value, onChange, error, disabled }) => {
  const t = useT()
  return (
    <LanguageSelect
      label={label}
      description={t("members.language_hint")}
      error={error}
      disabled={disabled}
      clearable
      value={typeof value === "string" && value ? value : null}
      onChange={(v) => onChange?.(v ?? "")}
    />
  )
}

// 成员的列表、筛选、添加和修改角色按 Go 中的声明渲染（role_id 通过 Refs 自动渲染为角色下拉框）；
// 主管理员那一行没有“修改”和“移出”，在线的人有“设为离开”。
function Members() {
  const ui = useLanguage()
  return (
    <ResourcePage
      model={members}
      route={Route}
      form="modal"
      inputs={{ password: PasswordField, language: LanguageField }}
      render={{
        status: (v) => <StatusBadge status={v as MemberStatus} />,
        language: (v) => (typeof v === "string" && v ? languageName(v, ui) : "—"),
      }}
    />
  )
}
