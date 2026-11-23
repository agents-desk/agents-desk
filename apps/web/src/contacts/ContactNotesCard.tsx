import { Box, Button, Group, Text, Textarea } from "@mantine/core"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"

/** ContactNotesCard 是联系人内部备注便签块（收件箱详情与联系人档案共用）：淡黄底，就地编辑与 Cmd/Ctrl+Enter 保存。 */
export function ContactNotesCard({
  contactId,
  value: serverValue,
  title,
  placeholder,
  editable,
  disabled,
  saving,
  maxRows = 6,
  onSave,
}: {
  contactId?: string
  value: string
  title: string
  placeholder: string
  editable: boolean
  disabled?: boolean
  saving: boolean
  maxRows?: number
  onSave: (notes: string) => void
}): ReactNode {
  const t = useT()
  const [value, setValue] = useState(serverValue)
  useEffect(() => {
    setValue(serverValue)
  }, [contactId, serverValue])
  const dirty = value !== serverValue

  return (
    <Box p="sm" className="desk-note">
      <Group justify="space-between" mb={4}>
        <Text size="xs" fw={600}>
          {title}
        </Text>
        <Text size="xs" c="dimmed">
          {t("contacts.notes_hint")}
        </Text>
      </Group>
      <Textarea
        variant="unstyled"
        size="sm"
        autosize
        minRows={2}
        maxRows={maxRows}
        maxLength={4000}
        placeholder={placeholder}
        value={value}
        onChange={(e) => setValue(e.currentTarget.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && dirty && editable && !saving) {
            e.preventDefault()
            onSave(value)
          }
        }}
        disabled={!editable || disabled}
      />
      {editable && dirty && (
        <Group justify="flex-end" gap="xs" mt="xs" data-tn-density="inspector">
          <Button variant="subtle" color="gray" onClick={() => setValue(serverValue)}>
            {t("common.cancel")}
          </Button>
          <Button loading={saving} onClick={() => onSave(value)}>
            {t("common.save")}
          </Button>
        </Group>
      )}
    </Box>
  )
}
