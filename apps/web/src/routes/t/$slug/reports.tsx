import { Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page, useT } from "@go-tenon/ui"

// 报表：会话、响应时间、满意度、成员、翻译，每张报表是二级导航中的一项。
// 现在是占位页，报表在 R1 中完成，访问权限随 R1 的接口加上。
export const Route = createFileRoute("/t/$slug/reports")({
  staticData: { title: "nav.reports", icon: "chart", order: 3 },
  component: Reports,
})

function Reports() {
  const t = useT()
  return (
    <Page title={t("nav.reports")}>
      <Text c="dimmed">{t("reports.placeholder")}</Text>
    </Page>
  )
}
