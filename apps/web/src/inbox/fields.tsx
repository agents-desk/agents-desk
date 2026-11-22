import type { AttributeDTO, TagDTO, TagSummary } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, ColorSwatch, Group, Menu, MultiSelect, NumberInput, Select, Switch, Text, TextInput } from "@mantine/core"
import { IconPlus, IconX } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"

/** TagsEditor 展示已打的标签并允许添加、移除。 */
export function TagsEditor({
  tags,
  allTags,
  editable,
  disabled,
  onAdd,
  onRemove,
}: {
  tags: TagSummary[]
  allTags: TagDTO[]
  editable: boolean
  disabled?: boolean
  onAdd: (tagId: string) => void
  onRemove: (tagId: string) => void
}): ReactNode {
  const t = useT()
  const attached = new Set(tags.map((x) => x.id))
  const available = allTags.filter((x) => !attached.has(x.id))

  return (
    <Group gap={4} wrap="wrap" align="center">
      {tags.map((tag) => (
        <Badge
          key={tag.id}
          size="sm"
          variant="light"
          color={tag.color}
          pr={editable ? 4 : undefined}
          rightSection={
            editable ? (
              <ActionIcon
                size={14}
                variant="transparent"
                color={tag.color}
                disabled={disabled}
                onClick={() => onRemove(tag.id)}
                aria-label={t("inbox.remove_tag", { tag: tag.name })}
              >
                <IconX size={10} />
              </ActionIcon>
            ) : undefined
          }
        >
          {tag.name}
        </Badge>
      ))}
      {!editable && tags.length === 0 && (
        <Text size="xs" c="dimmed">
          —
        </Text>
      )}
      {editable && (
        <Menu position="bottom-start" withinPortal>
          <Menu.Target>
            <ActionIcon size="xs" variant="light" color="gray" disabled={disabled} aria-label={t("inbox.add_tag")}>
              <IconPlus size={12} />
            </ActionIcon>
          </Menu.Target>
          <Menu.Dropdown mah={240} style={{ overflowY: "auto" }}>
            {available.length === 0 ? (
              <Menu.Item disabled>{t("inbox.no_tags")}</Menu.Item>
            ) : (
              available.map((tag) => (
                <Menu.Item
                  key={tag.id}
                  leftSection={<ColorSwatch color={`var(--mantine-color-${tag.color}-6)`} size={10} />}
                  onClick={() => onAdd(tag.id)}
                >
                  {tag.name}
                </Menu.Item>
              ))
            )}
          </Menu.Dropdown>
        </Menu>
      )}
    </Group>
  )
}

/** AttributeInput 按属性定义渲染对应的编辑控件，修改后通过 onSave 提交单个属性变更。 */
export function AttributeInput({
  def,
  value,
  disabled,
  onSave,
}: {
  def: AttributeDTO
  value: unknown
  disabled?: boolean
  onSave: (next: unknown) => void
}): ReactNode {
  switch (def.type) {
    case "text":
      return <TextAttrInput value={typeof value === "string" ? value : ""} disabled={disabled} onSave={onSave} />
    case "number":
      return <NumberAttrInput value={typeof value === "number" ? value : ""} disabled={disabled} onSave={onSave} />
    case "boolean":
      return <BooleanAttrInput value={Boolean(value)} disabled={disabled} onSave={onSave} />
    case "date":
      return <DateAttrInput value={typeof value === "string" ? value : ""} disabled={disabled} onSave={onSave} />
    case "select":
      return <SelectAttrInput options={def.options} value={typeof value === "string" ? value : null} disabled={disabled} onSave={onSave} />
    case "multi_select":
      return (
        <MultiSelectAttrInput
          options={def.options}
          value={Array.isArray(value) ? value.filter((x): x is string => typeof x === "string") : []}
          disabled={disabled}
          onSave={onSave}
        />
      )
  }
}

function TextAttrInput({ value, disabled, onSave }: { value: string; disabled?: boolean; onSave: (next: unknown) => void }): ReactNode {
  const t = useT()
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const commit = () => {
    if (draft !== value) onSave(draft === "" ? null : draft)
  }
  return (
    <TextInput
      size="xs"
      value={draft}
      placeholder={t("inbox.attr_text_placeholder")}
      disabled={disabled}
      onChange={(e) => setDraft(e.currentTarget.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter" && !e.nativeEvent.isComposing) {
          e.currentTarget.blur()
        }
      }}
    />
  )
}

function NumberAttrInput({ value, disabled, onSave }: { value: number | ""; disabled?: boolean; onSave: (next: unknown) => void }): ReactNode {
  const t = useT()
  const [draft, setDraft] = useState<string | number>(value)
  useEffect(() => setDraft(value), [value])
  const commit = () => {
    if (draft === "" || draft === undefined) {
      if (value !== "") onSave(null)
      return
    }
    const n = typeof draft === "number" ? draft : Number(draft)
    if (!Number.isNaN(n) && n !== value) onSave(n)
  }
  return (
    <NumberInput
      size="xs"
      value={draft}
      placeholder={t("inbox.attr_number_placeholder")}
      disabled={disabled}
      hideControls
      onChange={setDraft}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") e.currentTarget.blur()
      }}
    />
  )
}

function BooleanAttrInput({ value, disabled, onSave }: { value: boolean; disabled?: boolean; onSave: (next: unknown) => void }): ReactNode {
  const t = useT()
  return (
    <Switch
      size="xs"
      checked={value}
      disabled={disabled}
      label={t(value ? "inbox.attr_yes" : "inbox.attr_no")}
      onChange={(e) => onSave(e.currentTarget.checked)}
    />
  )
}

function DateAttrInput({ value, disabled, onSave }: { value: string; disabled?: boolean; onSave: (next: unknown) => void }): ReactNode {
  return (
    <TextInput
      size="xs"
      type="date"
      value={value}
      disabled={disabled}
      onChange={(e) => {
        const next = e.currentTarget.value
        if (next !== value) onSave(next === "" ? null : next)
      }}
    />
  )
}

function SelectAttrInput({
  options,
  value,
  disabled,
  onSave,
}: {
  options: string[]
  value: string | null
  disabled?: boolean
  onSave: (next: unknown) => void
}): ReactNode {
  const t = useT()
  return (
    <Select
      size="xs"
      placeholder={t("inbox.attr_select")}
      value={value}
      data={options}
      disabled={disabled}
      clearable
      searchable
      onChange={(v) => {
        const next = v ?? null
        if (next !== value) onSave(next)
      }}
    />
  )
}

function MultiSelectAttrInput({
  options,
  value,
  disabled,
  onSave,
}: {
  options: string[]
  value: string[]
  disabled?: boolean
  onSave: (next: unknown) => void
}): ReactNode {
  const t = useT()
  return (
    <MultiSelect
      size="xs"
      placeholder={value.length === 0 ? t("inbox.attr_select") : undefined}
      value={value}
      data={options}
      disabled={disabled}
      clearable
      searchable
      onChange={(v) => onSave(v.length > 0 ? v : null)}
    />
  )
}
