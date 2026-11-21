import { createFileRoute } from "@tanstack/react-router"
import { Page, useT } from "@go-tenon/ui"
import { ProfileTabs } from "../../profile"

// 个人资料：从头像菜单进入，不出现在侧栏菜单中。
export const Route = createFileRoute("/_app/profile")({
  staticData: { title: "nav.profile", menu: false },
  component: Profile,
})

function Profile() {
  const t = useT()
  return (
    <Page title={t("nav.profile")}>
      <ProfileTabs />
    </Page>
  )
}
