import type { AttributeDTO, AttributeTarget } from "@agents-desk/client/tenant"
import { Badge, Button, ColorSwatch, Group, Modal, SegmentedControl, Select, Stack, Text } from "@mantine/core"
import { IconPlus } from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import {
  confirm,
  type FieldInput,
  notifyError,
  notifySuccess,
  Page,
  PageActions,
  preloadQueries,
  preloadResourceList,
  ResourcePage,
  tabbedListSearch,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../api"
import { AttributeForm, AttributeListTable } from "../../../../features/attributes/AttributeEditor"
import { tags } from "../../../../resources"

// 标签与属性：合并标签定义与会话/联系人自定义属性管理两个页内标签，共用 tenant-settings:manage 权限。
export const Route = createFileRoute("/$slug/settings/_workspace/tags")({
  staticData: { title: "nav.tags_attributes", icon: "tags", order: 2, permission: "tenant-settings:manage" },
  validateSearch: tabbedListSearch({ tags: tags.listSearch, attributes: undefined }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => {
    const { tab, ...rest } = deps as Record<string, unknown>
    if (tab === "attributes") {
      return preloadQueries(context.app.queryClient, [kit.queries.attribute.list({ target: "conversation" })])
    }
    return preloadResourceList(context.app.queryClient, tags, rest)
  },
  component: TagsAndAttributesPage,
})

/** ColorInput 是标签的颜色选择器，展示主题预设色点。 */
const ColorInput: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const options = field.options ?? []
  const current = (value as string | undefined) || "blue-gray"
  const swatch = (c: string) => <ColorSwatch color={`var(--mantine-color-${c}-6)`} size={14} />
  return (
    <Select
      label={label}
      data={options.map((o) => ({ value: o.value, label: o.label }))}
      value={current}
      onChange={(v) => onChange?.(v ?? undefined)}
      leftSection={swatch(current)}
      renderOption={({ option }) => (
        <Group gap="xs">
          {swatch(option.value)}
          <Text size="sm">{option.label}</Text>
        </Group>
      )}
      error={error}
      disabled={disabled}
      allowDeselect={false}
    />
  )
}

function TagsAndAttributesPage(): ReactNode {
  const t = useT()

  return (
    <Page
      title={t("nav.tags_attributes")}
      tabs={[
        {
          value: "tags",
          label: t("nav.tags"),
          content: (
            <ResourcePage
              title={null}
              description={t("tags.description")}
              model={tags}
              route={Route}
              form="modal"
              inputs={{ color: ColorInput }}
              render={{
                name: (v, row) => (
                  <Badge color={String(row.color ?? "blue-gray")} variant="light">
                    {String(v)}
                  </Badge>
                ),
              }}
            />
          ),
        },
        {
          value: "attributes",
          label: t("nav.attributes"),
          content: <AttributesTab />,
        },
      ]}
    />
  )
}

/** AttributesTab 管理会话和联系人的自定义属性定义，支持按对象切换和拖动排序。 */
function AttributesTab(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<AttributeTarget>("conversation")
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<AttributeDTO>()
  // 本地拖动顺序与拖拽结束状态同帧提交，保持落位稳定
  const [draftOrder, setDraftOrder] = useState<{ target: AttributeTarget; items: AttributeDTO[] } | null>(null)
  const listQuery = kit.queries.attribute.list({ target })
  const list = useQuery(listQuery)
  const items = draftOrder?.target === target ? draftOrder.items : (list.data ?? [])

  const invalidate = () => queryClient.invalidateQueries({ queryKey: kit.queries.attribute.key() })

  const remove = async (attr: AttributeDTO) => {
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
      await invalidate()
      notifySuccess(t("attributes.deleted"))
    } catch (err) {
      notifyError(err)
    }
  }

  // onReorder 拖动结束后立即更新本地顺序并持久化，失败时回滚
  const onReorder = async (next: AttributeDTO[], previous: AttributeDTO[]) => {
    setDraftOrder({ target, items: next })
    queryClient.setQueryData(listQuery.queryKey, next)
    try {
      const saved = await tenantApi.attribute.reorder({ target, ids: next.map((x) => x.id) })
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
    <Stack gap="sm">
      <PageActions>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
          {t("attributes.add")}
        </Button>
      </PageActions>
      <Text size="sm" c="dimmed">
        {t("attributes.description")}
      </Text>
      <SegmentedControl
        w="fit-content"
        value={target}
        onChange={(v) => setTarget(v as AttributeTarget)}
        data={[
          { value: "conversation", label: t("attributes.target_conversation") },
          { value: "contact", label: t("attributes.target_contact") },
        ]}
      />

      <AttributeListTable
        items={items}
        loading={list.isPending}
        emptyTitle={t("attributes.empty")}
        onEdit={setEditing}
        onRemove={(attr) => void remove(attr)}
        onReorder={onReorder}
      />

      <Modal
        opened={creating || !!editing}
        onClose={() => {
          setCreating(false)
          setEditing(undefined)
        }}
        title={t(editing ? "attributes.edit" : "attributes.add")}
        size="md"
        closeOnClickOutside={false}
      >
        {(creating || editing) && (
          <AttributeForm
            target={editing?.target ?? target}
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
    </Stack>
  )
}
