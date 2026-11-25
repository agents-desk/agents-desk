import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Divider,
  Group,
  Loader,
  NumberInput,
  SegmentedControl,
  Stack,
  Table,
  Text,
} from "@mantine/core"
import { IconTrash } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { notifyError, notifySuccess, Page, ResourceSelect, useT } from "@go-tenon/ui"
import type { FlagsFlagDTO, FlagsMode } from "@agents-desk/client/admin"
import { useState } from "react"
import { api } from "../../../api"
import { tenants } from "../../../resources"

// 功能开关管理：配置每个开关的全员规则（跟随默认、全员开启、全员关闭、按比例放量）与按工作区的单独覆盖。
export const Route = createFileRoute("/_app/settings/features")({
  staticData: { title: "nav.features", icon: "plugin", order: 5, permission: "features:manage" },
  component: FeatureFlagsPage,
})

const queryKey = ["features"]

function FeatureFlagsPage() {
  const t = useT()
  const list = useQuery({ queryKey, queryFn: () => api.feature.list() })

  return (
    <Page title={t("nav.features")} description={t("features.description")}>
      <Stack gap="md">
        {list.isPending && <Loader size="sm" />}
        {(list.data?.items ?? []).map((flag) => (
          <FlagCard key={flag.name} flag={flag} />
        ))}
      </Stack>
    </Page>
  )
}

type GlobalChoice = "default" | FlagsMode

function FlagCard({ flag }: { flag: FlagsFlagDTO }) {
  const t = useT()
  const queryClient = useQueryClient()
  const [percent, setPercent] = useState<number>(flag.everyone?.mode === "percent" ? (flag.everyone.percent ?? 0) : 10)
  const [tenantID, setTenantID] = useState<string | null>(null)
  const [overrideMode, setOverrideMode] = useState<"on" | "off">("on")

  const updateCache = (updated: FlagsFlagDTO) => {
    queryClient.setQueryData<{ items: FlagsFlagDTO[] }>(queryKey, (prev) =>
      prev ? { items: prev.items.map((f) => (f.name === updated.name ? updated : f)) } : prev,
    )
    notifySuccess(t("features.saved"))
  }

  const setRule = useMutation({
    mutationFn: (body: { scope: "everyone" | "tenant"; tenant_id?: string; mode: FlagsMode; percent?: number }) =>
      api.feature.set(flag.name, body),
    onSuccess: updateCache,
    onError: (err) => notifyError(err),
  })

  const clearRule = useMutation({
    mutationFn: (params: { scope: "everyone" | "tenant"; tenant_id?: string }) => api.feature.clear(flag.name, params),
    onSuccess: updateCache,
    onError: (err) => notifyError(err),
  })

  const currentGlobal: GlobalChoice = flag.everyone ? flag.everyone.mode : "default"
  const overriddenTenants = new Set(flag.overrides.map((o) => o.tenant_id ?? ""))

  const onGlobalChange = (next: string) => {
    const choice = next as GlobalChoice
    if (choice === "default") {
      clearRule.mutate({ scope: "everyone" })
      return
    }
    if (choice === "percent") {
      setRule.mutate({ scope: "everyone", mode: "percent", percent })
      return
    }
    setRule.mutate({ scope: "everyone", mode: choice })
  }

  const onAddOverride = () => {
    if (!tenantID) return
    setRule.mutate(
      { scope: "tenant", tenant_id: tenantID, mode: overrideMode },
      { onSuccess: () => setTenantID(null) },
    )
  }

  const busy = setRule.isPending || clearRule.isPending

  return (
    <Card withBorder padding="md">
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <Stack gap={2}>
            <Group gap="xs">
              <Text fw={600} ff="monospace" size="sm">
                {flag.name}
              </Text>
              <Badge variant="light" color={flag.default_on ? "green" : "gray"} size="xs">
                {t(flag.default_on ? "features.default_on" : "features.default_off")}
              </Badge>
              {flag.by_tenant && (
                <Badge variant="outline" color="bright-blue" size="xs">
                  {t("features.by_tenant")}
                </Badge>
              )}
            </Group>
            <Text size="sm" c="dimmed">
              {flag.description}
            </Text>
          </Stack>
          <Group gap="xs" align="center">
            <Text size="xs" c="dimmed">
              {t("features.everyone_label")}
            </Text>
            <SegmentedControl
              size="xs"
              disabled={busy}
              value={currentGlobal}
              onChange={onGlobalChange}
              data={[
                { value: "default", label: t("features.mode.default") },
                { value: "on", label: t("features.mode.on") },
                { value: "off", label: t("features.mode.off") },
                { value: "percent", label: t("features.mode.percent") },
              ]}
            />
          </Group>
        </Group>

        {currentGlobal === "percent" && (
          <Group gap="sm" align="flex-end">
            <NumberInput
              label={t("features.percent_label")}
              min={0}
              max={100}
              value={percent}
              onChange={(v) => setPercent(typeof v === "number" ? v : 0)}
              w={160}
              size="xs"
            />
            <Button
              size="xs"
              variant="default"
              loading={busy}
              onClick={() => setRule.mutate({ scope: "everyone", mode: "percent", percent })}
            >
              {t("features.percent_apply")}
            </Button>
          </Group>
        )}

        <Divider />

        <Stack gap="xs">
          <Text size="xs" fw={600} c="dimmed">
            {t("features.overrides_title")}
          </Text>
          <Group gap="sm" align="flex-end">
            <ResourceSelect
              model={tenants}
              placeholder={t("features.select_tenant")}
              filterRow={(row) => !overriddenTenants.has(String(row.id))}
              getOption={(row) => ({ value: String(row.id), label: `${String(row.name ?? "")} (${String(row.slug ?? "")})` })}
              value={tenantID}
              onChange={setTenantID}
              size="xs"
              style={{ flex: 1, maxWidth: 320 }}
            />
            <SegmentedControl
              size="xs"
              value={overrideMode}
              onChange={(v) => setOverrideMode(v as "on" | "off")}
              data={[
                { value: "on", label: t("features.override_on") },
                { value: "off", label: t("features.override_off") },
              ]}
            />
            <Button size="xs" disabled={!tenantID} loading={busy} onClick={onAddOverride}>
              {t("features.add_override")}
            </Button>
          </Group>

          {flag.overrides.length === 0 ? (
            <Text size="xs" c="dimmed">
              {t("features.no_overrides")}
            </Text>
          ) : (
            <Table verticalSpacing="xs">
              <Table.Tbody>
                {flag.overrides.map((o) => (
                  <Table.Tr key={o.tenant_id}>
                    <Table.Td>
                      <Text size="sm">
                        {o.tenant_name || o.tenant_id}
                        {o.tenant_slug ? ` (${o.tenant_slug})` : ""}
                      </Text>
                    </Table.Td>
                    <Table.Td w={100}>
                      <Badge variant="light" color={o.mode === "on" ? "green" : "red"} size="xs">
                        {t(o.mode === "on" ? "features.override_on" : "features.override_off")}
                      </Badge>
                    </Table.Td>
                    <Table.Td align="right" w={60}>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        size="sm"
                        aria-label={t("features.remove_override")}
                        disabled={busy}
                        onClick={() => clearRule.mutate({ scope: "tenant", tenant_id: o.tenant_id })}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}
        </Stack>
      </Stack>
    </Card>
  )
}
