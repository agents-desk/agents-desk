import { Anchor, Badge, PasswordInput } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage, useCan } from "@go-tenon/ui"
import type { TenantDTO } from "@agents-desk/client/admin"
import { useState } from "react"
import { ChangeOwnerModal } from "../../change-owner"
import { tenants } from "../../resources"

export const Route = createFileRoute("/_app/tenants")({
  staticData: { title: "工作区", icon: "team", order: 1, permission: "tenants:read" },
  validateSearch: tenants.listSearch,
  component: Tenants,
})

// 主管理员的初始密码用密码框输入。
const PasswordField: FieldInput = ({ field, label, value, onChange, error, disabled }) => (
  <PasswordInput
    label={label}
    required={field.required}
    error={error}
    disabled={disabled}
    value={(value as string) ?? ""}
    onChange={(e) => onChange?.(e.currentTarget.value)}
  />
)

// 工作区的列表、新建和修改按 Go 中的声明渲染；名称链接到工作区的地址，状态显示为带颜色的标签，行上多一个“更换主管理员”。
function Tenants() {
  const can = useCan()
  const [changing, setChanging] = useState<TenantDTO>()
  return (
    <>
      <ResourcePage
        model={tenants}
        route={Route}
        form="modal"
        inputs={{ owner_password: PasswordField }}
        render={{
          name: (v, row) => (
            <Anchor href={String(row.url)} target="_blank" rel="noreferrer">
              {String(v)}
            </Anchor>
          ),
          // 状态的颜色：正常用 green，停用（没有状态）用 gray
          status: (v) => (
            <Badge variant="light" color={v === "active" ? "green" : "gray"}>
              {String(tenants.display("status", v))}
            </Badge>
          ),
        }}
        rowActions={(row) =>
          can("tenants:manage") ? [{ label: "更换主管理员", onClick: () => setChanging(row as unknown as TenantDTO) }] : []
        }
      />
      <ChangeOwnerModal tenant={changing} onClose={() => setChanging(undefined)} />
    </>
  )
}
