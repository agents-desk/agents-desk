import { createFileRoute } from "@tanstack/react-router"
import { Page } from "@go-tenon/ui"
import { PreferencesForm } from "../../../../../preferences"

// 偏好设置：语言和时区。账号是全局的，在哪个工作区中修改都一样。
export const Route = createFileRoute("/t/$slug/settings/_personal/preferences")({
  staticData: { title: "偏好设置", icon: "settings", order: 1 },
  component: () => (
    <Page title="偏好设置">
      <PreferencesForm />
    </Page>
  ),
})
