import { ActionIcon, Box, Button, Code, CopyButton, Group, PasswordInput, Stack, Table, Tabs, Text, TextInput, Title, Tooltip } from "@mantine/core"
import { IconCheck, IconCopy } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { confirm, notifyError, notifySuccess, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { tenantApi } from "../../api"

/** Copy 是复制 value 的小按钮，复制后短暂显示对勾；alignInput 时与 sm 输入框等高对齐。 */
function Copy({ value, alignInput = false }: { value: string; alignInput?: boolean }): ReactNode {
  const t = useT()
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={t(copied ? "widget_install.copied" : "widget_install.copy")} withArrow>
          <ActionIcon
            variant="subtle"
            size={alignInput ? "input-sm" : undefined}
            color={copied ? "green" : "gray"}
            onClick={copy}
            aria-label={t("widget_install.copy")}
          >
            {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  )
}

/** iosExample 是 iOS（Swift、WKWebView）打开聊天窗口的示例。 */
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

/** androidExample 是 Android（Kotlin、WebView）打开聊天窗口的示例。 */
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

/** bridgeMessages 是聊天窗口发给 App 的消息说明。 */
const bridgeMessages = [
  { type: "ready", when: "widget_install.app_msg_ready" },
  { type: "close", when: "widget_install.app_msg_close" },
  { type: "unread {count}", when: "widget_install.app_msg_unread" },
  { type: "open_url {url}", when: "widget_install.app_msg_open_url" },
  { type: "need_token", when: "widget_install.app_msg_need_token" },
] as const

/** AppSetup 是“App 接入”一节：App 用 WebView 全屏打开的地址与桥接示例。 */
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
        <Copy value={appURL} alignInput />
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

/** WidgetInstallPanel 是聊天插件的“安装与接入”页签：网页嵌入代码、App 接入与身份验证密钥管理。 */
export function WidgetInstallPanel({ id }: { id: string }): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
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
          {install.data && <Copy value={install.data.identity_secret} alignInput />}
        </Group>
        <Code block>{t("widget_install.claims_example")}</Code>
        <Group justify="flex-end">
          <Button variant="light" color="red" onClick={() => void askRotate()} loading={rotate.isPending}>
            {t("widget_install.rotate")}
          </Button>
        </Group>
      </Stack>
    </Stack>
  )
}
