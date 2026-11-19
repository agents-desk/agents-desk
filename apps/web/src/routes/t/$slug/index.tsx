import { Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"

// 工作台：我的工单、待领取、@我，在 PR 5 中加入。
export const Route = createFileRoute("/t/$slug/")({
  staticData: { title: "工作台", icon: "home", order: 0 },
  component: () => (
    <Page title="工作台">
      <Text c="dimmed">这里将显示你的工单和待处理的事项。</Text>
    </Page>
  ),
})
