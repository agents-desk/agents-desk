import { Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"

// 会话：客户的消息和坐席的回复，在网页插件和会话的 PR 中加入。
export const Route = createFileRoute("/t/$slug/conversations")({
  staticData: { title: "会话", icon: "message", order: 1 },
  component: () => (
    <Page title="会话">
      <Text c="dimmed">这里将显示客户的会话。</Text>
    </Page>
  ),
})
