import { Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"

// 常规设置：工作区的名称等，在后续的 PR 中加入。
export const Route = createFileRoute("/t/$slug/settings/general")({
  staticData: { title: "常规", icon: "settings", order: 0 },
  component: () => (
    <Page title="常规">
      <Text c="dimmed">这里将管理工作区的常规设置；成员、团队和角色在后续的 PR 中加入。</Text>
    </Page>
  ),
})
