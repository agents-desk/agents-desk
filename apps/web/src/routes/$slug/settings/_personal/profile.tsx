import { createFileRoute } from "@tanstack/react-router"
import { Page, useT } from "@go-tenon/ui"
import { ProfileTabs } from "../../../../profile"

// 个人资料：头像、名字和修改密码。账号是全局的，在哪个工作区中修改都一样。
export const Route = createFileRoute("/$slug/settings/_personal/profile")({
  staticData: { title: "nav.profile", icon: "user", order: 0 },
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
