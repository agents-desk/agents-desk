import { Button, Group, Select, Stack, Switch } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { applyServerErrors, FormError, inTimeZone, notifySuccess, useT } from "@go-tenon/ui"
import type { MemberSendShortcut, MemberUiLanguage } from "@agents-desk/client/api"
import { type ReactNode, useMemo, useState } from "react"
import { api, apiKit, kit } from "./api"
import { me } from "./auth"
import { i18n } from "./i18n"
import { LanguageSelect } from "./LanguageSelect"
import { allZones, browserZone, offsetOf } from "./timezone"
import { useUnsavedGuard } from "./components/unsaved-guard"

// 偏好设置：界面语言、沟通与译文语言、时区、发送方式和发送后自动打开下一个待回复，属于账号本身，在所有工作区中一样。

/** 偏好设置的表单。 */
export function PreferencesForm(): ReactNode {
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  if (!profile.data) return null
  return (
    <Form
      uiLanguage={(profile.data.ui_language as MemberUiLanguage) || "zh-CN"}
      language={profile.data.language}
      timeZone={profile.data.time_zone ?? ""}
      sendShortcut={profile.data.send_shortcut ?? "enter"}
      openNextAfterReply={profile.data.open_next_after_reply ?? false}
    />
  )
}

function Form({
  uiLanguage,
  language,
  timeZone,
  sendShortcut,
  openNextAfterReply,
}: {
  uiLanguage: MemberUiLanguage
  language: string
  timeZone: string
  sendShortcut: MemberSendShortcut
  openNextAfterReply: boolean
}): ReactNode {
  const queryClient = useQueryClient()
  const t = useT()
  const form = useForm({
    initialValues: {
      ui_language: uiLanguage,
      language,
      time_zone: timeZone,
      send_shortcut: sendShortcut,
      open_next_after_reply: openNextAfterReply,
    },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  // 界面语言和发送方式的显示文字来自服务端枚举
  const uiLanguages = apiKit.enums.MemberUiLanguage.values.map((v) => ({ value: v, label: apiKit.label("MemberUiLanguage", v) }))
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
        ui_language: values.ui_language,
        language: values.language,
        time_zone: values.time_zone || undefined,
        send_shortcut: values.send_shortcut,
        open_next_after_reply: values.open_next_after_reply,
      })
      // 界面语言与沟通译文语言解耦：界面按 ui_language 切换，沟通数据按 language 刷新译文
      i18n.setLanguage(values.ui_language)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["me"] }),
        queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
        queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() }),
        queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() }),
        queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() }),
        queryClient.invalidateQueries({ queryKey: kit.queries.translation.key() }),
      ])
      form.resetDirty()
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  useUnsavedGuard(form.isDirty())
  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />
        <Select
          label={t("preferences.ui_language")}
          description={t("preferences.ui_language_hint")}
          data={uiLanguages}
          allowDeselect={false}
          {...form.getInputProps("ui_language")}
        />
        <LanguageSelect
          label={t("preferences.language")}
          description={t("preferences.language_hint")}
          allowDeselect={false}
          includeWorkspace={false}
          value={form.values.language}
          error={form.errors.language}
          onChange={(v) => v && form.setFieldValue("language", v)}
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
