import type { MemberNotificationSettings } from "@agents-desk/client/api"
import { Alert, Button, Group, Stack, Switch, Table, Text } from "@mantine/core"
import { IconBell, IconBellOff, IconCheck, IconVolume } from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { notifyError, notifySuccess, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { api } from "./api"
import { me } from "./auth"
import { playAlertSound } from "./inbox/alerts"

type PermissionState = NotificationPermission | "unsupported"

function readBrowserPermission(): PermissionState {
  if (typeof window === "undefined" || typeof Notification === "undefined") return "unsupported"
  return Notification.permission
}

/** NotificationsForm 是个人设置中的通知偏好表单：浏览器通知权限引导、三类事件的桌面通知与提示音开关及试听。 */
export function NotificationsForm(): ReactNode {
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  if (!profile.data) return null
  return <Form initial={profile.data.notifications} />
}

/**
 * Form 是通知偏好：每个开关切换后立即保存，失败时恢复原值并提示错误。
 */
function Form({ initial }: { initial: MemberNotificationSettings }): ReactNode {
  const queryClient = useQueryClient()
  const t = useT()
  const [permission, setPermission] = useState<PermissionState>(() => readBrowserPermission())
  const [values, setValues] = useState<MemberNotificationSettings>(initial)
  const [pending, setPending] = useState<string>()

  const requestPermission = async () => {
    if (typeof Notification === "undefined") return
    const result = await Notification.requestPermission()
    setPermission(result)
  }

  // toggle 改一个开关并立即保存整份通知偏好
  const toggle = async (key: keyof MemberNotificationSettings, channel: "browser" | "sound", checked: boolean) => {
    const prev = values
    const next = { ...values, [key]: { ...values[key], [channel]: checked } }
    setValues(next)
    setPending(`${key}.${channel}`)
    try {
      await api.auth.notificationsUpdate(next)
      await queryClient.invalidateQueries({ queryKey: ["me"] })
      notifySuccess(t("common.saved"))
    } catch (err) {
      setValues(prev)
      notifyError(err)
    } finally {
      setPending(undefined)
    }
  }

  const rows: { key: keyof MemberNotificationSettings; title: string; desc: string }[] = [
    { key: "assigned", title: t("notifications_page.assigned_title"), desc: t("notifications_page.assigned_desc") },
    { key: "new_message", title: t("notifications_page.new_message_title"), desc: t("notifications_page.new_message_desc") },
    { key: "mention", title: t("notifications_page.mention_title"), desc: t("notifications_page.mention_desc") },
  ]

  return (
    <Stack gap="md">
      {permission === "granted" && (
        <Alert color="green" variant="light" icon={<IconCheck size={16} />}>
          {t("notifications_page.permission_granted")}
        </Alert>
      )}
      {permission === "default" && (
        <Alert color="bright-blue" variant="light" icon={<IconBell size={16} />}>
          <Group justify="space-between" align="center" gap="sm" wrap="wrap">
            <Text size="sm">{t("notifications_page.permission_default")}</Text>
            <Button size="xs" variant="light" onClick={() => void requestPermission()}>
              {t("notifications_page.request_permission")}
            </Button>
          </Group>
        </Alert>
      )}
      {permission === "denied" && (
        <Alert color="yellow" variant="light" icon={<IconBellOff size={16} />}>
          {t("notifications_page.permission_denied")}
        </Alert>
      )}
      {permission === "unsupported" && (
        <Alert color="gray" variant="light" icon={<IconBellOff size={16} />}>
          {t("notifications_page.permission_unsupported")}
        </Alert>
      )}

      <Table.ScrollContainer minWidth={520} className="desk-table-card" type="native">
        <Table verticalSpacing="sm" horizontalSpacing="md">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("notifications_page.col_event")}</Table.Th>
              <Table.Th w={130} ta="center">
                {t("notifications_page.col_browser")}
              </Table.Th>
              <Table.Th w={110} ta="center">
                {t("notifications_page.col_sound")}
              </Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((row) => (
              <Table.Tr key={row.key}>
                <Table.Td>
                  <Text size="sm" fw={500}>
                    {row.title}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {row.desc}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Group justify="center">
                    <Switch
                      aria-label={`${row.title} - ${t("notifications_page.col_browser")}`}
                      disabled={permission === "unsupported" || pending === `${row.key}.browser`}
                      checked={values[row.key].browser}
                      onChange={(e) => void toggle(row.key, "browser", e.currentTarget.checked)}
                    />
                  </Group>
                </Table.Td>
                <Table.Td>
                  <Group justify="center">
                    <Switch
                      aria-label={`${row.title} - ${t("notifications_page.col_sound")}`}
                      disabled={pending === `${row.key}.sound`}
                      checked={values[row.key].sound}
                      onChange={(e) => void toggle(row.key, "sound", e.currentTarget.checked)}
                    />
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>

      <Group>
        <Button variant="default" leftSection={<IconVolume size={16} />} onClick={playAlertSound}>
          {t("notifications_page.test_sound")}
        </Button>
      </Group>
    </Stack>
  )
}
