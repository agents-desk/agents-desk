import { Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"

// 工作区设置：成员、团队、角色等，在后续的 PR 中加入。
export const Route = createFileRoute("/t/$slug/settings")({
  staticData: { title: "设置", icon: "settings", order: 9 },
  component: () => (
    <Page title="设置">
      <Text c="dimmed">这里将管理工作区的成员、团队和角色。</Text>
    </Page>
  ),
})
