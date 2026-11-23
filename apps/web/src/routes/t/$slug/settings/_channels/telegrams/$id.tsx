import { ActionIcon, Badge, Button, Code, CopyButton, Group, PasswordInput, Stack, Table, Text, TextInput, Title, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconCheck, IconCopy } from "@tabler/icons-react"
import { confirm, notifyError, notifySuccess, Page, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { tenantApi } from "../../../../../../api"

// 单个 Telegram 机器人的接入配置页：展示渠道 ID、接入模式、Webhook 地址、Webhook 密钥（支持更换），以及外部网关透传身份头说明。
export const Route = createFileRoute("/t/$slug/settings/_channels/telegrams/$id")({
  staticData: { title: "telegram_install.title" },
  component: TelegramInstall,
})

// Copy 复制 value 并在按钮上短暂显示对勾。
function Copy({ value }: { value: string }): ReactNode {
  const t = useT()
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={t(copied ? "telegram_install.copied" : "telegram_install.copy")} withArrow>
          <ActionIcon variant="subtle" color={copied ? "green" : "gray"} onClick={copy} aria-label={t("telegram_install.copy")}>
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

function TelegramInstall(): ReactNode {
  const { slug, id } = Route.useParams()
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const tg = useQuery({ queryKey: ["telegram", id], queryFn: () => tenantApi.telegram.get(id) })
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

  return (
    <Page
      title={tg.data ? t("telegram_install.heading", { name: tg.data.name }) : t("telegram_install.title")}
      back={() => void navigate({ to: "/t/$slug/settings/telegrams", params: { slug } })}
    >
      <Stack gap="xl">
        <Stack gap="xs">
          <Group justify="space-between">
            <Title order={4}>{t("telegram_install.webhook")}</Title>
            {install.data && (
              <Badge variant="light" color={install.data.gateway_managed ? "bright-blue" : "gray"}>
                {t(install.data.gateway_managed ? "telegrams.mode_gateway" : "telegrams.mode_direct")}
              </Badge>
            )}
          </Group>
          {install.data && (
            <Text size="sm" c="dimmed">
              {t(install.data.gateway_managed ? "telegram_install.mode_gateway_hint" : "telegram_install.mode_direct_hint")}
            </Text>
          )}
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
        </Stack>

        <Stack gap="xs">
          <Title order={4}>{t("telegram_install.gateway_headers")}</Title>
          <Text size="sm" c="dimmed">
            {t("telegram_install.gateway_headers_hint")}
          </Text>
          <Table withTableBorder>
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
        </Stack>
      </Stack>
    </Page>
  )
}
