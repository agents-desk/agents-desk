import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"
import { ProfileTabs } from "../../profile"

// 个人资料：从头像菜单进入，不出现在侧栏菜单中。
export const Route = createFileRoute("/_app/profile")({
  staticData: { title: "个人资料", menu: false },
  component: () => (
    <Page title="个人资料">
      <ProfileTabs />
    </Page>
  ),
})
