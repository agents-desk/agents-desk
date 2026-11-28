import type {
  AttributeDTO,
  ChannelAudience,
  ConversationAssigneeFilterMode,
  ConversationAttributeFilter,
  ConversationAttributeOp,
  ConversationHandlingMode,
  ConversationPriority,
  ConversationStatus,
  ConversationTagMatch,
  ConversationTeamFilterMode,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Badge,
  Button,
  CloseButton,
  ColorSwatch,
  Group,
  MultiSelect,
  NumberInput,
  SegmentedControl,
  Select,
  Stack,
  Text,
  TextInput,
} from "@mantine/core"
import { IconPlus, IconTrash } from "@tabler/icons-react"
import { useLanguage, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { LanguageSelect } from "../LanguageSelect"
import { languageName } from "../languages"
import { handlingModeColor, useAttributes, useBots, useChannels, useMembers, useTags, useTeams } from "./data"
import type { ConversationFilter } from "./search"

/** FilterFieldsProps 是通用会话筛选条件编辑器的属性（用于收件箱筛选面板与新建/编辑视图弹窗）。 */
export interface FilterFieldsProps {
  value: ConversationFilter
  onChange: (next: ConversationFilter) => void
  compact?: boolean
}

/** opsForType 返回给定会话自定义属性类型支持的操作符列表。 */
function opsForType(type: AttributeDTO["type"]): ConversationAttributeOp[] {
  switch (type) {
    case "text":
      return ["eq", "neq", "contains", "is_set", "is_empty"]
    case "number":
    case "date":
      return ["eq", "neq", "gt", "gte", "lt", "lte", "is_set", "is_empty"]
    case "boolean":
      return ["eq", "neq", "is_set", "is_empty"]
    case "select":
      return ["eq", "neq", "in", "is_set", "is_empty"]
    case "multi_select":
      return ["in", "is_set", "is_empty"]
  }
}

/** defaultValueForType 返回切换属性或操作符后的合理默认比较值。 */
function defaultValueForType(def: AttributeDTO, op: ConversationAttributeOp): unknown {
  if (op === "is_set" || op === "is_empty") return undefined
  switch (def.type) {
    case "boolean":
      return true
    case "select":
      return op === "in" ? (def.options[0] ? [def.options[0]] : []) : (def.options[0] ?? "")
    case "multi_select":
      return def.options[0] ? [def.options[0]] : []
    default:
      return ""
  }
}

/** FilterFields 渲染同构的会话筛选条件表单：状态、回复状态、优先级、团队收件箱、负责人、渠道、标签、客户语言与会话自定义属性。 */
export function FilterFields({ value, onChange, compact }: FilterFieldsProps): ReactNode {
  const t = useT()
  const { list: members } = useMembers()
  const { list: bots } = useBots()
  const teams = useTeams()
  const channels = useChannels()
  const tags = useTags()
  const attrDefs = useAttributes("conversation")

  const tagById = new Map(tags.map((x) => [x.id, x]))
  const attrByKey = new Map(attrDefs.map((x) => [x.key, x]))
  const size = compact ? "xs" : "sm"
  const gap = compact ? "xs" : "sm"

  const patch = (p: Partial<ConversationFilter>) => onChange({ ...value, ...p })

  const teamMode = value.team?.mode ?? "any"
  const assigneeMode = value.assignee?.mode ?? "any"
  const waitingValue = value.waiting === true ? "true" : value.waiting === false ? "false" : "any"
  const priorityValue = value.priority || "any"
  const audienceValue = value.audience || "any"
  const attributes = value.attributes ?? []

  const addAttribute = () => {
    const first = attrDefs[0]
    if (!first) return
    const op = opsForType(first.type)[0]
    const next: ConversationAttributeFilter = {
      key: first.key,
      op,
      value: defaultValueForType(first, op),
    }
    patch({ attributes: [...attributes, next] })
  }

  const updateAttribute = (index: number, next: ConversationAttributeFilter) => {
    const copy = [...attributes]
    copy[index] = next
    patch({ attributes: copy })
  }

  const removeAttribute = (index: number) => {
    const copy = attributes.filter((_, i) => i !== index)
    patch({ attributes: copy.length > 0 ? copy : undefined })
  }

  return (
    <Stack gap={gap}>
      <MultiSelect
        size={size}
        label={t("views.statuses")}
        placeholder={t("views.statuses_all")}
        value={value.statuses ?? []}
        onChange={(v) => patch({ statuses: v.length > 0 ? (v as ConversationStatus[]) : undefined })}
        data={[
          { value: "open", label: t("inbox.status_open") },
          { value: "snoozed", label: t("inbox.status_snoozed") },
          { value: "closed", label: t("inbox.status_closed") },
        ]}
        clearable
        comboboxProps={{ withinPortal: true }}
      />

      <Stack gap={4}>
        <Text size={size} fw={500}>
          {t("views.audience")}
        </Text>
        <SegmentedControl
          size={compact ? "xs" : "sm"}
          fullWidth
          value={audienceValue}
          onChange={(v) => patch({ audience: v === "any" ? undefined : (v as ChannelAudience) })}
          data={[
            { value: "any", label: t("views.any") },
            { value: "customer", label: t("channels.audience_customer") },
            { value: "partner", label: t("channels.audience_partner") },
            { value: "internal", label: t("channels.audience_internal") },
          ]}
        />
      </Stack>

      <Stack gap={4}>
        <Text size={size} fw={500}>
          {t("views.waiting")}
        </Text>
        <SegmentedControl
          size={compact ? "xs" : "sm"}
          fullWidth
          value={waitingValue}
          onChange={(v) => patch({ waiting: v === "true" ? true : v === "false" ? false : undefined })}
          data={[
            { value: "any", label: t("views.any") },
            { value: "true", label: t("views.waiting_true") },
            { value: "false", label: t("views.waiting_false") },
          ]}
        />
      </Stack>

      <Stack gap={4}>
        <Text size={size} fw={500}>
          {t("views.priority")}
        </Text>
        <SegmentedControl
          size={compact ? "xs" : "sm"}
          fullWidth
          value={priorityValue}
          onChange={(v) => patch({ priority: v === "any" ? undefined : (v as ConversationPriority) })}
          data={[
            { value: "any", label: t("views.any") },
            { value: "high", label: t("views.priority_high") },
            { value: "normal", label: t("views.priority_normal") },
          ]}
        />
      </Stack>

      <Stack gap={6}>
        <Select
          size={size}
          label={t("views.team")}
          value={teamMode}
          onChange={(v) => {
            if (!v || v === "any") patch({ team: undefined })
            else if (v === "ids") patch({ team: { mode: "ids", ids: value.team?.mode === "ids" ? value.team.ids : [] } })
            else patch({ team: { mode: v as ConversationTeamFilterMode } })
          }}
          data={[
            { value: "any", label: t("views.any") },
            { value: "my_teams", label: t("views.team_my_teams") },
            { value: "unassigned", label: t("views.team_unassigned") },
            { value: "ids", label: t("views.team_ids") },
          ]}
          allowDeselect={false}
          comboboxProps={{ withinPortal: true }}
        />
        {teamMode === "ids" && (
          <MultiSelect
            size={size}
            placeholder={t("views.team_select")}
            value={value.team?.ids ?? []}
            onChange={(ids) => patch({ team: { mode: "ids", ids } })}
            data={teams.map((tm) => ({
              value: tm.id,
              label: tm.status === "disabled" ? t("inbox.team_disabled_suffix", { name: tm.name }) : tm.name,
            }))}
            searchable
            clearable
            comboboxProps={{ withinPortal: true }}
          />
        )}
      </Stack>

      <Stack gap={6}>
        <Select
          size={size}
          label={t("views.assignee")}
          value={assigneeMode}
          onChange={(v) => {
            if (!v || v === "any") patch({ assignee: undefined })
            else if (v === "member")
              patch({
                assignee: {
                  mode: "member",
                  member_id: value.assignee?.mode === "member" ? value.assignee.member_id : members[0]?.id,
                },
              })
            else patch({ assignee: { mode: v as ConversationAssigneeFilterMode } })
          }}
          data={[
            { value: "any", label: t("views.any") },
            { value: "me", label: t("views.assignee_me") },
            { value: "unassigned", label: t("views.assignee_unassigned") },
            { value: "member", label: t("views.assignee_member") },
            { value: "bot", label: t("views.assignee_bot") },
          ]}
          allowDeselect={false}
          comboboxProps={{ withinPortal: true }}
        />
        {assigneeMode === "member" && (
          <Select
            size={size}
            placeholder={t("views.assignee_select")}
            value={value.assignee?.member_id ?? null}
            onChange={(id) => patch({ assignee: id ? { mode: "member", member_id: id } : { mode: "member" } })}
            data={members.map((m) => ({ value: m.id, label: m.name }))}
            searchable
            comboboxProps={{ withinPortal: true }}
          />
        )}
        {assigneeMode === "bot" && (
          <Select
            size={size}
            placeholder={t("views.assignee_bot_any")}
            value={value.assignee?.bot_id ?? null}
            onChange={(id) => patch({ assignee: id ? { mode: "bot", bot_id: id } : { mode: "bot" } })}
            data={bots.map((b) => ({ value: b.id, label: b.name }))}
            clearable
            searchable
            comboboxProps={{ withinPortal: true }}
          />
        )}
      </Stack>

      <MultiSelect
        size={size}
        label={t("views.handling_mode")}
        placeholder={t("views.handling_mode_placeholder")}
        value={value.handling_modes ?? []}
        onChange={(next) => patch({ handling_modes: next.length > 0 ? (next as ConversationHandlingMode[]) : undefined })}
        data={[
          { value: "human", label: t("inbox.handling_mode_human") },
          { value: "ai_auto", label: t("inbox.handling_mode_ai_auto") },
          { value: "ai_copilot", label: t("inbox.handling_mode_ai_copilot") },
          { value: "ai_paused", label: t("inbox.handling_mode_ai_paused") },
          { value: "waiting_approval", label: t("inbox.handling_mode_waiting_approval") },
        ]}
        comboboxProps={{ withinPortal: true }}
      />

      {channels.length > 0 && (
        <MultiSelect
          size={size}
          label={t("views.channels")}
          placeholder={t("views.channels_placeholder")}
          value={value.channel_ids ?? []}
          onChange={(ids) => patch({ channel_ids: ids.length > 0 ? ids : undefined })}
          data={channels.map((ch) => ({ value: ch.id, label: ch.name }))}
          searchable
          clearable
          comboboxProps={{ withinPortal: true }}
        />
      )}

      {tags.length > 0 && (
        <Stack gap={6}>
          <MultiSelect
            size={size}
            label={t("views.tags")}
            placeholder={t("views.tags_placeholder")}
            value={value.tag_ids ?? []}
            onChange={(ids) =>
              patch({
                tag_ids: ids.length > 0 ? ids : undefined,
                tag_match: ids.length > 0 ? (value.tag_match ?? "any") : undefined,
              })
            }
            data={tags.map((tag) => ({ value: tag.id, label: tag.name }))}
            renderOption={({ option }) => {
              const tag = tagById.get(option.value)
              return (
                <Group gap="xs" wrap="nowrap">
                  {tag && <ColorSwatch color={`var(--mantine-color-${tag.color}-6)`} size={10} />}
                  <Text size={size} truncate>
                    {option.label}
                  </Text>
                </Group>
              )
            }}
            searchable
            clearable
            comboboxProps={{ withinPortal: true }}
          />
          {(value.tag_ids?.length ?? 0) > 1 && (
            <SegmentedControl
              size="xs"
              fullWidth
              value={value.tag_match ?? "any"}
              onChange={(m) => patch({ tag_match: m as ConversationTagMatch })}
              data={[
                { value: "any", label: t("views.tag_match_any") },
                { value: "all", label: t("views.tag_match_all") },
              ]}
            />
          )}
        </Stack>
      )}

      <LanguageSelect
        size={size}
        label={t("views.language")}
        placeholder={t("views.language_placeholder")}
        value={value.language ?? null}
        onChange={(v) => patch({ language: v || undefined })}
        clearable
      />

      {attrDefs.length > 0 && (
        <Stack gap={6}>
          <Group justify="space-between" align="center">
            <Text size={size} fw={500}>
              {t("views.attributes")}
            </Text>
            <Button size="compact-xs" variant="subtle" leftSection={<IconPlus size={12} />} onClick={addAttribute}>
              {t("views.add_attr")}
            </Button>
          </Group>
          {attributes.map((cond, index) => {
            const def = attrByKey.get(cond.key) ?? attrDefs[0]
            const ops = def ? opsForType(def.type) : (["eq"] as ConversationAttributeOp[])
            const needVal = cond.op !== "is_set" && cond.op !== "is_empty"
            return (
              <Stack key={`${cond.key}-${index}`} gap={4} p={6} className="inbox-attr-cond">
                <Group gap={4} wrap="nowrap">
                  <Select
                    size="xs"
                    style={{ flex: 1, minWidth: 0 }}
                    aria-label={t("views.attr_key")}
                    value={cond.key}
                    onChange={(k) => {
                      if (!k) return
                      const nextDef = attrByKey.get(k)
                      if (!nextDef) return
                      const nextOp = opsForType(nextDef.type)[0]
                      updateAttribute(index, { key: k, op: nextOp, value: defaultValueForType(nextDef, nextOp) })
                    }}
                    data={attrDefs.map((d) => ({ value: d.key, label: d.name }))}
                    allowDeselect={false}
                    comboboxProps={{ withinPortal: true }}
                  />
                  <Select
                    size="xs"
                    w={104}
                    aria-label={t("views.attr_op")}
                    value={cond.op}
                    onChange={(op) => {
                      if (!op || !def) return
                      const nextOp = op as ConversationAttributeOp
                      const keep =
                        nextOp !== "is_set" &&
                        nextOp !== "is_empty" &&
                        (nextOp === "in") === (cond.op === "in") &&
                        cond.value !== undefined
                      updateAttribute(index, {
                        key: cond.key,
                        op: nextOp,
                        value: keep ? cond.value : defaultValueForType(def, nextOp),
                      })
                    }}
                    data={ops.map((op) => ({ value: op, label: t(`views.attr_op_${op}` as const) }))}
                    allowDeselect={false}
                    comboboxProps={{ withinPortal: true }}
                  />
                  <ActionIcon
                    size="sm"
                    variant="subtle"
                    color="gray"
                    aria-label={t("views.delete")}
                    onClick={() => removeAttribute(index)}
                  >
                    <IconTrash size={14} />
                  </ActionIcon>
                </Group>
                {needVal && def && (
                  <AttrValueInput
                    def={def}
                    op={cond.op}
                    value={cond.value}
                    onChange={(val) => updateAttribute(index, { ...cond, value: val })}
                  />
                )}
              </Stack>
            )
          })}
        </Stack>
      )}
    </Stack>
  )
}

/** AttrValueInput 按属性定义类型和操作符渲染对应的筛选值控件。 */
function AttrValueInput({
  def,
  op,
  value,
  onChange,
}: {
  def: AttributeDTO
  op: ConversationAttributeOp
  value: unknown
  onChange: (v: unknown) => void
}): ReactNode {
  const t = useT()
  switch (def.type) {
    case "number":
      return (
        <NumberInput
          size="xs"
          aria-label={t("views.attr_value")}
          value={typeof value === "number" ? value : ""}
          onChange={(v) => onChange(v === "" ? "" : Number(v))}
        />
      )
    case "boolean":
      return (
        <SegmentedControl
          size="xs"
          fullWidth
          value={value === false ? "false" : "true"}
          onChange={(v) => onChange(v === "true")}
          data={[
            { value: "true", label: t("views.attr_true") },
            { value: "false", label: t("views.attr_false") },
          ]}
        />
      )
    case "date":
      return (
        <TextInput
          size="xs"
          type="date"
          aria-label={t("views.attr_value")}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.currentTarget.value)}
        />
      )
    case "select":
      if (op === "in") {
        return (
          <MultiSelect
            size="xs"
            aria-label={t("views.attr_value")}
            value={Array.isArray(value) ? value.map(String) : []}
            onChange={(vals) => onChange(vals)}
            data={def.options}
            comboboxProps={{ withinPortal: true }}
          />
        )
      }
      return (
        <Select
          size="xs"
          aria-label={t("views.attr_value")}
          value={typeof value === "string" ? value : null}
          onChange={(v) => onChange(v ?? "")}
          data={def.options}
          comboboxProps={{ withinPortal: true }}
        />
      )
    case "multi_select":
      return (
        <MultiSelect
          size="xs"
          aria-label={t("views.attr_value")}
          value={Array.isArray(value) ? value.map(String) : []}
          onChange={(vals) => onChange(vals)}
          data={def.options}
          comboboxProps={{ withinPortal: true }}
        />
      )
    default:
      return (
        <TextInput
          size="xs"
          aria-label={t("views.attr_value")}
          placeholder={t("views.attr_value")}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.currentTarget.value)}
        />
      )
  }
}

/** FilterSummaryChips 渲染当前生效的筛选条件摘要胶囊，支持逐项点击移除。 */
export function FilterSummaryChips({
  filter,
  onChange,
}: {
  filter: ConversationFilter
  onChange: (next: ConversationFilter) => void
}): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const { byId: memberById } = useMembers()
  const { byId: botById } = useBots()
  const teams = useTeams()
  const channels = useChannels()
  const tags = useTags()
  const attrDefs = useAttributes("conversation")

  const teamById = new Map(teams.map((x) => [x.id, x]))
  const channelById = new Map(channels.map((x) => [x.id, x]))
  const tagById = new Map(tags.map((x) => [x.id, x]))
  const attrByKey = new Map(attrDefs.map((x) => [x.key, x]))

  const chips: { key: string; label: string; color?: string; remove: () => void }[] = []

  if (filter.statuses && filter.statuses.length > 0) {
    chips.push({
      key: "statuses",
      label: filter.statuses.map((s) => t(`inbox.status_${s}` as const)).join(" / "),
      remove: () => onChange({ ...filter, statuses: undefined }),
    })
  }
  if (filter.audience) {
    const audienceLabel =
      filter.audience === "internal"
        ? "channels.audience_internal"
        : filter.audience === "partner"
          ? "channels.audience_partner"
          : "channels.audience_customer"
    const audienceColor = filter.audience === "internal" ? "green" : filter.audience === "partner" ? "brand" : "bright-blue"
    chips.push({
      key: "audience",
      label: `${t("views.audience")}: ${t(audienceLabel)}`,
      color: audienceColor,
      remove: () => onChange({ ...filter, audience: undefined }),
    })
  }
  if (typeof filter.waiting === "boolean") {
    chips.push({
      key: "waiting",
      label: t(filter.waiting ? "views.waiting_true" : "views.waiting_false"),
      color: filter.waiting ? "deep-orange" : undefined,
      remove: () => onChange({ ...filter, waiting: undefined }),
    })
  }
  if (filter.priority) {
    chips.push({
      key: "priority",
      label: `${t("views.priority")}: ${t(filter.priority === "high" ? "views.priority_high" : "views.priority_normal")}`,
      color: filter.priority === "high" ? "red" : undefined,
      remove: () => onChange({ ...filter, priority: undefined }),
    })
  }
  if (filter.team) {
    const label =
      filter.team.mode === "my_teams"
        ? t("views.team_my_teams")
        : filter.team.mode === "unassigned"
          ? t("views.team_unassigned")
          : (filter.team.ids ?? []).map((id) => teamById.get(id)?.name ?? id.slice(0, 6)).join(", ")
    chips.push({
      key: "team",
      label: `${t("views.team")}: ${label}`,
      remove: () => onChange({ ...filter, team: undefined }),
    })
  }
  if (filter.handling_modes && filter.handling_modes.length > 0) {
    chips.push({
      key: "handling_modes",
      label: filter.handling_modes.map((m) => t(`inbox.handling_mode_${m}` as const)).join(" / "),
      color: filter.handling_modes.length === 1 ? handlingModeColor(filter.handling_modes[0]) : undefined,
      remove: () => onChange({ ...filter, handling_modes: undefined }),
    })
  }
  if (filter.assignee) {
    const label =
      filter.assignee.mode === "me"
        ? t("views.assignee_me")
        : filter.assignee.mode === "unassigned"
          ? t("views.assignee_unassigned")
          : filter.assignee.mode === "bot"
            ? filter.assignee.bot_id
              ? (botById.get(filter.assignee.bot_id)?.name ?? t("views.assignee_bot"))
              : t("views.assignee_bot_any")
            : (memberById.get(filter.assignee.member_id ?? "")?.name ?? "…")
    chips.push({
      key: "assignee",
      label: `${t("views.assignee")}: ${label}`,
      remove: () => onChange({ ...filter, assignee: undefined }),
    })
  }
  if (filter.channel_ids && filter.channel_ids.length > 0) {
    chips.push({
      key: "channels",
      label: `${t("views.channels")}: ${filter.channel_ids.map((id) => channelById.get(id)?.name ?? id.slice(0, 6)).join(", ")}`,
      remove: () => onChange({ ...filter, channel_ids: undefined }),
    })
  }
  if (filter.tag_ids && filter.tag_ids.length > 0) {
    const names = filter.tag_ids.map((id) => tagById.get(id)?.name ?? id.slice(0, 6)).join(filter.tag_match === "all" ? " + " : " / ")
    chips.push({
      key: "tags",
      label: `${t("views.tags")}: ${names}`,
      remove: () => onChange({ ...filter, tag_ids: undefined, tag_match: undefined }),
    })
  }
  if (filter.language) {
    chips.push({
      key: "language",
      label: `${t("views.language")}: ${languageName(filter.language, uiLang)}`,
      remove: () => onChange({ ...filter, language: undefined }),
    })
  }
  for (const [index, cond] of (filter.attributes ?? []).entries()) {
    const def = attrByKey.get(cond.key)
    const name = def?.name ?? cond.key
    const opLabel = t(`views.attr_op_${cond.op}` as const)
    let valText = ""
    if (cond.op !== "is_set" && cond.op !== "is_empty") {
      if (typeof cond.value === "boolean") valText = t(cond.value ? "views.attr_true" : "views.attr_false")
      else if (Array.isArray(cond.value)) valText = cond.value.join(", ")
      else if (cond.value !== undefined) valText = String(cond.value)
    }
    chips.push({
      key: `attr-${cond.key}-${index}`,
      label: valText ? `${name} ${opLabel} ${valText}` : `${name} ${opLabel}`,
      remove: () => {
        const rest = (filter.attributes ?? []).filter((_, i) => i !== index)
        onChange({ ...filter, attributes: rest.length > 0 ? rest : undefined })
      },
    })
  }

  if (chips.length === 0) return null
  return (
    <Group gap={4} wrap="wrap">
      {chips.map((c) => (
        <Badge
          key={c.key}
          size="xs"
          variant="light"
          color={c.color ?? "gray"}
          pr={3}
          rightSection={
            <CloseButton
              size={14}
              iconSize={10}
              variant="transparent"
              aria-label={t("views.delete")}
              onClick={c.remove}
            />
          }
          styles={{ label: { textTransform: "none", fontWeight: 500 } }}
        >
          {c.label}
        </Badge>
      ))}
    </Group>
  )
}
