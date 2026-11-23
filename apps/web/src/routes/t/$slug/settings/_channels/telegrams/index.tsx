import { Anchor, Badge, Text } from "@mantine/core"
import { createFileRoute, Link } from "@tanstack/react-router"
import { type FieldInput, ResourcePage, ResourceSelect, useT } from "@go-tenon/ui"
import { teams, telegrams } from "../../../../../../resources"

// Telegram 机器人渠道列表：有 channels:manage 权限的成员新建、修改、停用渠道；点名称进入接入配置页查看 Webhook 地址和密钥。
export const Route = createFileRoute("/t/$slug/settings/_channels/telegrams/")({
  validateSearch: telegrams.listSearch,
  component: Telegrams,
})

// TeamInput 是新会话进入的团队收件箱：从启用的团队收件箱中选，可以清空（进入未指派）；如果原渠道绑定的团队已停用，保留回显并标明已停用。
const TeamInput: FieldInput = ({ label, value, onChange, error, disabled }) => {
  const t = useT()
  const current = (value as string | undefined) ?? null
  return (
    <ResourceSelect
      model={teams}
      label={label}
      description={t("telegrams.team_hint")}
      placeholder={t("telegrams.team_unassigned")}
      filterRow={(row) => row.status === "active" || String(row.id) === String(current ?? "")}
      getOption={(row) => ({
        value: String(row.id),
        label: row.status === "disabled" ? t("telegrams.team_disabled_suffix", { name: String(row.name ?? "") }) : String(row.name ?? ""),
      })}
      value={current}
      onChange={(v) => onChange?.(v ?? undefined)}
      error={error}
      disabled={disabled}
      clearable
    />
  )
}

function Telegrams() {
  const { slug } = Route.useParams()
  const t = useT()
  return (
    <ResourcePage
      model={telegrams}
      route={Route}
      form="modal"
      inputs={{ team_id: TeamInput }}
      render={{
        name: (v, row) => (
          <Anchor
            renderRoot={(props) => <Link {...props} to="/t/$slug/settings/telegrams/$id" params={{ slug, id: String(row.id) }} />}
          >
            {String(v)}
          </Anchor>
        ),
        bot_username: (v) => (v ? <Text size="sm">@{String(v)}</Text> : <Text c="dimmed" size="sm">—</Text>),
        team_name: (v) => (v ? String(v) : <Text c="dimmed" size="sm">{t("telegrams.team_unassigned")}</Text>),
        gateway_managed: (v) => (
          <Badge variant="light" color={v ? "bright-blue" : "gray"}>
            {t(v ? "telegrams.mode_gateway" : "telegrams.mode_direct")}
          </Badge>
        ),
        enabled: (v) => (
          <Badge variant="dot" color={v ? "green" : "gray"}>
            {t(v ? "telegrams.enabled" : "telegrams.disabled")}
          </Badge>
        ),
      }}
    />
  )
}
