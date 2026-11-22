import { Badge, ColorSwatch, Group, Select, Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { type FieldInput, ResourcePage, useT } from "@go-tenon/ui"
import { tags } from "../../../../../resources"

// 标签：有 tenant-settings:manage 权限的成员新建、修改、删除标签；会话和联系人共用同一套标签定义。
export const Route = createFileRoute("/t/$slug/settings/_data/tags")({
  staticData: { title: "nav.tags", icon: "tags", order: 0, permission: "tenant-settings:manage" },
  validateSearch: tags.listSearch,
  component: Tags,
})

// ColorInput 是标签的颜色选择器：从主题预设的 11 种颜色中选，每项前面显示色点。
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

function Tags() {
  const t = useT()
  return (
    <ResourcePage
      model={tags}
      route={Route}
      form="modal"
      description={t("tags.description")}
      inputs={{ color: ColorInput }}
      render={{
        name: (v, row) => (
          <Badge color={String(row.color ?? "blue-gray")} variant="light">
            {String(v)}
          </Badge>
        ),
      }}
    />
  )
}
