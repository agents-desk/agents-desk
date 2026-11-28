import type { TelegramDTO } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Code,
  CopyButton,
  Group,
  PasswordInput,
  SegmentedControl,
  Skeleton,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconCheck, IconCopy } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { applyServerErrors, confirm, FormError, FormSection, notifyError, notifySuccess, Page, ResourceSelect, TableCard, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../../api"
import { AiReceptionCard } from "../../components/ai-placeholders"
import { useUnsavedGuard } from "../../components/unsaved-guard"
import { teams } from "../../resources"

/** Copy 复制 value 并在按钮上短暂显示对勾；默认尺寸与 sm 输入框等高。 */
function Copy({ value }: { value: string }): ReactNode {
  const t = useT()
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={t(copied ? "telegram_install.copied" : "telegram_install.copy")}>
          <ActionIcon variant="subtle" size="input-sm" color={copied ? "green" : "gray"} onClick={copy} aria-label={t("telegram_install.copy")}>
            {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  )
}

const gatewayHeaders = [
  { name: "X-Agents-Desk-External-Id", desc: "telegram_install.gateway_header_id" },
  { name: "X-Agents-Desk-External-Email", desc: "telegram_install.gateway_header_email" },
  { name: "X-Agents-Desk-External-Name", desc: "telegram_install.gateway_header_name" },
] as const

/** TelegramFormValues 是 Telegram 渠道配置工作台的表单草稿结构。 */
interface TelegramFormValues {
  name: string
  audience: "customer" | "partner" | "internal"
  bot_token: string
  team_id: string | null
  gateway_managed: boolean
  enable_groups: boolean
  enabled: boolean
}

/** toFormValues 将服务端返回的 TelegramDTO 转换为表单初始值。 */
function toFormValues(tg: TelegramDTO): TelegramFormValues {
  return {
    name: tg.name,
    audience: tg.audience === "internal" ? "internal" : tg.audience === "partner" ? "partner" : "customer",
    bot_token: "",
    team_id: tg.team_id ?? null,
    gateway_managed: tg.gateway_managed,
    enable_groups: tg.enable_groups,
    enabled: tg.enabled,
  }
}

/** TelegramInstallPanel 是“安装与接入”页签：展示 Webhook 地址、密钥与外部网关透传头说明。 */
function TelegramInstallPanel({ id }: { id: string }): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const install = useQuery({ queryKey: ["telegram", id, "installation"], queryFn: () => tenantApi.telegram.installation(id) })
  const rotate = useMutation({
    mutationFn: () => tenantApi.telegram.rotateSecret(id),
    onSuccess: (data) => {
      queryClient.setQueryData(["telegram", id, "installation"], data)
      notifySuccess(t("telegram_install.rotated"))
    },
    onError: (err) => notifyError(err),
  })

  const askRotate = async () => {
    if (
      await confirm({
        title: t("telegram_install.rotate_confirm"),
        message: t("telegram_install.rotate_message"),
        danger: true,
        confirmLabel: t("telegram_install.rotate"),
      })
    ) {
      rotate.mutate()
    }
  }

  if (install.isPending) {
    return (
      <Stack gap="md">
        <Skeleton height={240} radius="md" />
        <Skeleton height={180} radius="md" />
      </Stack>
    )
  }

  return (
    <Stack gap="md">
      <FormSection
        title={t("telegram_install.webhook")}
        description={
          install.data
            ? t(install.data.gateway_managed ? "telegram_install.mode_gateway_hint" : "telegram_install.mode_direct_hint")
            : undefined
        }
        actions={
          install.data && (
            <Badge variant="light" color={install.data.gateway_managed ? "bright-blue" : "gray"}>
              {t(install.data.gateway_managed ? "telegrams.mode_gateway" : "telegrams.mode_direct")}
            </Badge>
          )
        }
      >
        <Group align="flex-end" gap="xs">
          <TextInput label={t("telegram_install.channel_id")} value={install.data?.channel_id ?? id} readOnly style={{ flex: 1 }} />
          <Copy value={install.data?.channel_id ?? id} />
        </Group>
        {install.data?.bot_username && (
          <TextInput label={t("telegram_install.bot_username")} value={`@${install.data.bot_username}`} readOnly />
        )}
        <Group align="flex-end" gap="xs">
          <TextInput label={t("telegram_install.webhook_url")} value={install.data?.webhook_url ?? ""} readOnly style={{ flex: 1 }} />
          {install.data && <Copy value={install.data.webhook_url} />}
        </Group>
        <Group align="flex-end" gap="xs">
          <PasswordInput label={t("telegram_install.webhook_secret")} value={install.data?.webhook_secret ?? ""} readOnly style={{ flex: 1 }} />
          {install.data && <Copy value={install.data.webhook_secret} />}
        </Group>
        <Group justify="flex-end">
          <Button variant="light" color="red" onClick={() => void askRotate()} loading={rotate.isPending}>
            {t("telegram_install.rotate")}
          </Button>
        </Group>
      </FormSection>

      <FormSection title={t("telegram_install.gateway_headers")} description={t("telegram_install.gateway_headers_hint")}>
        <TableCard minWidth={480}>
          <Table verticalSpacing="xs" horizontalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("telegram_install.header")}</Table.Th>
                <Table.Th>{t("telegram_install.header_desc")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {gatewayHeaders.map((h) => (
                <Table.Tr key={h.name}>
                  <Table.Td>
                    <Code>{h.name}</Code>
                  </Table.Td>
                  <Table.Td>{t(h.desc)}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </TableCard>
      </FormSection>
    </Stack>
  )
}

/**
 * TelegramStudio 是 Telegram 机器人渠道的配置页：
 *   - 基本配置：按分组卡片编辑基础信息、Bot Token / 接入模式与团队路由；
 *   - 安装与接入：查看渠道 ID、Webhook 地址、密钥与外部网关透传头说明。
 */
export function TelegramStudio({
  slug,
  id,
  backRoute = "/$slug/settings/channels",
}: {
  slug: string
  id: string
  backRoute?: "/$slug/settings/channels" | "/$slug/settings/business" | "/$slug/settings/internal-channels"
}): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  // 当前标签记在地址的 ?tab= 中（Page 的 tabs），只有“配置”标签显示保存按钮
  const tab = ((useSearch({ strict: false }) as { tab?: string }).tab ?? "config")
  const [serverError, setServerError] = useState<ReturnType<typeof applyServerErrors>>()

  const tg = useQuery({
    queryKey: ["telegram", id],
    queryFn: () => tenantApi.telegram.get(id),
  })

  const form = useForm<TelegramFormValues>({
    initialValues: {
      name: "",
      audience: "customer",
      bot_token: "",
      team_id: null,
      gateway_managed: false,
      enable_groups: true,
      enabled: true,
    },
  })

  useEffect(() => {
    if (!tg.data) return
    form.initialize(toFormValues(tg.data))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅在服务端数据到达或切换渠道时重置基准
  }, [tg.data?.id])

  const save = useMutation({
    mutationFn: (values: TelegramFormValues) =>
      tenantApi.telegram.update(id, {
        name: values.name.trim(),
        audience: values.audience,
        bot_token: values.bot_token.trim() || undefined,
        team_id: values.team_id || undefined,
        gateway_managed: values.gateway_managed,
        enable_groups: values.enable_groups,
        enabled: values.enabled,
      }),
    onSuccess: (updated) => {
      setServerError(undefined)
      queryClient.setQueryData(["telegram", id], updated)
      void queryClient.invalidateQueries({ queryKey: ["telegram", id, "installation"] })
      void queryClient.invalidateQueries({ queryKey: ["resource", "telegram"] })
      const next = toFormValues(updated)
      form.setValues(next)
      form.resetDirty(next)
      notifySuccess(t("telegram_studio.saved"))
    },
    onError: (err) => {
      setServerError(applyServerErrors(form, err))
    },
  })

  const dirty = form.isDirty()
  useUnsavedGuard(dirty)

  const handleDiscard = () => {
    if (!tg.data) return
    setServerError(undefined)
    form.clearErrors()
    const base = toFormValues(tg.data)
    form.setValues(base)
    form.resetDirty(base)
  }

  return (
    <Page
      title={tg.data ? tg.data.name : t("telegram_install.title")}
      back={() =>
        void navigate({
          to: backRoute,
          params: { slug },
          search: { tab: backRoute === "/$slug/settings/business" ? "telegram_bots" : "telegram" },
        })
      }
      actions={
        tg.data ? (
          <Group
            gap="xs"
            style={{
              visibility: tab === "config" ? "visible" : "hidden",
              pointerEvents: tab === "config" ? "auto" : "none",
            }}
          >
            {dirty && (
              <Badge variant="light" color="yellow">
                {t("common.unsaved")}
              </Badge>
            )}
            <Button variant="default" size="sm" disabled={!dirty || save.isPending} onClick={handleDiscard}>
              {t("common.discard")}
            </Button>
            <Button size="sm" disabled={!dirty} loading={save.isPending} onClick={() => void form.onSubmit((v) => save.mutate(v))()}>
              {t("common.save")}
            </Button>
          </Group>
        ) : undefined
      }
      tabs={[
        {
          value: "config",
          label: t("telegram_studio.tab_config"),
          content: (
            <Box mih={640}>
              {tg.isPending ? (
                <Stack gap="md">
                  <Skeleton height={150} radius="md" />
                  <Skeleton height={200} radius="md" />
                  <Skeleton height={130} radius="md" />
                </Stack>
              ) : (
                <form noValidate onSubmit={form.onSubmit((v) => save.mutate(v))}>
                  <Stack gap="md">
                    <FormError error={serverError} />

                    <FormSection title={t("telegram_studio.section_basic")} description={t("telegram_studio.section_basic_desc")}>
                      <Group gap="xs">
                        <Text size="xs" c="dimmed">
                          {t("channels.audience")}
                        </Text>
                        <Badge
                          size="xs"
                          variant="light"
                          color={form.values.audience === "internal" ? "green" : form.values.audience === "partner" ? "brand" : "bright-blue"}
                        >
                          {kit.label("ChannelAudience", form.values.audience)}
                        </Badge>
                      </Group>
                      <TextInput label={t("telegrams.name")} required maxLength={60} {...form.getInputProps("name")} />
                      <Switch
                        label={t("telegrams.enable_groups")}
                        description={t("telegrams.enable_groups_desc")}
                        {...form.getInputProps("enable_groups", { type: "checkbox" })}
                      />
                      <Switch
                        label={t("telegram_studio.enabled")}
                        description={t("telegram_studio.enabled_hint")}
                        {...form.getInputProps("enabled", { type: "checkbox" })}
                      />
                    </FormSection>

                    <FormSection title={t("telegram_studio.section_bot")} description={t("telegram_studio.section_bot_desc")}>
                      <PasswordInput
                        label={t("telegrams.bot_token")}
                        description={t("telegrams.bot_token_update_hint")}
                        autoComplete="off"
                        {...form.getInputProps("bot_token")}
                      />
                      <Stack gap={6}>
                        <Text size="sm" fw={500}>
                          {t("telegrams.mode")}
                        </Text>
                        <SegmentedControl
                          fullWidth
                          value={form.values.gateway_managed ? "gateway" : "direct"}
                          onChange={(v) => form.setFieldValue("gateway_managed", v === "gateway")}
                          data={[
                            { value: "direct", label: t("telegrams.mode_direct") },
                            { value: "gateway", label: t("telegrams.mode_gateway") },
                          ]}
                        />
                        <Text size="xs" c="dimmed">
                          {t(form.values.gateway_managed ? "telegrams.mode_gateway_desc" : "telegrams.mode_direct_desc")}
                        </Text>
                      </Stack>
                    </FormSection>

                    <AiReceptionCard scene={form.values.audience} />

                    <FormSection title={t("telegram_studio.section_routing")} description={t("telegram_studio.section_routing_desc")}>
                      <ResourceSelect
                        model={teams}
                        label={t("telegrams.team_hint")}
                        placeholder={t("telegrams.team_unassigned")}
                        filterRow={(row) => row.status === "active" || String(row.id) === String(form.values.team_id ?? "")}
                        getOption={(row) => ({
                          value: String(row.id),
                          label:
                            row.status === "disabled"
                              ? t("telegrams.team_disabled_suffix", { name: String(row.name ?? "") })
                              : String(row.name ?? ""),
                        })}
                        value={form.values.team_id}
                        onChange={(v) => form.setFieldValue("team_id", v)}
                        error={form.errors.team_id}
                        clearable
                      />
                    </FormSection>
                  </Stack>
                </form>
              )}
            </Box>
          ),
        },
        {
          value: "install",
          label: t("telegram_studio.tab_install"),
          content: (
            <Box mih={640}>
              <TelegramInstallPanel id={id} />
            </Box>
          ),
        },
      ]}
    />
  )
}
