import { ActionIcon, Alert, Button, Group, Radio, Stack, Switch, Tabs, Text, TextInput } from "@mantine/core"
import { DateInput } from "@mantine/dates"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconPlus, IconTrash } from "@tabler/icons-react"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, notifySuccess, Page, useT } from "@go-tenon/ui"
import type { HoursReplyTime, HoursSchedule, HoursSettingsDTO, HoursSpecialDate } from "@agents-desk/client/tenant"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { emptySchedule, Intervals, ScheduleFields } from "../../../../../business-hours"

// 工作时间：时区和每周的工作时间、特殊日期（节假日、调休）、通常的回复时间。三个标签各自保存，保存一个不影响其他的。
// 聊天插件和自动化用它们告诉客户什么时候能得到回复，收件箱按回复时间给等待时长变色。
export const Route = createFileRoute("/t/$slug/settings/_workspace/business-hours")({
  staticData: { title: "business_hours.title", icon: "clock", order: 3, permission: "tenant-settings:manage" },
  component: BusinessHours,
})

const queryKey = ["business-hours"]

function BusinessHours(): ReactNode {
  const current = useQuery({ queryKey, queryFn: () => tenantApi.hours.get() })
  const t = useT()
  return (
    <Page title={t("business_hours.title")} description={t("business_hours.description")}>
      <Tabs defaultValue="schedule" keepMounted>
        <Tabs.List>
          <Tabs.Tab value="schedule">{t("business_hours.tab_schedule")}</Tabs.Tab>
          <Tabs.Tab value="special">{t("business_hours.tab_special")}</Tabs.Tab>
          <Tabs.Tab value="reply">{t("business_hours.tab_reply")}</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="schedule" pt="md">
          {current.data && <ScheduleForm saved={current.data} />}
        </Tabs.Panel>
        <Tabs.Panel value="special" pt="md">
          {current.data && <SpecialDatesForm saved={current.data} />}
        </Tabs.Panel>
        <Tabs.Panel value="reply" pt="md">
          {current.data && <ReplyTimeForm saved={current.data} />}
        </Tabs.Panel>
      </Tabs>
    </Page>
  )
}

/** useSave 保存一个标签：成功后更新缓存、提示已保存并把表单的初始值换成保存的值，失败时把错误显示在字段下面。 */
function useSave<T extends object>(form: ReturnType<typeof useForm<T>>, pick: (s: HoursSettingsDTO) => T) {
  const queryClient = useQueryClient()
  const t = useT()
  const [error, setError] = useState<ErrorInfo>()
  const [saving, setSaving] = useState(false)
  const submit = (save: (values: T) => Promise<HoursSettingsDTO>) =>
    form.onSubmit(async (values) => {
      setSaving(true)
      setError(undefined)
      try {
        const saved = await save(values)
        queryClient.setQueryData(queryKey, saved)
        form.setInitialValues(pick(saved))
        form.setValues(pick(saved))
        notifySuccess(t("common.saved"))
      } catch (err) {
        setError(applyServerErrors(form, err))
      } finally {
        setSaving(false)
      }
    })
  return { error, saving, submit }
}

/** SaveButton 是靠右的保存按钮，没有修改时不能点。 */
function SaveButton({ dirty, saving }: { dirty: boolean; saving: boolean }): ReactNode {
  const t = useT()
  return (
    <Group justify="flex-end">
      <Button type="submit" disabled={!dirty} loading={saving}>
        {t("common.save")}
      </Button>
    </Group>
  )
}

/** ScheduleForm 是时区和每周的工作时间。 */
function ScheduleForm({ saved }: { saved: HoursSettingsDTO }): ReactNode {
  const pick = (s: HoursSettingsDTO): HoursSchedule => ({ time_zone: s.time_zone, week: s.week })
  const form = useForm<HoursSchedule>({ initialValues: emptySchedule })
  useEffect(() => {
    form.setInitialValues(pick(saved))
    form.setValues(pick(saved))
  }, [])
  const { error, saving, submit } = useSave(form, pick)
  return (
    <form noValidate onSubmit={submit((v) => tenantApi.hours.scheduleSave(v))}>
      <Stack gap="md">
        <FormError error={error} />
        <ScheduleFields form={form} />
        <SaveButton dirty={form.isDirty()} saving={saving} />
      </Stack>
    </form>
  )
}

/** SpecialDatesForm 是特殊日期：节假日全天不上班，调休的日子按自己的时间段上班。 */
function SpecialDatesForm({ saved }: { saved: HoursSettingsDTO }): ReactNode {
  const pick = (s: HoursSettingsDTO) => ({ special_dates: s.special_dates })
  const form = useForm<{ special_dates: HoursSpecialDate[] }>({ initialValues: { special_dates: [] } })
  useEffect(() => {
    form.setInitialValues(pick(saved))
    form.setValues(pick(saved))
  }, [])
  const { error, saving, submit } = useSave(form, pick)
  const dates = form.getValues().special_dates
  const t = useT()
  return (
    <form noValidate onSubmit={submit((v) => tenantApi.hours.specialDatesSave(v))}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("business_hours.special_hint")}
        </Text>
        <FormError error={error} />
        {dates.map((d, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: 特殊日期按位置编辑
          <Stack key={i} gap="xs" p="sm" style={{ border: "1px solid var(--tn-border)", borderRadius: "var(--mantine-radius-md)" }}>
            <Group justify="space-between">
              <Text size="sm" fw={500}>
                {d.date || t("business_hours.new_date")}
              </Text>
              <ActionIcon variant="subtle" color="red" aria-label={t("business_hours.remove_date")} onClick={() => form.removeListItem("special_dates", i)}>
                <IconTrash size={16} />
              </ActionIcon>
            </Group>
            <DateInput label={t("business_hours.date")} valueFormat="YYYY-MM-DD" {...form.getInputProps(`special_dates.${i}.date`)} />
            <TextInput label={t("business_hours.name")} placeholder={t("business_hours.name_placeholder")} {...form.getInputProps(`special_dates.${i}.name`)} />
            <Switch
              label={t("business_hours.closed")}
              checked={d.intervals.length === 0}
              onChange={(e) => form.setFieldValue(`special_dates.${i}.intervals`, e.currentTarget.checked ? [] : [{ start: "09:00", end: "18:00" }])}
            />
            {d.intervals.length > 0 && <Intervals form={form} path={`special_dates.${i}.intervals`} />}
          </Stack>
        ))}
        <Group>
          <Button
            variant="light"
            leftSection={<IconPlus size={16} />}
            onClick={() => form.insertListItem("special_dates", { date: "", name: "", intervals: [] })}
            disabled={dates.length >= 100}
          >
            {t("business_hours.add_date")}
          </Button>
        </Group>
        <SaveButton dirty={form.isDirty()} saving={saving} />
      </Stack>
    </form>
  )
}

const replyTimes: HoursReplyTime[] = ["minutes", "hour", "hours", "day"]

/** ReplyTimeForm 是通常多久回复。 */
function ReplyTimeForm({ saved }: { saved: HoursSettingsDTO }): ReactNode {
  const pick = (s: HoursSettingsDTO) => ({ reply_time: s.reply_time })
  const form = useForm<{ reply_time: HoursReplyTime }>({ initialValues: pick(saved) })
  const { error, saving, submit } = useSave(form, pick)
  const t = useT()
  return (
    <form noValidate onSubmit={submit((v) => tenantApi.hours.replyTimeSave(v))}>
      <Stack gap="md">
        <FormError error={error} />
        <Radio.Group label={t("business_hours.reply_time")} {...form.getInputProps("reply_time")}>
          <Stack gap="xs" mt="xs">
            {replyTimes.map((r) => (
              <Radio key={r} value={r} label={kit.label("HoursReplyTime", r)} />
            ))}
          </Stack>
        </Radio.Group>
        <Alert color="gray" variant="light">
          {t("business_hours.reply_hint", { time: kit.label("HoursReplyTime", form.getValues().reply_time) })}
        </Alert>
        <SaveButton dirty={form.isDirty()} saving={saving} />
      </Stack>
    </form>
  )
}
