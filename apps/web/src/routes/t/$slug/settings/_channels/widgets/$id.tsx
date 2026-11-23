import { ActionIcon, Box, Button, Code, CopyButton, Group, PasswordInput, Stack, Table, Tabs, Text, TextInput, Title, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconCheck, IconCopy } from "@tabler/icons-react"
import { confirm, notifyError, notifySuccess, Page, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { tenantApi } from "../../../../../../api"

// 一个聊天插件的安装：放进网站的嵌入代码，App 的接入方法，以及产品后端签名已登录用户身份的密钥（可以更换）。
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

        {install.data && <AppSetup appURL={install.data.app_url} />}

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

// iosExample 是 iOS（Swift、WKWebView）打开聊天窗口的示例：页面加载前传入用户身份，接收聊天窗口的消息。
const iosExample = (appURL: string) => `let config = WKWebViewConfiguration()
// Pass the signed-in user's identity before the page loads (omit for guests)
let identity = "window.agentsDesk = { user_token: \\"\\(userToken)\\" };"
config.userContentController.addUserScript(
  WKUserScript(source: identity, injectionTime: .atDocumentStart, forMainFrameOnly: true))
// Receive close / unread / open_url from the chat window
config.userContentController.add(self, name: "agentsDesk")
let webView = WKWebView(frame: .zero, configuration: config)
webView.load(URLRequest(url: URL(string: "${appURL}")!))

func userContentController(_ c: WKUserContentController, didReceive message: WKScriptMessage) {
  guard let body = message.body as? [String: Any], let type = body["type"] as? String else { return }
  switch type {
  case "close": dismiss(animated: true)
  case "unread": updateBadge(body["count"] as? Int ?? 0)
  case "open_url":
    if let s = body["url"] as? String, let url = URL(string: s) { UIApplication.shared.open(url) }
  default: break
  }
}`

// androidExample 是 Android（Kotlin、WebView）打开聊天窗口的示例。
const androidExample = (appURL: string) => `webView.settings.javaScriptEnabled = true
webView.settings.domStorageEnabled = true
// Pass the signed-in user's identity before the page loads (omit for guests)
if (WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
  WebViewCompat.addDocumentStartJavaScript(
    webView, "window.agentsDesk = { user_token: \\"$userToken\\" };", setOf("${new URL(appURL).origin}"))
}
// Receive close / unread / open_url from the chat window
webView.addJavascriptInterface(object {
  @JavascriptInterface
  fun postMessage(json: String) {
    val msg = JSONObject(json)
    when (msg.getString("type")) {
      "close" -> runOnUiThread { finish() }
      "unread" -> runOnUiThread { updateBadge(msg.getInt("count")) }
      "open_url" -> startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(msg.getString("url"))))
    }
  }
}, "AgentsDeskBridge")
webView.loadUrl("${appURL}")`

/** bridgeMessages 是聊天窗口发给 App 的消息。 */
const bridgeMessages = [
  { type: "ready", when: "widget_install.app_msg_ready" },
  { type: "close", when: "widget_install.app_msg_close" },
  { type: "unread {count}", when: "widget_install.app_msg_unread" },
  { type: "open_url {url}", when: "widget_install.app_msg_open_url" },
  { type: "need_token", when: "widget_install.app_msg_need_token" },
] as const

/**
 * AppSetup 是“App 接入”一节：App 用 WebView 全屏打开的地址，页面加载前传入用户身份、接收聊天窗口消息的示例代码，
 * 以及聊天窗口发给 App 的消息。
 */
function AppSetup({ appURL }: { appURL: string }): ReactNode {
  const t = useT()
  return (
    <Stack gap="xs">
      <Title order={4}>{t("widget_install.app")}</Title>
      <Text size="sm" c="dimmed">
        {t("widget_install.app_hint")}
      </Text>
      <Group align="flex-end" gap="xs">
        <TextInput label={t("widget_install.app_url")} value={appURL} readOnly style={{ flex: 1 }} />
        <Copy value={appURL} />
      </Group>
      <Tabs defaultValue="ios" keepMounted={false}>
        <Tabs.List>
          <Tabs.Tab value="ios">iOS (Swift)</Tabs.Tab>
          <Tabs.Tab value="android">Android (Kotlin)</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="ios" pt="xs">
          <Box pos="relative">
            <Box pos="absolute" top={8} right={8} style={{ zIndex: 1 }}>
              <Copy value={iosExample(appURL)} />
            </Box>
            <Code block>{iosExample(appURL)}</Code>
          </Box>
        </Tabs.Panel>
        <Tabs.Panel value="android" pt="xs">
          <Box pos="relative">
            <Box pos="absolute" top={8} right={8} style={{ zIndex: 1 }}>
              <Copy value={androidExample(appURL)} />
            </Box>
            <Code block>{androidExample(appURL)}</Code>
          </Box>
        </Tabs.Panel>
      </Tabs>
      <Table withTableBorder>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t("widget_install.app_msg")}</Table.Th>
            <Table.Th>{t("widget_install.app_msg_when")}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {bridgeMessages.map((m) => (
            <Table.Tr key={m.type}>
              <Table.Td>
                <Code>{m.type}</Code>
              </Table.Td>
              <Table.Td>{t(m.when)}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Stack>
  )
}
