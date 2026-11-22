import { Anchor, Badge, ColorSwatch, Group, Select, Text } from "@mantine/core"
import { createFileRoute, Link } from "@tanstack/react-router"
import { type FieldInput, ResourcePage, ResourceSelect, useT } from "@go-tenon/ui"
import { teams, widgets } from "../../../../../../resources"

// 网页聊天插件：有 channels:manage 权限的成员新建、修改、停用聊天插件；点名称进入聊天插件，拿到嵌入代码和身份验证密钥。
export const Route = createFileRoute("/t/$slug/settings/_channels/widgets/")({
  validateSearch: widgets.listSearch,
  component: Widgets,
})

// TeamInput 是新会话进入的团队收件箱：从启用的团队收件箱中选，可以清空（进入未指派）；如果原渠道绑定的团队已停用，保留回显并标明已停用。
const TeamInput: FieldInput = ({ label, value, onChange, error, disabled }) => {
  const t = useT()
  const current = (value as string | undefined) ?? null
  return (
    <ResourceSelect
      model={teams}
      label={label}
      description={t("widgets.team_hint")}
      placeholder={t("widgets.team_unassigned")}
      filterRow={(row) => row.status === "active" || String(row.id) === String(current ?? "")}
      getOption={(row) => ({
        value: String(row.id),
        label: row.status === "disabled" ? t("widgets.team_disabled_suffix", { name: String(row.name ?? "") }) : String(row.name ?? ""),
      })}
      value={current}
      onChange={(v) => onChange?.(v ?? undefined)}
      error={error}
      disabled={disabled}
      clearable
    />
  )
}

// ColorInput 是聊天窗口的主题色：界面主题中预设的色板，每项前面是色块。
const ColorInput: FieldInput = ({ field, label, value, onChange, error, disabled }) => {
  const swatch = (c: string) => <ColorSwatch color={`var(--mantine-color-${c}-6)`} size={14} />
  const current = (value as string | undefined) ?? "bright-blue"
  return (
    <Select
      label={label}
      data={(field.options ?? []).map((o) => o.value)}
      value={current}
      onChange={(v) => onChange?.(v ?? undefined)}
      leftSection={swatch(current)}
      renderOption={({ option }) => (
        <Group gap="xs">
          {swatch(option.value)}
          <Text size="sm">{option.value}</Text>
        </Group>
      )}
      error={error}
      disabled={disabled}
      allowDeselect={false}
    />
  )
}

function Widgets() {
  const { slug } = Route.useParams()
  const t = useT()
  return (
    <ResourcePage
      model={widgets}
      route={Route}
      form="modal"
      inputs={{ team_id: TeamInput, color: ColorInput }}
      render={{
        name: (v, row) => (
          <Anchor
            renderRoot={(props) => <Link {...props} to="/t/$slug/settings/widgets/$id" params={{ slug, id: String(row.id) }} />}
          >
            {String(v)}
          </Anchor>
        ),
        team_name: (v) => (v ? String(v) : <Text c="dimmed" size="sm">{t("widgets.team_unassigned")}</Text>),
        enabled: (v) => (
          <Badge variant="dot" color={v ? "green" : "gray"}>
            {t(v ? "widgets.enabled" : "widgets.disabled")}
          </Badge>
        ),
      }}
    />
  )
}
