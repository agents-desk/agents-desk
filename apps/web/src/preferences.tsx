import { Button, Group, Select, Stack, Switch } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { applyServerErrors, FormError, inTimeZone, notifySuccess, useT } from "@go-tenon/ui"
import type { MemberLanguage, MemberSendShortcut } from "@agents-desk/client/api"
import { type ReactNode, useMemo, useState } from "react"
import { api, apiKit, kit } from "./api"
import { me } from "./auth"
import { i18n } from "./i18n"
import { allZones, browserZone, offsetOf } from "./timezone"

// 偏好设置：语言、时区、发送方式和发送后自动打开下一个待回复，属于账号本身，在所有工作区中一样。

/** 偏好设置的表单。 */
export function PreferencesForm(): ReactNode {
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  if (!profile.data) return null
  return (
    <Form
      language={profile.data.language}
      timeZone={profile.data.time_zone ?? ""}
      sendShortcut={profile.data.send_shortcut ?? "enter"}
      openNextAfterReply={profile.data.open_next_after_reply ?? false}
    />
  )
}

function Form({
  language,
  timeZone,
  sendShortcut,
  openNextAfterReply,
}: {
  language: MemberLanguage
  timeZone: string
  sendShortcut: MemberSendShortcut
  openNextAfterReply: boolean
}): ReactNode {
  const queryClient = useQueryClient()
  const t = useT()
  const form = useForm({
    initialValues: {
      language,
      time_zone: timeZone,
      send_shortcut: sendShortcut,
      open_next_after_reply: openNextAfterReply,
    },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  // 语言和发送方式的显示文字来自服务端枚举
  const languages = apiKit.enums.MemberLanguage.values.map((v) => ({ value: v, label: apiKit.label("MemberLanguage", v) }))
  const shortcuts = apiKit.enums.MemberSendShortcut.values.map((v) => ({ value: v, label: apiKit.label("MemberSendShortcut", v) }))
  // 第一项是跟随浏览器；其余是浏览器支持的全部时区，带现在的 UTC 偏移。
  const zones = useMemo(
    () => [
      { value: "", label: t("preferences.follow_browser", { zone: browserZone }) },
      ...allZones().map((z) => ({ value: z, label: `${z} (${offsetOf(z)})` })),
    ],
    [t],
  )

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await api.auth.preferencesUpdate({
        language: values.language,
        time_zone: values.time_zone || undefined,
        send_shortcut: values.send_shortcut,
        open_next_after_reply: values.open_next_after_reply,
      })
      // 界面换成新的语言；重新读取 /me 和会话缓存：时区、译文预览和 untranslated 标记随之按新语言刷新
      i18n.setLanguage(values.language)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["me"] }),
        queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
      ])
      form.resetDirty()
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />
        <Select
          label={t("preferences.language")}
          description={t("preferences.language_hint")}
          data={languages}
          allowDeselect={false}
          {...form.getInputProps("language")}
        />
        <Select
          label={t("preferences.time_zone")}
          description={t("preferences.time_zone_hint", {
            now: inTimeZone(new Date(), form.values.time_zone || undefined).format("YYYY-MM-DD HH:mm"),
          })}
          data={zones}
          searchable
          allowDeselect={false}
          nothingFoundMessage={t("preferences.no_time_zone")}
          {...form.getInputProps("time_zone")}
        />
        <Select
          label={t("preferences.send_shortcut")}
          description={t("preferences.send_shortcut_hint")}
          data={shortcuts}
          allowDeselect={false}
          {...form.getInputProps("send_shortcut")}
        />
        <Switch
          label={t("preferences.open_next_after_reply")}
          description={t("preferences.open_next_after_reply_hint")}
          {...form.getInputProps("open_next_after_reply", { type: "checkbox" })}
        />
        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty()} loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
