import type { AttributeDTO, AttributeTarget, AttributeType } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, Button, Code, Group, Modal, SegmentedControl, Select, Stack, Table, TagsInput, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconArrowDown, IconArrowUp, IconPencil, IconPlus, IconTrash } from "@tabler/icons-react"
import { applyServerErrors, confirm, FormError, notifyError, notifySuccess, Page, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"

// 属性：管理会话和联系人的自定义属性定义，按对象分组切换。key 和 type 创建后不可修改。
export const Route = createFileRoute("/t/$slug/settings/_data/attributes")({
  staticData: { title: "nav.attributes", icon: "list", order: 1, permission: "tenant-settings:manage" },
  component: Attributes,
})

/** hasOptions 判断属性类型是否需要配置可选项。 */
const hasOptions = (type: AttributeType): boolean => type === "select" || type === "multi_select"

function Attributes(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<AttributeTarget>("conversation")
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<AttributeDTO>()
  const list = useQuery(kit.queries.attribute.list({ target }))
  const items = list.data ?? []

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
      await invalidate()
      notifySuccess(t("attributes.deleted"))
    } catch (err) {
      notifyError(err)
    }
  }

  // move 通过交换相邻两项的 position 调整展示顺序（位置相同时按当前索引重新编号）
  const move = async (index: number, delta: -1 | 1) => {
    const other = index + delta
    if (other < 0 || other >= items.length) return
    const a = items[index]
    const b = items[other]
    const posA = b.position === a.position ? other : b.position
    const posB = b.position === a.position ? index : a.position
    try {
      await Promise.all([
        tenantApi.attribute.update(a.id, { name: a.name, description: a.description, options: a.options, position: posA }),
        tenantApi.attribute.update(b.id, { name: b.name, description: b.description, options: b.options, position: posB }),
      ])
      await invalidate()
    } catch (err) {
      notifyError(err)
    }
  }

  return (
    <Page
      title={t("attributes.title")}
      description={t("attributes.description")}
      actions={
        <Button leftSection={<IconPlus size={16} />} onClick={() => setCreating(true)}>
          {t("attributes.add")}
        </Button>
      }
    >
      <Stack gap="md">
        <SegmentedControl
          w="fit-content"
          value={target}
          onChange={(v) => setTarget(v as AttributeTarget)}
          data={[
            { value: "conversation", label: t("attributes.target_conversation") },
            { value: "contact", label: t("attributes.target_contact") },
          ]}
        />
        {items.length === 0 ? (
          <Text c="dimmed" ta="center" py="xl">
            {t("attributes.empty")}
          </Text>
        ) : (
          <Table verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th w="22%">{t("attributes.name")}</Table.Th>
                <Table.Th w="20%">{t("attributes.key")}</Table.Th>
                <Table.Th w={100}>{t("attributes.type")}</Table.Th>
                <Table.Th>{t("attributes.details")}</Table.Th>
                <Table.Th w={132} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {items.map((attr, i) => (
                <Table.Tr key={attr.id}>
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
                      <ActionIcon variant="subtle" color="gray" disabled={i === 0} onClick={() => void move(i, -1)} aria-label={t("attributes.move_up")}>
                        <IconArrowUp size={15} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        disabled={i === items.length - 1}
                        onClick={() => void move(i, 1)}
                        aria-label={t("attributes.move_down")}
                      >
                        <IconArrowDown size={15} />
                      </ActionIcon>
                      <ActionIcon variant="subtle" color="gray" onClick={() => setEditing(attr)} aria-label={t("attributes.edit")}>
                        <IconPencil size={15} />
                      </ActionIcon>
                      <ActionIcon variant="subtle" color="red" onClick={() => void remove(attr)} aria-label={t("attributes.delete")}>
                        <IconTrash size={15} />
                      </ActionIcon>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>

      <Modal opened={creating || !!editing} onClose={() => { setCreating(false); setEditing(undefined) }} title={t(editing ? "attributes.edit" : "attributes.add")} size="md">
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
    </Page>
  )
}

/** AttributeForm 是新建或编辑自定义属性定义的表单。编辑时锁定 key 和 type。 */
function AttributeForm({
  target,
  initial,
  onSaved,
  onCancel,
}: {
  target: AttributeTarget
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
