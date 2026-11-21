import { Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page, useT } from "@go-tenon/ui"

// 联系人：用户和线索的列表、联系人的资料。二级导航是全部、用户、线索和保存的筛选。
// 现在是占位页，列表和资料在 U1 中完成，访问权限随 U1 的接口加上。
export const Route = createFileRoute("/t/$slug/contacts")({
  staticData: { title: "nav.contacts", icon: "contacts", order: 2 },
  component: Contacts,
})

function Contacts() {
  const t = useT()
  return (
    <Page title={t("nav.contacts")}>
      <Text c="dimmed">{t("contacts.placeholder")}</Text>
    </Page>
  )
}
