import type { MemberNotificationSettings } from "@agents-desk/client/api"
import { Alert, Button, Group, Paper, Stack, Switch, Table, Text } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconBell, IconBellOff, IconCheck, IconVolume } from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { applyServerErrors, FormError, notifySuccess, useT } from "@go-tenon/ui"
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

function Form({ initial }: { initial: MemberNotificationSettings }): ReactNode {
  const queryClient = useQueryClient()
  const t = useT()
  const [permission, setPermission] = useState<PermissionState>(() => readBrowserPermission())
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()
  const form = useForm<MemberNotificationSettings>({ initialValues: initial })

  const requestPermission = async () => {
    if (typeof Notification === "undefined") return
    const result = await Notification.requestPermission()
    setPermission(result)
  }

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await api.auth.notificationsUpdate(values)
      await queryClient.invalidateQueries({ queryKey: ["me"] })
      form.resetDirty()
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  const rows: { key: keyof MemberNotificationSettings; title: string; desc: string }[] = [
    { key: "assigned", title: t("notifications_page.assigned_title"), desc: t("notifications_page.assigned_desc") },
    { key: "new_message", title: t("notifications_page.new_message_title"), desc: t("notifications_page.new_message_desc") },
    { key: "mention", title: t("notifications_page.mention_title"), desc: t("notifications_page.mention_desc") },
  ]

  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />
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

        <Paper withBorder radius="md">
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
                        disabled={permission === "unsupported"}
                        {...form.getInputProps(`${row.key}.browser`, { type: "checkbox" })}
                      />
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Group justify="center">
                      <Switch
                        aria-label={`${row.title} - ${t("notifications_page.col_sound")}`}
                        {...form.getInputProps(`${row.key}.sound`, { type: "checkbox" })}
                      />
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Paper>

        <Group justify="space-between">
          <Button variant="default" leftSection={<IconVolume size={15} />} onClick={playAlertSound}>
            {t("notifications_page.test_sound")}
          </Button>
          <Button type="submit" disabled={!form.isDirty()} loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
