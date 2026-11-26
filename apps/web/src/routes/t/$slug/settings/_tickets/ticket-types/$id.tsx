import type { AttributeDTO, AttributeType } from "@agents-desk/client/tenant"
import { closestCenter, DndContext, type DragEndEvent, KeyboardSensor, PointerSensor, useSensor, useSensors } from "@dnd-kit/core"
import { restrictToParentElement, restrictToVerticalAxis } from "@dnd-kit/modifiers"
import { arrayMove, SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { ActionIcon, Alert, Badge, Button, Code, Group, Modal, Select, Stack, Table, TagsInput, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconGripVertical, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react"
import { applyServerErrors, confirm, FormError, notifyError, notifySuccess, Page, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../../api"
import { TicketTypeForm } from "./index"

// 工单类型详情页：管理该类型的基本信息及专属自定义字段（增删改与拖拽排序）。
export const Route = createFileRoute("/t/$slug/settings/_tickets/ticket-types/$id")({
  staticData: { title: "ticket_types.detail_title" },
  component: TicketTypeDetailPage,
})

const hasOptions = (type: AttributeType): boolean => type === "select" || type === "multi_select"

function TicketTypeDetailPage(): ReactNode {
  const { slug, id } = Route.useParams()
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()

  const typeQuery = useQuery(kit.queries.ticket.typeGet(id))
  const fieldsQueryOpts = kit.queries.attribute.list({ target: "ticket", ticket_type_id: id })
  const fieldsQuery = useQuery(fieldsQueryOpts)
  const [editingType, setEditingType] = useState(false)
  const [creatingField, setCreatingField] = useState(false)
  const [editingField, setEditingField] = useState<AttributeDTO>()
  const [draftOrder, setDraftOrder] = useState<AttributeDTO[] | null>(null)
  const [toggling, setToggling] = useState(false)

  const item = typeQuery.data
  const fields = draftOrder ?? fieldsQuery.data ?? []
  const isDisabled = item?.status === "disabled"

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const invalidateAll = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() }),
      queryClient.invalidateQueries({ queryKey: kit.queries.attribute.key() }),
    ])
  }

  const toggleDisabled = async () => {
    if (!item) return
    if (!isDisabled) {
      const ok = await confirm({
        title: t("ticket_types.disable_confirm", { name: item.name }),
        message: t("ticket_types.disable_message"),
        danger: true,
        confirmLabel: t("ticket_types.disable"),
      })
      if (!ok) return
    }
    setToggling(true)
    try {
      if (isDisabled) {
        await tenantApi.ticket.typeEnable(item.id)
        notifySuccess(t("ticket_types.enabled_done"))
      } else {
        await tenantApi.ticket.typeDisable(item.id)
        notifySuccess(t("ticket_types.disabled_done"))
      }
      await invalidateAll()
    } catch (err) {
      notifyError(err)
    } finally {
      setToggling(false)
    }
  }

  const removeField = async (attr: AttributeDTO) => {
    const ok = await confirm({
      title: t("attributes.delete_confirm", { name: attr.name }),
      message: t("attributes.delete_message"),
      danger: true,
      confirmLabel: t("attributes.delete"),
    })
    if (!ok) return
    try {
      await tenantApi.attribute.delete(attr.id)
      setDraftOrder(null)
      await invalidateAll()
      notifySuccess(t("attributes.deleted"))
    } catch (err) {
      notifyError(err)
    }
  }

  const onDragEnd = async (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = fields.findIndex((x) => x.id === active.id)
    const newIndex = fields.findIndex((x) => x.id === over.id)
    if (oldIndex < 0 || newIndex < 0) return

    const previous = fields
    const next = arrayMove(fields, oldIndex, newIndex).map((f, position) => ({ ...f, position }))
    setDraftOrder(next)
    queryClient.setQueryData(fieldsQueryOpts.queryKey, next)
    try {
      const saved = await tenantApi.attribute.reorder({ target: "ticket", ticket_type_id: id, ids: next.map((x) => x.id) })
      queryClient.setQueryData(fieldsQueryOpts.queryKey, saved)
      setDraftOrder(null)
      await invalidateAll()
    } catch (err) {
      queryClient.setQueryData(fieldsQueryOpts.queryKey, previous)
      setDraftOrder(null)
      notifyError(err)
    }
  }

  return (
    <Page
      title={item?.name ?? t("ticket_types.detail_title")}
      description={item?.description || undefined}
      back={() => void navigate({ to: "/t/$slug/settings/ticket-types", params: { slug } })}
      actions={
        item ? (
          <Group gap="xs">
            <Button variant="default" size="xs" leftSection={<IconPencil size={14} />} onClick={() => setEditingType(true)}>
              {t("ticket_types.edit")}
            </Button>
            <Button
              variant={isDisabled ? "filled" : "light"}
              color={isDisabled ? undefined : "red"}
              size="xs"
              loading={toggling}
              onClick={() => void toggleDisabled()}
            >
              {t(isDisabled ? "ticket_types.enable" : "ticket_types.disable")}
            </Button>
            <Button size="xs" leftSection={<IconPlus size={14} />} onClick={() => setCreatingField(true)}>
              {t("ticket_types.add_field")}
            </Button>
          </Group>
        ) : undefined
      }
    >
      <Stack gap="md">
        {isDisabled && (
          <Alert color="gray" variant="light">
            {t("ticket_types.disabled_notice")}
          </Alert>
        )}
        {item && (
          <Group gap="md">
            <Group gap={6}>
              <Text size="sm" c="dimmed">
                {t("ticket_types.default_visibility")}:
              </Text>
              <Badge variant="light" color={item.default_visibility === "all" ? "bright-blue" : "gray"}>
                {kit.label("TicketTypeVisibility", item.default_visibility)}
              </Badge>
            </Group>
            <Group gap={6}>
              <Text size="sm" c="dimmed">
                {t("ticket_types.default_team")}:
              </Text>
              <Text size="sm">{item.default_team_name || "—"}</Text>
            </Group>
          </Group>
        )}

        <Text fw={500} size="sm" mt="xs">
          {t("ticket_types.fields_heading")}
        </Text>

        {fields.length === 0 ? (
          <Text c="dimmed" ta="center" py="xl">
            {t("ticket_types.fields_empty")}
          </Text>
        ) : (
          <DndContext
            sensors={sensors}
            collisionDetection={closestCenter}
            modifiers={[restrictToVerticalAxis, restrictToParentElement]}
            onDragEnd={(e) => void onDragEnd(e)}
          >
            <SortableContext items={fields.map((x) => x.id)} strategy={verticalListSortingStrategy}>
              <Table verticalSpacing="sm">
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
                  {fields.map((attr) => (
                    <SortableFieldRow
                      key={attr.id}
                      attr={attr}
                      onEdit={() => setEditingField(attr)}
                      onRemove={() => void removeField(attr)}
                    />
                  ))}
                </Table.Tbody>
              </Table>
            </SortableContext>
          </DndContext>
        )}
      </Stack>

      <Modal opened={editingType && !!item} onClose={() => setEditingType(false)} title={t("ticket_types.edit")} size="md">
        {editingType && item && (
          <TicketTypeForm
            initial={item}
            onCancel={() => setEditingType(false)}
            onSaved={async () => {
              await invalidateAll()
              setEditingType(false)
              notifySuccess(t("common.saved"))
            }}
          />
        )}
      </Modal>

      <Modal
        opened={creatingField || !!editingField}
        onClose={() => {
          setCreatingField(false)
          setEditingField(undefined)
        }}
        title={t(editingField ? "attributes.edit" : "ticket_types.add_field")}
        size="md"
      >
        {(creatingField || editingField) && (
          <TicketFieldForm
            ticketTypeId={id}
            initial={editingField}
            onCancel={() => {
              setCreatingField(false)
              setEditingField(undefined)
            }}
            onSaved={async () => {
              await invalidateAll()
              setCreatingField(false)
              setEditingField(undefined)
              notifySuccess(t("common.saved"))
            }}
          />
        )}
      </Modal>
    </Page>
  )
}

/** SortableFieldRow 是可拖拽排序的工单自定义字段行。 */
function SortableFieldRow({ attr, onEdit, onRemove }: { attr: AttributeDTO; onEdit: () => void; onRemove: () => void }): ReactNode {
  const t = useT()
  const { attributes, listeners, setNodeRef, transform, transition, isDragging, isSorting } = useSortable({
    id: attr.id,
    animateLayoutChanges: () => false,
  })
  const style = {
    transform: CSS.Translate.toString(transform),
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
          ) : !hasOptions(attr.type) ? (
            <Text size="xs" c="dimmed">
              —
            </Text>
          ) : null}
        </Stack>
      </Table.Td>
      <Table.Td>
        <Group gap={4} justify="flex-end" wrap="nowrap">
          <ActionIcon variant="subtle" color="gray" onClick={onEdit} aria-label={t("attributes.edit")}>
            <IconPencil size={15} />
          </ActionIcon>
          <ActionIcon variant="subtle" color="red" onClick={onRemove} aria-label={t("attributes.delete")}>
            <IconTrash size={15} />
          </ActionIcon>
        </Group>
      </Table.Td>
    </Table.Tr>
  )
}

/** TicketFieldForm 是新建或编辑工单类型专属字段的表单。 */
function TicketFieldForm({
  ticketTypeId,
  initial,
  onSaved,
  onCancel,
}: {
  ticketTypeId: string
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
          target: "ticket",
          ticket_type_id: ticketTypeId,
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
        <Group justify="flex-end">
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
