import type { WecomBotDTO } from "@agents-desk/client/tenant"
import {
  Alert,
  Badge,
  Box,
  Button,
  Group,
  Paper,
  PasswordInput,
  Skeleton,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconAlertCircle, IconRefresh } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { applyServerErrors, FormError, notifyError, notifySuccess, Page, ResourceSelect, StatusDot, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../../api"
import { AiReceptionCard } from "../../components/ai-placeholders"
import { useUnsavedGuard } from "../../components/unsaved-guard"
import { fullDateTime } from "../../inbox/format"
import { teams } from "../../resources"

/** SectionCard 是企业微信机器人工作台的分组卡片。 */
function SectionCard({ title, description, children }: { title: string; description: string; children: ReactNode }): ReactNode {
  return (
    <Paper withBorder p="md" radius="md">
      <Stack gap="sm">
        <Box>
          <Title order={5}>{title}</Title>
          <Text size="xs" c="dimmed" mt={2}>
            {description}
          </Text>
        </Box>
        {children}
      </Stack>
    </Paper>
  )
}

/** wecomStatusColor 返回企微机器人长连接状态对应的语义色。 */
export function wecomStatusColor(status: WecomBotDTO["status"]): string {
  switch (status) {
    case "connected":
      return "green"
    case "connecting":
      return "bright-blue"
    case "auth_failed":
      return "red"
    default:
      return "gray"
  }
}

interface WeComFormValues {
  name: string
  bot_id: string
  secret: string
  ws_url: string
  welcome_text: string
  team_id: string | null
  enabled: boolean
}

function toFormValues(bot: WecomBotDTO): WeComFormValues {
  return {
    name: bot.name,
    bot_id: bot.bot_id,
    secret: "",
    ws_url: bot.ws_url ?? "",
    welcome_text: bot.welcome_text ?? "",
    team_id: bot.team_id ?? null,
    enabled: bot.enabled,
  }
}

/**
 * WeComStudio 是企业微信智能机器人（WebSocket 长连接模式）的配置工作台：
 * 展示实时连接状态与一键重连，配置基础信息、服务对象、长连接凭据、欢迎语与默认团队收件箱。
 */
export function WeComStudio({ slug, id }: { slug: string; id: string }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [serverError, setServerError] = useState<ReturnType<typeof applyServerErrors>>()

  const bot = useQuery({
    queryKey: ["wecom", id],
    queryFn: () => tenantApi.wecom.get(id),
    refetchInterval: 10_000,
  })

  const form = useForm<WeComFormValues>({
    initialValues: {
      name: "",
      bot_id: "",
      secret: "",
      ws_url: "",
      welcome_text: "",
      team_id: null,
      enabled: true,
    },
  })

  useEffect(() => {
    if (!bot.data) return
    form.initialize(toFormValues(bot.data))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅在服务端数据到达或切换渠道时重置基准
  }, [bot.data?.id])

  const save = useMutation({
    mutationFn: (v: WeComFormValues) =>
      tenantApi.wecom.update(id, {
        name: v.name.trim(),
        bot_id: v.bot_id.trim(),
        secret: v.secret.trim() || undefined,
        ws_url: v.ws_url.trim() || undefined,
        welcome_text: v.welcome_text.trim() || undefined,
        team_id: v.team_id || undefined,
        enabled: v.enabled,
      }),
    onSuccess: (updated) => {
      setServerError(undefined)
      queryClient.setQueryData(["wecom", id], updated)
      void queryClient.invalidateQueries({ queryKey: ["resource", "wecom"] })
      const next = toFormValues(updated)
      form.setValues(next)
      form.resetDirty(next)
      notifySuccess(t("wecom_studio.saved"))
    },
    onError: (err) => setServerError(applyServerErrors(form, err)),
  })

  const reconnect = useMutation({
    mutationFn: () => tenantApi.wecom.reconnect(id),
    onSuccess: (updated) => {
      queryClient.setQueryData(["wecom", id], updated)
      void queryClient.invalidateQueries({ queryKey: ["resource", "wecom"] })
      notifySuccess(t("wecom_studio.reconnecting"))
    },
    onError: (err) => notifyError(err),
  })

  const dirty = form.isDirty()
  useUnsavedGuard(dirty)

  const handleDiscard = () => {
    if (!bot.data) return
    setServerError(undefined)
    form.clearErrors()
    const base = toFormValues(bot.data)
    form.setValues(base)
    form.resetDirty(base)
  }

  return (
    <Page
      title={bot.data ? bot.data.name : t("wecom_studio.title")}
      back={() => void navigate({ to: "/$slug/settings/internal-channels", params: { slug }, search: { tab: "wecom" } })}
      actions={
        bot.data ? (
          <Group gap="xs">
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
    >
      <Box mih={640}>
        {bot.isPending ? (
          <Stack gap="md">
            <Skeleton height={110} radius="md" />
            <Skeleton height={220} radius="md" />
            <Skeleton height={180} radius="md" />
          </Stack>
        ) : (
          <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
            <Stack gap="md">
              <FormError error={serverError} />

              {bot.data && (
                <Paper withBorder p="md" radius="md">
                  <Group justify="space-between" wrap="wrap" gap="sm">
                    <Stack gap={4}>
                      <Group gap="xs">
                        <StatusDot color={wecomStatusColor(bot.data.status)}>{kit.label("WecomStatus", bot.data.status)}</StatusDot>
                        <Badge variant="light" color="gray" size="xs">
                          {t("wecom_studio.transport_ws")}
                        </Badge>
                      </Group>
                      {bot.data.last_connected_at && (
                        <Text size="xs" c="dimmed">
                          {t("wecom_studio.last_connected", { time: fullDateTime(bot.data.last_connected_at, ui, zone) })}
                        </Text>
                      )}
                    </Stack>
                    <Button
                      size="sm"
                      variant="light"
                      leftSection={<IconRefresh size={14} />}
                      loading={reconnect.isPending}
                      onClick={() => reconnect.mutate()}
                    >
                      {t("wecom_studio.reconnect")}
                    </Button>
                  </Group>
                  {bot.data.last_error && (
                    <Alert mt="sm" color="red" variant="light" icon={<IconAlertCircle size={16} />}>
                      <Text size="xs">{bot.data.last_error}</Text>
                    </Alert>
                  )}
                </Paper>
              )}

              <SectionCard title={t("wecom_studio.section_basic")} description={t("wecom_studio.section_basic_desc")}>
                <Group gap="xs">
                  <Text size="xs" c="dimmed">
                    {t("channels.audience")}
                  </Text>
                  <Badge size="xs" variant="light" color="green">
                    {kit.label("ChannelAudience", "internal")}
                  </Badge>
                </Group>
                <TextInput label={t("wecom.name")} required maxLength={60} {...form.getInputProps("name")} />
                <Switch
                  label={t("wecom_studio.enabled")}
                  description={t("wecom_studio.enabled_hint")}
                  {...form.getInputProps("enabled", { type: "checkbox" })}
                />
              </SectionCard>

              <SectionCard title={t("wecom_studio.section_conn")} description={t("wecom_studio.section_conn_desc")}>
                <TextInput label={t("wecom.bot_id")} required maxLength={128} {...form.getInputProps("bot_id")} />
                <PasswordInput
                  label={t("wecom.secret")}
                  description={t("wecom.secret_update_hint")}
                  autoComplete="off"
                  {...form.getInputProps("secret")}
                />
                <TextInput
                  label={t("wecom.ws_url")}
                  description={t("wecom.ws_url_hint")}
                  placeholder="wss://openws.work.weixin.qq.com"
                  {...form.getInputProps("ws_url")}
                />
              </SectionCard>

              <AiReceptionCard scene="internal" />

              <SectionCard title={t("wecom_studio.section_routing")} description={t("wecom_studio.section_routing_desc")}>
                <ResourceSelect
                  model={teams}
                  label={t("wecom.team_hint")}
                  placeholder={t("wecom.team_unassigned")}
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
                <Textarea
                  label={t("wecom.welcome_text")}
                  description={t("wecom.welcome_text_hint")}
                  minRows={2}
                  maxRows={5}
                  autosize
                  maxLength={1000}
                  {...form.getInputProps("welcome_text")}
                />
              </SectionCard>
            </Stack>
          </form>
        )}
      </Box>
    </Page>
  )
}
