import { ActionIcon, Autocomplete, Button, Group, Select, Stack, Switch, Text } from "@mantine/core"
import type { UseFormReturnType } from "@mantine/form"
import { IconPlus, IconX } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import type { HoursInterval, HoursSchedule, HoursWeek } from "@agents-desk/client/tenant"
import type { ReactNode } from "react"
import type { MessageId } from "./messages.gen"

// 工作时间的编辑器：时区、每周七天的时间段。工作区的“工作时间”设置页和团队收件箱单独设置工作时间时共用。
// 校验在服务端：错误按字段（例如 week.monday.1.end）显示在对应的输入框下面。

/** days 是每周七天：接口中的字段名和显示名称的消息 ID，从周一开始。 */
export const days: { key: keyof HoursWeek; label: MessageId }[] = [
  { key: "monday", label: "hours.day.monday" },
  { key: "tuesday", label: "hours.day.tuesday" },
  { key: "wednesday", label: "hours.day.wednesday" },
  { key: "thursday", label: "hours.day.thursday" },
  { key: "friday", label: "hours.day.friday" },
  { key: "saturday", label: "hours.day.saturday" },
  { key: "sunday", label: "hours.day.sunday" },
]

/** defaultInterval 是新加一段时间时的默认值。 */
export const defaultInterval: HoursInterval = { start: "09:00", end: "18:00" }

// times 是时间输入框的候选：每半小时一个，到 24:00；也可以直接输入其他钟点。
const times = Array.from({ length: 49 }, (_, i) => `${String(Math.floor(i / 2)).padStart(2, "0")}:${i % 2 ? "30" : "00"}`)

// zones 是浏览器认识的 IANA 时区名。
const zones = Intl.supportedValuesOf("timeZone")

/** TimeZoneSelect 是时区的下拉框，可以搜索。 */
export function TimeZoneSelect({ form, path }: { form: UseFormReturnType<any>; path: string }): ReactNode {
  const t = useT()
  return (
    <Select
      label={t("hours.time_zone")}
      description={t("hours.time_zone_hint")}
      searchable
      data={zones}
      {...form.getInputProps(path)}
      onChange={(v) => form.setFieldValue(path, v ?? "")}
    />
  )
}

/**
 * Intervals 是一天的时间段：每段一个开始和一个结束，可以增删，最多 3 段。path 是时间段列表在表单中的位置，
 * 例如 week.monday 或 special_dates.0.intervals。
 */
export function Intervals({ form, path }: { form: UseFormReturnType<any>; path: string }): ReactNode {
  const t = useT()
  const list = (form.getInputProps(path).value as HoursInterval[] | undefined) ?? []
  return (
    <Stack gap={6}>
      {list.map((_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: 时间段只按位置编辑
        <Group key={i} gap="xs" wrap="nowrap" align="flex-start">
          <Autocomplete w={110} data={times} placeholder="09:00" aria-label={t("hours.start")} {...form.getInputProps(`${path}.${i}.start`)} />
          <Text mt={6}>–</Text>
          <Autocomplete w={110} data={times} placeholder="18:00" aria-label={t("hours.end")} {...form.getInputProps(`${path}.${i}.end`)} />
          <ActionIcon mt={4} variant="subtle" color="gray" aria-label={t("hours.remove_interval")} onClick={() => form.removeListItem(path, i)}>
            <IconX size={16} />
          </ActionIcon>
        </Group>
      ))}
      {list.length < 3 && (
        <Group>
          <Button
            variant="subtle"
            size="compact-sm"
            leftSection={<IconPlus size={14} />}
            onClick={() => form.insertListItem(path, list.length === 0 ? defaultInterval : { start: "", end: "" })}
          >
            {t("hours.add_interval")}
          </Button>
        </Group>
      )}
    </Stack>
  )
}

/** WeekEditor 是每周七天的工作时间：每天一行，左边是“上班”开关，右边是这天的时间段。path 是 Week 在表单中的位置。 */
export function WeekEditor({ form, path }: { form: UseFormReturnType<any>; path: string }): ReactNode {
  const t = useT()
  return (
    <Stack gap="sm">
      <Text size="sm" fw={500}>
        {t("hours.week")}
      </Text>
      {days.map((d) => {
        const dayPath = `${path}.${d.key}`
        const working = ((form.getInputProps(dayPath).value as HoursInterval[] | undefined) ?? []).length > 0
        return (
          <Group key={d.key} align="flex-start" gap="lg" wrap="nowrap">
            <Switch
              w={110}
              mt={6}
              label={t(d.label)}
              checked={working}
              onChange={(e) => form.setFieldValue(dayPath, e.currentTarget.checked ? [defaultInterval] : [])}
            />
            {working ? (
              <Intervals form={form} path={dayPath} />
            ) : (
              <Text size="sm" c="dimmed" mt={6}>
                {t("hours.off")}
              </Text>
            )}
          </Group>
        )
      })}
    </Stack>
  )
}

/** ScheduleFields 是时区和每周的工作时间。path 是 Schedule 在表单中的位置，为空时就是表单本身。 */
export function ScheduleFields({ form, path = "" }: { form: UseFormReturnType<any>; path?: string }): ReactNode {
  const at = (field: string) => (path ? `${path}.${field}` : field)
  return (
    <Stack gap="md">
      <TimeZoneSelect form={form} path={at("time_zone")} />
      <WeekEditor form={form} path={at("week")} />
    </Stack>
  )
}

/** emptySchedule 是读到设置之前表单的初始值。 */
export const emptySchedule: HoursSchedule = {
  time_zone: "",
  week: { monday: [], tuesday: [], wednesday: [], thursday: [], friday: [], saturday: [], sunday: [] },
}
