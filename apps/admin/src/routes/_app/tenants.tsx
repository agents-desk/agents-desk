import { Anchor, Badge, Button, Group, Modal, NumberInput, PasswordInput, Stack, Text } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { ExportButton, type FieldInput, notifyError, notifySuccess, ResourcePage, useCan, useT } from "@go-tenon/ui"
import type { LlmTenantBudget, TenantDTO } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api } from "../../api"
import { ChangeOwnerModal } from "../../change-owner"
import { ImpersonateTenantModal } from "../../impersonate-tenant"
import { tenants } from "../../resources"

export const Route = createFileRoute("/_app/tenants")({
  staticData: { title: "nav.tenants", icon: "team", order: 1, permission: "tenants:read" },
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

// 工作区的列表、新建和修改按 Go 中的声明渲染；名称链接到工作区的地址，状态显示为带颜色的标签，行上多一个“更换主管理员”，
// 工具栏提供后台异步导出按钮（进度同时同步到顶栏任务中心，完成后推送站内通知）。
function Tenants() {
  const can = useCan()
  const t = useT()
  const search = Route.useSearch()
  const [changing, setChanging] = useState<TenantDTO>()
  const [impersonating, setImpersonating] = useState<TenantDTO>()
  const [budgetTenant, setBudgetTenant] = useState<TenantDTO>()
  return (
    <>
      <ResourcePage
        model={tenants}
        route={Route}
        form="modal"
        inputs={{ owner_password: PasswordField }}
        toolbar={
          <ExportButton
            title={t("tenants.export")}
            source={{
              start: () =>
                api.tenant.exportStart({
                  format: "xlsx",
                  name: typeof search.name === "string" ? search.name : undefined,
                  status: search.status === "active" || search.status === "disabled" ? search.status : undefined,
                }),
              progress: (id) => (o) => api.tenant.exportProgress(id, o),
            }}
          />
        }
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
        rowActions={(row) => {
          const item = row as unknown as TenantDTO
          const actions: { label: string; onClick: () => void }[] = []
          if (can("tenants:impersonate") && item.status === "active") {
            actions.push({ label: t("tenants.impersonate"), onClick: () => setImpersonating(item) })
          }
          if (can("tenants:manage")) {
            actions.push({ label: t("tenants.change_owner"), onClick: () => setChanging(item) })
          }
          if (can("platform-settings:manage")) {
            actions.push({ label: t("tenants.llm_budget"), onClick: () => setBudgetTenant(item) })
          }
          return actions
        }}
      />
      <ChangeOwnerModal tenant={changing} onClose={() => setChanging(undefined)} />
      <ImpersonateTenantModal tenant={impersonating} onClose={() => setImpersonating(undefined)} />
      <TenantBudgetModal tenant={budgetTenant} onClose={() => setBudgetTenant(undefined)} />
    </>
  )
}

function TenantBudgetModal({ tenant, onClose }: { tenant?: TenantDTO; onClose: () => void }) {
  const t = useT()
  const queryClient = useQueryClient()
  const [dailyCost, setDailyCost] = useState<string | number>("")
  const [monthlyCost, setMonthlyCost] = useState<string | number>("")
  const [dailyTokens, setDailyTokens] = useState<string | number>("")

  const current = useQuery({
    queryKey: ["llm", "tenant-budget", tenant?.id],
    queryFn: () => {
      if (!tenant) throw new Error("missing tenant")
      return api.llm.tenantBudgetGet(tenant.id)
    },
    enabled: Boolean(tenant),
  })

  useEffect(() => {
    if (!current.data) return
    const ov = current.data.override
    setDailyCost(ov.daily_cost_usd ?? "")
    setMonthlyCost(ov.monthly_cost_usd ?? "")
    setDailyTokens(ov.daily_total_tokens ?? "")
  }, [current.data])

  const save = useMutation({
    mutationFn: () => {
      if (!tenant) throw new Error("missing tenant")
      const body: LlmTenantBudget = {
        daily_cost_usd: typeof dailyCost === "number" ? dailyCost : null,
        monthly_cost_usd: typeof monthlyCost === "number" ? monthlyCost : null,
        daily_total_tokens: typeof dailyTokens === "number" ? dailyTokens : null,
      }
      return api.llm.tenantBudgetSave(tenant.id, body)
    },
    onSuccess: (saved) => {
      if (tenant) {
        queryClient.setQueryData(["llm", "tenant-budget", tenant.id], saved)
      }
      notifySuccess(t("tenants.llm_budget_saved"))
      onClose()
    },
    onError: (err) => notifyError(err),
  })

  return (
    <Modal
      opened={Boolean(tenant)}
      onClose={onClose}
      title={tenant ? t("tenants.llm_budget_title", { name: tenant.name }) : ""}
      size="md"
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("tenants.llm_budget_desc")}
        </Text>
        <NumberInput
          label={t("tenants.llm_budget_daily_cost")}
          placeholder={t("tenants.llm_budget_placeholder")}
          min={0}
          max={100000}
          decimalScale={4}
          value={dailyCost}
          onChange={setDailyCost}
        />
        <NumberInput
          label={t("tenants.llm_budget_monthly_cost")}
          placeholder={t("tenants.llm_budget_placeholder")}
          min={0}
          max={1000000}
          decimalScale={4}
          value={monthlyCost}
          onChange={setMonthlyCost}
        />
        <NumberInput
          label={t("tenants.llm_budget_daily_tokens")}
          placeholder={t("tenants.llm_budget_placeholder")}
          min={0}
          max={10000000000}
          allowDecimal={false}
          value={dailyTokens}
          onChange={setDailyTokens}
        />
        {current.data && (
          <Text size="xs" c="dimmed">
            {t("tenants.llm_budget_effective", {
              daily_cost: String(current.data.effective.daily_cost_usd),
              monthly_cost: String(current.data.effective.monthly_cost_usd),
              daily_tokens: String(current.data.effective.daily_total_tokens),
            })}
          </Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button loading={save.isPending} onClick={() => save.mutate()}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
