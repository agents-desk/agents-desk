import { Button, Group, Select, Stack } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { applyServerErrors, FormError, inTimeZone, notifySuccess, useT } from "@go-tenon/ui"
import type { MemberLanguage } from "@agents-desk/client/api"
import { type ReactNode, useMemo, useState } from "react"
import { api, apiKit, kit } from "./api"
import { i18n } from "./i18n"
import { me } from "./auth"

// 偏好设置：语言和时区，属于账号本身，在所有工作区中一样。按表单的约定：宽度不限，一个输入框一行，保存按钮在右侧。

/**
 * 浏览器（例如 Chrome）的时区列表中仍用旧名字的时区，换成 IANA 现在的名字：用户按现在的名字搜索（例如 Kolkata）也能找到，
 * 保存的也是现在的名字。服务端两种名字都认识。
 */
const renamed: Record<string, string> = {
  "Asia/Calcutta": "Asia/Kolkata",
  "Asia/Saigon": "Asia/Ho_Chi_Minh",
  "Asia/Katmandu": "Asia/Kathmandu",
  "Asia/Rangoon": "Asia/Yangon",
  "Europe/Kiev": "Europe/Kyiv",
  "Africa/Asmera": "Africa/Asmara",
  "Atlantic/Faeroe": "Atlantic/Faroe",
  "America/Godthab": "America/Nuuk",
  "America/Buenos_Aires": "America/Argentina/Buenos_Aires",
  "America/Catamarca": "America/Argentina/Catamarca",
  "America/Cordoba": "America/Argentina/Cordoba",
  "America/Jujuy": "America/Argentina/Jujuy",
  "America/Mendoza": "America/Argentina/Mendoza",
  "America/Indianapolis": "America/Indiana/Indianapolis",
  "America/Louisville": "America/Kentucky/Louisville",
  "Pacific/Truk": "Pacific/Chuuk",
  "Pacific/Ponape": "Pacific/Pohnpei",
  "Pacific/Enderbury": "Pacific/Kanton",
}

/** 浏览器所在的时区，例如 Asia/Shanghai。 */
const browserZone = (() => {
  const z = Intl.DateTimeFormat().resolvedOptions().timeZone
  return renamed[z] ?? z
})()

/** 浏览器支持的全部时区，旧名字换成现在的名字，按名字排序。 */
function allZones(): string[] {
  const zones = new Set(Intl.supportedValuesOf("timeZone").map((z) => renamed[z] ?? z))
  return [...zones].sort()
}

/** 时区 zone 现在的 UTC 偏移，例如 UTC+05:30。 */
function offsetOf(zone: string): string {
  const name = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "longOffset" })
    .formatToParts(new Date())
    .find((p) => p.type === "timeZoneName")?.value
  return !name || name === "GMT" ? "UTC+00:00" : name.replace("GMT", "UTC")
}

/** 偏好设置的表单。 */
export function PreferencesForm(): ReactNode {
  const profile = useQuery({ queryKey: ["me"], queryFn: me })
  if (!profile.data) return null
  return <Form language={profile.data.language} timeZone={profile.data.time_zone ?? ""} />
}

function Form({ language, timeZone }: { language: MemberLanguage; timeZone: string }): ReactNode {
  const queryClient = useQueryClient()
  const t = useT()
  const form = useForm({ initialValues: { language, time_zone: timeZone } })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  // 语言的名称是枚举的显示文字，按界面的当前语言
  const languages = apiKit.enums.MemberLanguage.values.map((v) => ({ value: v, label: apiKit.label("MemberLanguage", v) }))
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
      await api.auth.preferencesUpdate({ language: values.language, time_zone: values.time_zone || undefined })
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
        <Group justify="flex-end">
          <Button type="submit" loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
