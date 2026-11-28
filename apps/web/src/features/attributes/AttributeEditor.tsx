import type { AttributeDTO, AttributeTarget, AttributeType } from "@agents-desk/client/tenant"
import { closestCenter, DndContext, type DragEndEvent, KeyboardSensor, PointerSensor, useSensor, useSensors } from "@dnd-kit/core"
import { restrictToParentElement, restrictToVerticalAxis } from "@dnd-kit/modifiers"
import { arrayMove, SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { ActionIcon, Badge, Button, Code, Group, Paper, Select, Skeleton, Stack, Table, TagsInput, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconGripVertical, IconTrash } from "@tabler/icons-react"
import { applyServerErrors, EmptyState, FormError, RowActions, TableCard, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../api"

/** hasOptions 判断属性类型是否需要配置可选项。 */
const hasOptions = (type: AttributeType): boolean => type === "select" || type === "multi_select"

/** AttributeListTable 是可拖拽排序的自定义属性表格，支持加载与空状态展示。 */
export function AttributeListTable({
  items,
  loading,
  emptyTitle,
  onEdit,
  onRemove,
  onReorder,
}: {
  items: AttributeDTO[]
  loading: boolean
  emptyTitle: string
  onEdit: (attr: AttributeDTO) => void
  onRemove: (attr: AttributeDTO) => void
  onReorder: (next: AttributeDTO[], previous: AttributeDTO[]) => Promise<void>
}): ReactNode {
  const t = useT()
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  // onDragEnd 计算拖拽后的新排序并发起持久化回调。
  const onDragEnd = async (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = items.findIndex((x) => x.id === active.id)
    const newIndex = items.findIndex((x) => x.id === over.id)
    if (oldIndex < 0 || newIndex < 0) return

    const next = arrayMove(items, oldIndex, newIndex).map((item, position) => ({ ...item, position }))
    await onReorder(next, items)
  }

  if (loading) {
    return <Skeleton height={160} radius="md" />
  }
  if (items.length === 0) {
    return (
      <Paper withBorder>
        <EmptyState title={emptyTitle} />
      </Paper>
    )
  }

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      modifiers={[restrictToVerticalAxis, restrictToParentElement]}
      onDragEnd={(e) => void onDragEnd(e)}
    >
      <SortableContext items={items.map((x) => x.id)} strategy={verticalListSortingStrategy}>
        <TableCard minWidth={720}>
          <Table verticalSpacing="sm" horizontalSpacing="md">
            <Table.Thead>
              <Table.Tr>
                <Table.Th w={36} />
                <Table.Th w="22%">{t("attributes.name")}</Table.Th>
                <Table.Th w="20%">{t("attributes.key")}</Table.Th>
                <Table.Th w={100}>{t("attributes.type")}</Table.Th>
                <Table.Th>{t("attributes.details")}</Table.Th>
                <Table.Th w={80} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((attr) => (
                <SortableAttributeRow key={attr.id} attr={attr} onEdit={() => onEdit(attr)} onRemove={() => onRemove(attr)} />
              ))}
            </Table.Tbody>
          </Table>
        </TableCard>
      </SortableContext>
    </DndContext>
  )
}

/** SortableAttributeRow 是可拖动排序的属性表格行，通过左侧手柄拖拽调整顺序。 */
function SortableAttributeRow({ attr, onEdit, onRemove }: { attr: AttributeDTO; onEdit: () => void; onRemove: () => void }): ReactNode {
  const t = useT()
  const { attributes, listeners, setNodeRef, transform, transition, isDragging, isSorting } = useSortable({
    id: attr.id,
    animateLayoutChanges: () => false,
  })
  const style = {
    transform: CSS.Translate.toString(transform),
    // 只在拖动过程中播放让位动画，松手落位时保持静止
    transition: isSorting ? transition : undefined,
    position: isDragging ? ("relative" as const) : undefined,
    zIndex: isDragging ? 1 : undefined,
    backgroundColor: isDragging ? "var(--mantine-color-body)" : undefined,
    boxShadow: isDragging ? "var(--mantine-shadow-sm)" : undefined,
    opacity: isDragging ? 0.92 : undefined,
  }

  return (
    <Table.Tr ref={setNodeRef} style={style}>
      <Table.Td>
        <ActionIcon
          variant="subtle"
          color="gray"
          size="sm"
          style={{ cursor: isDragging ? "grabbing" : "grab", touchAction: "none" }}
          aria-label={t("attributes.drag_handle")}
          {...attributes}
          {...listeners}
        >
          <IconGripVertical size={16} />
        </ActionIcon>
      </Table.Td>
      <Table.Td fw={500}>{attr.name}</Table.Td>
      <Table.Td>
        <Code>{attr.key}</Code>
      </Table.Td>
      <Table.Td>
        <Badge variant="light" color="blue-gray">
          {kit.label("AttributeType", attr.type)}
        </Badge>
      </Table.Td>
      <Table.Td>
        <Stack gap={4}>
          {hasOptions(attr.type) && attr.options.length > 0 && (
            <Group gap={4}>
              {attr.options.map((opt) => (
                <Badge key={opt} size="xs" variant="default" fw={400}>
                  {opt}
                </Badge>
              ))}
            </Group>
          )}
          {attr.description ? (
            <Text size="xs" c="dimmed">
              {attr.description}
            </Text>
          ) : !(hasOptions(attr.type) && attr.options.length > 0) ? (
            <Text size="xs" c="dimmed">
              —
            </Text>
          ) : null}
        </Stack>
      </Table.Td>
      <Table.Td>
        <RowActions
          onEdit={onEdit}
          actions={[{ label: t("common.delete"), icon: <IconTrash size={14} />, danger: true, onClick: onRemove }]}
        />
      </Table.Td>
    </Table.Tr>
  )
}

/** AttributeForm 是新建或编辑自定义属性（含工单类型专属字段）的表单，编辑时锁定 key 和 type。 */
export function AttributeForm({
  target,
  ticketTypeId,
  initial,
  onSaved,
  onCancel,
}: {
  target: AttributeTarget
  ticketTypeId?: string
  initial?: AttributeDTO
  onSaved: () => Promise<void>
  onCancel: () => void
}): ReactNode {
  const t = useT()
  const form = useForm<{ key: string; name: string; type: AttributeType; description: string; options: string[] }>({
    initialValues: {
      key: initial?.key ?? "",
      name: initial?.name ?? "",
      type: initial?.type ?? "text",
      description: initial?.description ?? "",
      options: initial?.options ?? [],
    },
    validate: {
      key: (v) => (initial || /^[a-z][a-z0-9_]{0,63}$/.test(v.trim()) ? null : t("attributes.key_invalid")),
      name: (v) => (v.trim() ? null : t("attributes.name_required")),
      options: (v, values) => (!hasOptions(values.type) || v.length > 0 ? null : t("attributes.options_required")),
    },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      const options = hasOptions(values.type) ? values.options : []
      if (initial) {
        await tenantApi.attribute.update(initial.id, {
          name: values.name.trim(),
          description: values.description.trim(),
          options,
        })
      } else {
        await tenantApi.attribute.create({
          target,
          ...(ticketTypeId ? { ticket_type_id: ticketTypeId } : {}),
          key: values.key.trim(),
          name: values.name.trim(),
          description: values.description.trim(),
          type: values.type,
          options,
        })
      }
      await onSaved()
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
        <TextInput
          label={t("attributes.name")}
          placeholder={t("attributes.name_placeholder")}
          required
          maxLength={60}
          data-autofocus
          {...form.getInputProps("name")}
        />
        <TextInput
          label={t("attributes.key")}
          description={t(initial ? "attributes.key_locked" : "attributes.key_hint")}
          placeholder={t("attributes.key_placeholder")}
          required
          disabled={!!initial}
          maxLength={64}
          {...form.getInputProps("key")}
        />
        <Select
          label={t("attributes.type")}
          description={initial ? t("attributes.type_locked") : undefined}
          required
          disabled={!!initial}
          allowDeselect={false}
          data={kit.enums.AttributeType.values.map((v) => ({ value: v, label: kit.label("AttributeType", v) }))}
          {...form.getInputProps("type")}
        />
        {hasOptions(form.values.type) && (
          <TagsInput
            label={t("attributes.options")}
            description={t("attributes.options_hint")}
            placeholder={t("attributes.options_placeholder")}
            required
            {...form.getInputProps("options")}
          />
        )}
        <TextInput
          label={t("attributes.description_label")}
          placeholder={t("attributes.description_placeholder")}
          maxLength={200}
          {...form.getInputProps("description")}
        />
        <Group justify="flex-end" gap="xs">
          <Button variant="default" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
