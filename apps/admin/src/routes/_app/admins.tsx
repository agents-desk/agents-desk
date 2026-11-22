import { Badge, Group, PasswordInput, Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage, useT } from "@go-tenon/ui"
import { admins } from "../../resources"

// 平台管理员：管理整套部署的平台管理员账号（列表、新建、改名、停用和启用）。
export const Route = createFileRoute("/_app/admins")({
  staticData: { title: "nav.admins", icon: "users", order: 2, permission: "platform-admins:manage" },
  validateSearch: admins.listSearch,
  component: Admins,
})

// 初始密码使用密码框输入。
const PasswordField: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const t = useT()
  return (
    <PasswordInput
      label={label}
      description={t("admins.password_hint")}
      required={field.required}
      error={error}
      disabled={disabled}
      value={(value as string) ?? ""}
      onChange={(e) => onChange?.(e.currentTarget.value)}
    />
  )
}

function Admins() {
  const t = useT()
  return (
    <ResourcePage
      model={admins}
      route={Route}
      form="modal"
      inputs={{ password: PasswordField }}
      render={{
        name: (v, row) => (
          <Group gap="xs">
            <Text size="sm">{String(v)}</Text>
            {Boolean(row.self) && (
              <Badge variant="light" color="bright-blue" size="xs">
                {t("admins.self_badge")}
              </Badge>
            )}
          </Group>
        ),
        status: (v) => (
          <Badge variant="light" color={v === "active" ? "green" : "gray"}>
            {String(admins.display("status", v))}
          </Badge>
        ),
      }}
    />
  )
}
