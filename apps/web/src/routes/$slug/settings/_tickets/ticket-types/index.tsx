import type { TicketTypeDTO, TicketTypeVisibility } from "@agents-desk/client/tenant"
import { closestCenter, DndContext, type DragEndEvent, KeyboardSensor, PointerSensor, useSensor, useSensors } from "@dnd-kit/core"
import { restrictToParentElement, restrictToVerticalAxis } from "@dnd-kit/modifiers"
import { arrayMove, SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { ActionIcon, Anchor, Badge, Button, Group, Modal, Radio, Select, Stack, Table, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { IconGripVertical, IconPlayerPause, IconPlayerPlay, IconPlus, IconTrash } from "@tabler/icons-react"
import { applyServerErrors, confirm, FormError, notifyError, notifySuccess, Page, preloadQueries, StatusDot, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { EmptyState } from "../../../../../components/empty-state"
import { RowActions } from "../../../../../components/row-actions"

// 工单类型列表：管理工单模板、默认团队收件箱、默认可见范围、拖拽排序及停用/启用；点击名称进入字段配置。
export const Route = createFileRoute("/$slug/settings/_tickets/ticket-types/")({
  // 预取页面的数据，最多等一小会儿：切换到这一页时内容一次完整出现，不先空白再出现
  loader: ({ context }) => preloadQueries(context.app.queryClient, [kit.queries.ticket.typeList({})]),
  component: TicketTypesPage,
})

function TicketTypesPage(): ReactNode {
  const { slug } = Route.useParams()
  const t = useT()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<TicketTypeDTO>()
  const [draftOrder, setDraftOrder] = useState<TicketTypeDTO[] | null>(null)
  const listQuery = kit.queries.ticket.typeList({})
  const list = useQuery(listQuery)
  const items = draftOrder ?? list.data ?? []

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const invalidate = () => queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })

  const toggleDisabled = async (item: TicketTypeDTO) => {
    const isDisabled = item.status === "disabled"
    if (!isDisabled) {
      const ok = await confirm({
        title: t("ticket_types.disable_confirm", { name: item.name }),
        message: t("ticket_types.disable_message"),
        danger: true,
        confirmLabel: t("ticket_types.disable"),
      })
      if (!ok) return
    }
    try {
      if (isDisabled) {
        await tenantApi.ticket.typeEnable(item.id)
        notifySuccess(t("ticket_types.enabled_done"))
      } else {
        await tenantApi.ticket.typeDisable(item.id)
        notifySuccess(t("ticket_types.disabled_done"))
      }
      await invalidate()
    } catch (err) {
      notifyError(err)
    }
  }

  const remove = async (item: TicketTypeDTO) => {
    const ok = await confirm({
      title: t("ticket_types.delete_confirm", { name: item.name }),
      message: t("ticket_types.delete_message"),
      danger: true,
      confirmLabel: t("ticket_types.delete"),
    })
    if (!ok) return
    try {
      await tenantApi.ticket.typeDelete(item.id)
      setDraftOrder(null)
      await invalidate()
      notifySuccess(t("ticket_types.deleted"))
    } catch (err) {
      notifyError(err)
    }
  }

  const onDragEnd = async (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = items.findIndex((x) => x.id === active.id)
    const newIndex = items.findIndex((x) => x.id === over.id)
    if (oldIndex < 0 || newIndex < 0) return

    const previous = items
    const next = arrayMove(items, oldIndex, newIndex).map((item, position) => ({ ...item, position }))
    setDraftOrder(next)
    queryClient.setQueryData(listQuery.queryKey, next)
    try {
      const saved = await tenantApi.ticket.typeReorder({ ids: next.map((x) => x.id) })
      queryClient.setQueryData(listQuery.queryKey, saved)
      setDraftOrder(null)
      await invalidate()
    } catch (err) {
      queryClient.setQueryData(listQuery.queryKey, previous)
      setDraftOrder(null)
      notifyError(err)
    }
  }

  return (
    <Page
      title={t("ticket_types.title")}
      description={t("ticket_types.description")}
      actions={
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
          {t("ticket_types.add")}
        </Button>
      }
    >
      {items.length === 0 ? (
        <div className="desk-table-card">
          <EmptyState title={t("ticket_types.empty")} />
        </div>
      ) : (
        <DndContext
          sensors={sensors}
          collisionDetection={closestCenter}
          modifiers={[restrictToVerticalAxis, restrictToParentElement]}
          onDragEnd={(e) => void onDragEnd(e)}
        >
          <SortableContext items={items.map((x) => x.id)} strategy={verticalListSortingStrategy}>
            <Table.ScrollContainer minWidth={760} className="desk-table-card" type="native">
              <Table verticalSpacing="sm" horizontalSpacing="md">
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th w={36} />
                    <Table.Th>{t("ticket_types.name")}</Table.Th>
                    <Table.Th w={140}>{t("ticket_types.default_visibility")}</Table.Th>
                    <Table.Th w={160}>{t("ticket_types.default_team")}</Table.Th>
                    <Table.Th w={90}>{t("ticket_types.field_count")}</Table.Th>
                    <Table.Th w={90}>{t("ticket_types.status")}</Table.Th>
                    <Table.Th w={120} />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {items.map((item) => (
                    <SortableTypeRow
                      key={item.id}
                      slug={slug}
                      item={item}
                      onEdit={() => setEditing(item)}
                      onToggleDisabled={() => void toggleDisabled(item)}
                      onRemove={() => void remove(item)}
                    />
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </SortableContext>
        </DndContext>
      )}

      <Modal
        opened={creating || !!editing}
        onClose={() => {
          setCreating(false)
          setEditing(undefined)
        }}
        title={t(editing ? "ticket_types.edit" : "ticket_types.add")}
        size="md"
      >
        {(creating || editing) && (
          <TicketTypeForm
            initial={editing}
            onCancel={() => {
              setCreating(false)
              setEditing(undefined)
            }}
            onSaved={async () => {
              await invalidate()
              setCreating(false)
              setEditing(undefined)
              notifySuccess(t("common.saved"))
            }}
          />
        )}
      </Modal>
    </Page>
  )
}

/** SortableTypeRow 是可拖拽排序的工单类型行。 */
function SortableTypeRow({
  slug,
  item,
  onEdit,
  onToggleDisabled,
  onRemove,
}: {
  slug: string
  item: TicketTypeDTO
  onEdit: () => void
  onToggleDisabled: () => void
  onRemove: () => void
}): ReactNode {
  const t = useT()
  const isDisabled = item.status === "disabled"
  const { attributes, listeners, setNodeRef, transform, transition, isDragging, isSorting } = useSortable({
    id: item.id,
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
          aria-label={t("ticket_types.drag_handle")}
          {...attributes}
          {...listeners}
        >
          <IconGripVertical size={16} />
        </ActionIcon>
      </Table.Td>
      <Table.Td>
        <Stack gap={2}>
          <Anchor
            fw={500}
            renderRoot={(props) => <Link {...props} to="/$slug/settings/ticket-types/$id" params={{ slug, id: item.id }} />}
          >
            {item.name}
          </Anchor>
          {item.description && (
            <Text size="xs" c="dimmed" lineClamp={1}>
              {item.description}
            </Text>
          )}
        </Stack>
      </Table.Td>
      <Table.Td>
        <Badge variant="light" color={item.default_visibility === "all" ? "bright-blue" : "gray"}>
          {kit.label("TicketTypeVisibility", item.default_visibility)}
        </Badge>
      </Table.Td>
      <Table.Td>
        <Text size="sm" c={item.default_team_name ? undefined : "dimmed"}>
          {item.default_team_name || "—"}
        </Text>
      </Table.Td>
      <Table.Td>
        <Text size="sm">{item.field_count}</Text>
      </Table.Td>
      <Table.Td>
        <StatusDot color={isDisabled ? "gray" : "green"}>{kit.label("TicketTypeStatus", item.status)}</StatusDot>
      </Table.Td>
      <Table.Td>
        <RowActions
          onEdit={onEdit}
          actions={[
            {
              label: t(isDisabled ? "ticket_types.enable" : "ticket_types.disable"),
              icon: isDisabled ? <IconPlayerPlay size={14} /> : <IconPlayerPause size={14} />,
              onClick: onToggleDisabled,
            },
            { label: t("common.delete"), icon: <IconTrash size={14} />, danger: true, onClick: onRemove },
          ]}
        />
      </Table.Td>
    </Table.Tr>
  )
}

/** TicketTypeForm 是新建或编辑工单类型的表单。 */
export function TicketTypeForm({
  initial,
  onSaved,
  onCancel,
}: {
  initial?: TicketTypeDTO
  onSaved: () => Promise<void>
  onCancel: () => void
}): ReactNode {
  const t = useT()
  const teamsQuery = useQuery(kit.queries.team.list({ per_page: 100 }))
  const activeTeams = (teamsQuery.data?.items ?? []).filter((tm) => tm.status === "active" || tm.id === initial?.default_team_id)
  const form = useForm<{
    name: string
    description: string
    default_team_id: string | null
    default_visibility: TicketTypeVisibility
  }>({
    initialValues: {
      name: initial?.name ?? "",
      description: initial?.description ?? "",
      default_team_id: initial?.default_team_id ?? null,
      default_visibility: initial?.default_visibility ?? "all",
    },
    validate: {
      name: (v) => (v.trim() ? null : t("ticket_types.name_required")),
    },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      const payload = {
        name: values.name.trim(),
        description: values.description.trim(),
        default_team_id: values.default_team_id || undefined,
        default_visibility: values.default_visibility,
      }
      if (initial) {
        await tenantApi.ticket.typeUpdate(initial.id, payload)
      } else {
        await tenantApi.ticket.typeCreate(payload)
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
          label={t("ticket_types.name")}
          placeholder={t("ticket_types.name_placeholder")}
          required
          maxLength={60}
          data-autofocus
          {...form.getInputProps("name")}
        />
        <TextInput
          label={t("ticket_types.description_label")}
          placeholder={t("ticket_types.description_placeholder")}
          maxLength={200}
          {...form.getInputProps("description")}
        />
        <Select
          label={t("ticket_types.default_team")}
          placeholder={t("ticket_types.default_team_placeholder")}
          clearable
          searchable
          data={activeTeams.map((tm) => ({ value: tm.id, label: tm.name }))}
          {...form.getInputProps("default_team_id")}
        />
        <Radio.Group
          label={t("ticket_types.default_visibility")}
          description={t("ticket_types.default_visibility_hint")}
          {...form.getInputProps("default_visibility")}
        >
          <Stack gap="xs" mt="xs">
            <Radio value="all" label={kit.label("TicketTypeVisibility", "all")} />
            <Radio value="members" label={kit.label("TicketTypeVisibility", "members")} />
          </Stack>
        </Radio.Group>
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
