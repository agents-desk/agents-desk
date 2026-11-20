import { Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"

// 报表：会话、响应时间、满意度、成员、翻译，每张报表是二级导航中的一项。
// 现在是占位页，报表在 R1 中完成，访问权限随 R1 的接口加上。
export const Route = createFileRoute("/t/$slug/reports")({
  staticData: { title: "报表", icon: "chart", order: 3 },
  component: () => (
    <Page title="报表">
      <Text c="dimmed">各张报表在基础报表的开发（R1）中加入。</Text>
    </Page>
  ),
})
