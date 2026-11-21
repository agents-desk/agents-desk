import { ActionIcon, Button, Code, CopyButton, Group, PasswordInput, Stack, Text, Title, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconCheck, IconCopy } from "@tabler/icons-react"
import { confirm, notifyError, notifySuccess, Page, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { tenantApi } from "../../../../../../api"

// 一个聊天插件的安装：放进网站的嵌入代码，以及产品后端签名已登录用户身份的密钥（可以更换）。
// 权限来自上级的 widgets 布局，菜单中“网页聊天插件”保持选中。
export const Route = createFileRoute("/t/$slug/settings/_channels/widgets/$id")({
  staticData: { title: "widget_install.title" },
  component: WidgetInstall,
})

// Copy 是复制 value 的小按钮，复制后短暂显示对勾。
function Copy({ value }: { value: string }): ReactNode {
  const t = useT()
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={t(copied ? "widget_install.copied" : "widget_install.copy")} withArrow>
          <ActionIcon variant="subtle" color={copied ? "green" : "gray"} onClick={copy} aria-label={t("widget_install.copy")}>
            {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  )
}

function WidgetInstall(): ReactNode {
  const { slug, id } = Route.useParams()
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const widget = useQuery({ queryKey: ["widget", id], queryFn: () => tenantApi.widget.get(id) })
  const install = useQuery({ queryKey: ["widget", id, "installation"], queryFn: () => tenantApi.widget.installation(id) })
  const rotate = useMutation({
    mutationFn: () => tenantApi.widget.rotateSecret(id),
    onSuccess: (data) => {
      queryClient.setQueryData(["widget", id, "installation"], data)
      notifySuccess(t("widget_install.rotated"))
    },
    onError: (err) => notifyError(err),
  })
  const askRotate = async () => {
    if (
      await confirm({
        title: t("widget_install.rotate_confirm"),
        message: t("widget_install.rotate_message"),
        danger: true,
        confirmLabel: t("widget_install.rotate"),
      })
    ) {
      rotate.mutate()
    }
  }

  return (
    <Page
      title={widget.data ? t("widget_install.heading", { name: widget.data.name }) : t("widget_install.title")}
      // 标题左边的返回回到聊天插件列表，而不是离开设置
      back={() => void navigate({ to: "/t/$slug/settings/widgets", params: { slug } })}
    >
      <Stack gap="xl">
        <Stack gap="xs">
          <Group justify="space-between">
            <Title order={4}>{t("widget_install.embed")}</Title>
            {install.data && <Copy value={install.data.embed_code} />}
          </Group>
          <Text size="sm" c="dimmed">
            {t("widget_install.embed_hint")}
          </Text>
          <Code block>{install.data?.embed_code ?? ""}</Code>
        </Stack>

        <Stack gap="xs">
          <Title order={4}>{t("widget_install.identity")}</Title>
          <Text size="sm" c="dimmed">
            {t("widget_install.identity_hint")}
          </Text>
          <Group align="flex-end" gap="xs">
            <PasswordInput label={t("widget_install.secret")} value={install.data?.identity_secret ?? ""} readOnly style={{ flex: 1 }} />
            {install.data && <Copy value={install.data.identity_secret} />}
          </Group>
          <Code block>{t("widget_install.claims_example")}</Code>
          <Group justify="flex-end">
            <Button variant="light" color="red" onClick={() => void askRotate()} loading={rotate.isPending}>
              {t("widget_install.rotate")}
            </Button>
          </Group>
        </Stack>
      </Stack>
    </Page>
  )
}
