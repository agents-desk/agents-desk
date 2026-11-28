import { Stack, Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { Page, useT } from "@go-tenon/ui"
import { NotificationsForm } from "../../../../notifications"
import { PreferencesForm } from "../../../../preferences"
import { BasicProfileForm, PasswordForm } from "../../../../profile"

// 个人设置：基本资料、修改密码、偏好设置与通知合并为页内标签，属于账号全局设置。
export const Route = createFileRoute("/$slug/settings/_personal/profile")({
  staticData: { title: "nav.account", icon: "user", order: 0 },
  component: AccountSettings,
})

function AccountSettings() {
  const t = useT()
  return (
    <Page
      title={t("nav.account")}
      tabs={[
        { value: "basic", label: t("profile.tab_basic"), content: <BasicProfileForm /> },
        { value: "password", label: t("profile.tab_password"), content: <PasswordForm /> },
        { value: "preferences", label: t("nav.preferences"), content: <PreferencesForm /> },
        {
          value: "notifications",
          label: t("nav.notifications"),
          content: (
            <Stack gap="md">
              <Text size="sm" c="dimmed">
                {t("notifications_page.description")}
              </Text>
              <NotificationsForm />
            </Stack>
          ),
        },
      ]}
    />
  )
}
