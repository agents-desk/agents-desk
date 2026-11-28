import type { AttributeDTO } from "@agents-desk/client/tenant"
import { Alert, Badge, Button, Group, Modal, Stack, Text } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconPencil, IconPlus } from "@tabler/icons-react"
import { confirm, notifyError, notifySuccess, Page, preloadQueries, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { AttributeForm, AttributeListTable } from "../../../../../features/attributes/AttributeEditor"
import { TicketTypeForm } from "./index"

// 工单类型详情页：管理该类型的基本信息及专属自定义字段（增删改与拖拽排序）。
export const Route = createFileRoute("/$slug/settings/_service/ticket-types/$id")({
  staticData: { title: "ticket_types.detail_title" },
  // 预取页面的数据，最多等一小会儿：切换到这一页时内容一次完整出现，不先空白再出现
  loader: ({ context, params }) =>
    preloadQueries(context.app.queryClient, [kit.queries.ticket.typeGet(params.id), kit.queries.attribute.list({ target: "ticket", ticket_type_id: params.id })]),
  component: TicketTypeDetailPage,
})

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
      confirmLabel: t("common.delete"),
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

  const onReorder = async (next: AttributeDTO[], previous: AttributeDTO[]) => {
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
      back={() => void navigate({ to: "/$slug/settings/ticket-types", params: { slug } })}
      actions={
        item ? (
          <Group gap="xs">
            <Button variant="default" leftSection={<IconPencil size={14} />} onClick={() => setEditingType(true)}>
              {t("ticket_types.edit")}
            </Button>
            <Button
              variant={isDisabled ? "filled" : "light"}
              color={isDisabled ? undefined : "red"}
              loading={toggling}
              onClick={() => void toggleDisabled()}
            >
              {t(isDisabled ? "ticket_types.enable" : "ticket_types.disable")}
            </Button>
            <Button leftSection={<IconPlus size={14} />} onClick={() => setCreatingField(true)}>
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
                {t("ticket_types.default_visibility")}
              </Text>
              <Badge variant="light" color={item.default_visibility === "all" ? "bright-blue" : "gray"}>
                {kit.label("TicketTypeVisibility", item.default_visibility)}
              </Badge>
            </Group>
            <Group gap={6}>
              <Text size="sm" c="dimmed">
                {t("ticket_types.default_team")}
              </Text>
              <Text size="sm">{item.default_team_name || "—"}</Text>
            </Group>
          </Group>
        )}

        <Text fw={500} size="sm" mt="xs">
          {t("ticket_types.fields_heading")}
        </Text>

        <AttributeListTable
          items={fields}
          loading={fieldsQuery.isPending}
          emptyTitle={t("ticket_types.fields_empty")}
          onEdit={setEditingField}
          onRemove={(attr) => void removeField(attr)}
          onReorder={onReorder}
        />
      </Stack>

      <Modal opened={editingType && !!item} onClose={() => setEditingType(false)} title={t("ticket_types.edit")} size="md" closeOnClickOutside={false}>
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
        closeOnClickOutside={false}
      >
        {(creatingField || editingField) && (
          <AttributeForm
            target="ticket"
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
