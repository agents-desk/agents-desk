import type {
  AttributeDTO,
  ConversationPriority,
  MacroActions,
  MacroCreateParams,
  MacroDTO,
  MacroScope,
  MacroSnooze,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Divider,
  Group,
  Menu,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core"
import { IconBraces, IconPlus, IconTrash } from "@tabler/icons-react"
import {
  FormError,
  ResourceFormButtons,
  type ResourceFormContext,
  ResourceMultiSelect,
  ResourceSelect,
  TriggerMenu,
  textTriggerRule,
  useCan,
  useResourceForm,
  useT,
  useTextTrigger,
} from "@go-tenon/ui"
import { type ReactNode, useMemo, useRef } from "react"
import { useAttributes } from "../inbox/data"
import { AttributeInput } from "../inbox/fields"
import { members, tags, teams } from "../resources"

type TeamMode = "keep" | "set" | "clear"
type AssigneeMode = "keep" | "me" | "member" | "clear"
type PriorityMode = "keep" | ConversationPriority
type StatusMode = "keep" | "close" | "snooze"

interface FormState {
  scope: MacroScope
  title: string
  shortcut: string
  body: string
  teamMode: TeamMode
  teamId: string
  assigneeMode: AssigneeMode
  assigneeId: string
  priority: PriorityMode
  addTagIds: string[]
  setAttributes: Record<string, unknown>
  statusMode: StatusMode
  snooze: MacroSnooze
}

function toInitial(row: MacroDTO | undefined, canShared: boolean): FormState {
  const act = row?.actions
  const assigneeMode = act?.assignee?.mode
  return {
    scope: row?.scope ?? (canShared ? "shared" : "personal"),
    title: row?.title ?? "",
    shortcut: row?.shortcut ?? "",
    body: row?.body ?? "",
    teamMode: act?.team?.mode ?? "keep",
    teamId: act?.team?.id ?? "",
    assigneeMode: assigneeMode === "me" || assigneeMode === "member" || assigneeMode === "clear" ? assigneeMode : "keep",
    assigneeId: act?.assignee?.id ?? "",
    priority: act?.priority ?? "keep",
    addTagIds: act?.add_tag_ids ?? [],
    setAttributes: act?.set_attributes ? { ...act.set_attributes } : {},
    statusMode: act?.status?.kind ?? "keep",
    snooze: act?.status?.snooze ?? "tomorrow",
  }
}

function toActionsPayload(v: FormState): MacroActions {
  const actions: MacroActions = {}
  if (v.teamMode === "set" && v.teamId) {
    actions.team = { mode: "set", id: v.teamId }
  } else if (v.teamMode === "clear") {
    actions.team = { mode: "clear" }
  }
  if (v.assigneeMode === "member" && v.assigneeId) {
    actions.assignee = { mode: "member", id: v.assigneeId }
  } else if (v.assigneeMode === "me" || v.assigneeMode === "clear") {
    actions.assignee = { mode: v.assigneeMode }
  }
  if (v.priority !== "keep") {
    actions.priority = v.priority
  }
  if (v.addTagIds.length > 0) {
    actions.add_tag_ids = v.addTagIds
  }
  if (Object.keys(v.setAttributes).length > 0) {
    actions.set_attributes = v.setAttributes
  }
  if (v.statusMode === "close") {
    actions.status = { kind: "close" }
  } else if (v.statusMode === "snooze") {
    actions.status = { kind: "snooze", snooze: v.snooze }
  }
  return actions
}

/**
 * MacroForm 是快捷回复的新建/修改表单，放进 ResourcePage 的抽屉容器中：
 * 正文区接入框架的 useTextTrigger + TriggerMenu（输入 {{ 自动补全联系人和成员变量），
 * 附带操作区复用框架 ResourceSelect / ResourceMultiSelect 以及会话自定义属性输入组件。
 */
export function MacroForm({ ctx }: { ctx: ResourceFormContext }): ReactNode {
  const t = useT()
  const can = useCan()
  const canShared = can("macros:manage")
  const contactAttrs = useAttributes("contact")
  const convAttrs = useAttributes("conversation")
  const bodyRef = useRef<HTMLTextAreaElement>(null)
  const existing = ctx.row as MacroDTO | undefined
  const isEdit = Boolean(ctx.editKey)
  const fieldLabel = (name: string) =>
    (isEdit ? ctx.model.updateForm : ctx.model.createForm).find((f) => f.name === name)?.label ??
    ctx.model.columns.find((f) => f.name === name)?.label ??
    name

  const { form, error, submit, saving } = useResourceForm<FormState>(ctx, {
    initialValues: toInitial(existing, canShared),
    toBody: (v) => {
      const actions = toActionsPayload(v)
      if (isEdit) {
        return {
          title: v.title,
          shortcut: v.shortcut,
          body: v.body,
          actions,
        }
      }
      const createBody: MacroCreateParams = {
        scope: canShared ? v.scope : "personal",
        title: v.title,
        shortcut: v.shortcut || undefined,
        body: v.body || undefined,
        actions,
      }
      return createBody as unknown as Record<string, unknown>
    },
  })

  // 可插入的变量列表：联系人内置字段 + 联系人自定义属性 + 当前成员名字
  const templateVars = useMemo(() => {
    const base = [
      { key: "contact.name", label: t("macros.var_contact_name") },
      { key: "contact.email", label: t("macros.var_contact_email") },
      { key: "contact.location", label: t("macros.var_contact_location") },
      { key: "member.name", label: t("macros.var_member_name") },
    ]
    const extra = contactAttrs.map((a) => ({ key: `contact.attr.${a.key}`, label: a.name }))
    return [...base, ...extra]
  }, [t, contactAttrs])

  const rules = useMemo(
    () => [
      textTriggerRule({
        id: "var",
        trigger: "{{",
        boundary: "any",
        stop: "}}",
        items: (q) => {
          const s = q.toLowerCase()
          return templateVars.filter((v) => v.key.toLowerCase().includes(s) || v.label.toLowerCase().includes(s))
        },
        getKey: (v) => v.key,
        renderItem: (v) => (
          <Group justify="space-between" wrap="nowrap" gap="xs">
            <Text size="xs" ff="monospace" truncate>
              {`{{${v.key}}}`}
            </Text>
            <Text size="xs" c="dimmed" truncate>
              {v.label}
            </Text>
          </Group>
        ),
        onSelect: (v, { replace }) => replace(`{{${v.key}}}`),
      }),
    ],
    [templateVars],
  )

  const trigger = useTextTrigger({
    ref: bodyRef,
    value: form.values.body,
    onChange: (next) => form.setFieldValue("body", next),
    rules,
  })

  const convAttrMap = useMemo(() => new Map<string, AttributeDTO>(convAttrs.map((a) => [a.key, a])), [convAttrs])
  const selectedAttrKeys = Object.keys(form.values.setAttributes)
  const availableConvAttrs = convAttrs.filter((a) => !(a.key in form.values.setAttributes))

  const addConvAttr = (def: AttributeDTO) => {
    const initialValue = def.type === "boolean" ? false : def.type === "multi_select" ? [] : ""
    form.setFieldValue("setAttributes", { ...form.values.setAttributes, [def.key]: initialValue })
  }
  const updateConvAttr = (key: string, next: unknown) => {
    form.setFieldValue("setAttributes", { ...form.values.setAttributes, [key]: next })
  }
  const removeConvAttr = (key: string) => {
    const copy = { ...form.values.setAttributes }
    delete copy[key]
    form.setFieldValue("setAttributes", copy)
  }

  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />

        {/* 作用域：新建时按权限选择共享/个人，修改时只读 */}
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {fieldLabel("scope")}
          </Text>
          {isEdit || !canShared ? (
            <Group gap="xs">
              <Badge variant="light" color={form.values.scope === "shared" ? "brand" : "gray"}>
                {t(form.values.scope === "shared" ? "inbox.macro_scope_shared" : "inbox.macro_scope_personal")}
              </Badge>
              <Text size="xs" c="dimmed">
                {t(isEdit ? "macros.scope_locked" : "macros.scope_hint_personal_only")}
              </Text>
            </Group>
          ) : (
            <SegmentedControl
              w="fit-content"
              value={form.values.scope}
              onChange={(v) => form.setFieldValue("scope", v as MacroScope)}
              data={[
                { value: "shared", label: t("inbox.macro_scope_shared") },
                { value: "personal", label: t("inbox.macro_scope_personal") },
              ]}
            />
          )}
        </Stack>

        <Group grow align="flex-start">
          <TextInput
            label={fieldLabel("title")}
            required
            placeholder={t("macros.title_placeholder")}
            maxLength={100}
            {...form.getInputProps("title")}
          />
          <TextInput
            label={fieldLabel("shortcut")}
            description={t("macros.shortcut_hint")}
            inputWrapperOrder={["label", "input", "description", "error"]}
            placeholder={t("macros.shortcut_placeholder")}
            leftSection={
              <Text size="sm" c="dimmed" ff="monospace">
                /
              </Text>
            }
            maxLength={32}
            {...form.getInputProps("shortcut")}
          />
        </Group>

        {/* 回复正文 + 内联 {{ 变量补全 */}
        <Stack gap={4}>
          <Group justify="space-between" align="center">
            <Text size="sm" fw={500}>
              {fieldLabel("body")}
            </Text>
            <Button
              size="compact-xs"
              variant="subtle"
              leftSection={<IconBraces size={14} />}
              onClick={() => trigger.open("var")}
            >
              {t("macros.insert_var")}
            </Button>
          </Group>
          <Box pos="relative">
            <TriggerMenu trigger={trigger} position="bottom" width={300} empty={t("macros.var_empty")} />
            <Textarea
              ref={bodyRef}
              aria-label={fieldLabel("body")}
              value={form.values.body}
              onChange={(e) => trigger.handleChange(e.currentTarget.value, e.currentTarget.selectionStart)}
              onKeyDown={(e) => {
                trigger.handleKeyDown(e)
              }}
              placeholder={t("macros.body_placeholder")}
              description={t("macros.body_hint")}
              error={form.errors.body}
              autosize
              minRows={4}
              maxRows={10}
              maxLength={10000}
            />
          </Box>
        </Stack>

        <Divider label={t("macros.action_section")} labelPosition="left" />
        <Text size="xs" c="dimmed" mt={-8}>
          {t("macros.action_section_hint")}
        </Text>

        {/* 附带操作配置 */}
        <Paper withBorder radius="md" p="sm">
          <Stack gap="sm">
            {/* 指派团队收件箱 */}
            <Group grow align="flex-start">
              <Select
                label={t("macros.action_team")}
                allowDeselect={false}
                value={form.values.teamMode}
                onChange={(v) => {
                  const next = (v ?? "keep") as TeamMode
                  form.setFieldValue("teamMode", next)
                  if (next !== "set") form.setFieldValue("teamId", "")
                }}
                data={[
                  { value: "keep", label: t("macros.action_team_keep") },
                  { value: "set", label: t("macros.action_team_set") },
                  { value: "clear", label: t("macros.action_team_clear") },
                ]}
              />
              {form.values.teamMode === "set" && (
                <ResourceSelect
                  model={teams}
                  label={t("macros.action_team_select")}
                  filterRow={(row) => row.status !== "disabled"}
                  value={form.values.teamId || null}
                  onChange={(v) => form.setFieldValue("teamId", v ?? "")}
                  error={form.errors["actions.team.id"]}
                />
              )}
            </Group>

            {/* 指派负责人 */}
            <Group grow align="flex-start">
              <Select
                label={t("macros.action_assignee")}
                allowDeselect={false}
                value={form.values.assigneeMode}
                onChange={(v) => {
                  const next = (v ?? "keep") as AssigneeMode
                  form.setFieldValue("assigneeMode", next)
                  if (next !== "member") form.setFieldValue("assigneeId", "")
                }}
                data={[
                  { value: "keep", label: t("macros.action_assignee_keep") },
                  { value: "me", label: t("macros.action_assignee_me") },
                  { value: "member", label: t("macros.action_assignee_member") },
                  { value: "clear", label: t("macros.action_assignee_clear") },
                ]}
              />
              {form.values.assigneeMode === "member" && (
                <ResourceSelect
                  model={members}
                  label={t("macros.action_assignee_select")}
                  value={form.values.assigneeId || null}
                  onChange={(v) => form.setFieldValue("assigneeId", v ?? "")}
                  error={form.errors["actions.assignee.id"]}
                />
              )}
            </Group>

            {/* 优先级 + 添加标签 */}
            <Group grow align="flex-start">
              <Select
                label={t("macros.action_priority")}
                allowDeselect={false}
                value={form.values.priority}
                onChange={(v) => form.setFieldValue("priority", (v ?? "keep") as PriorityMode)}
                data={[
                  { value: "keep", label: t("macros.action_priority_keep") },
                  { value: "normal", label: t("inbox.priority_normal") },
                  { value: "high", label: t("inbox.priority_high") },
                ]}
              />
              <ResourceMultiSelect
                model={tags}
                label={t("macros.action_tags")}
                placeholder={t("macros.action_tags_placeholder")}
                value={form.values.addTagIds}
                onChange={(v) => form.setFieldValue("addTagIds", v)}
                error={form.errors["actions.add_tag_ids"]}
              />
            </Group>

            {/* 设置会话自定义属性 */}
            <Stack gap="xs">
              <Group justify="space-between" align="center">
                <Text size="sm" fw={500}>
                  {t("macros.action_attributes")}
                </Text>
                {availableConvAttrs.length > 0 && (
                  <Menu position="bottom-end" withinPortal>
                    <Menu.Target>
                      <Button size="compact-xs" variant="subtle" leftSection={<IconPlus size={14} />}>
                        {t("macros.action_add_attr")}
                      </Button>
                    </Menu.Target>
                    <Menu.Dropdown>
                      {availableConvAttrs.map((a) => (
                        <Menu.Item key={a.id} onClick={() => addConvAttr(a)}>
                          {a.name}
                        </Menu.Item>
                      ))}
                    </Menu.Dropdown>
                  </Menu>
                )}
              </Group>
              {convAttrs.length === 0 ? (
                <Text size="xs" c="dimmed">
                  {t("macros.action_attributes_empty")}
                </Text>
              ) : (
                selectedAttrKeys.map((key) => {
                  const def = convAttrMap.get(key)
                  if (!def) return null
                  return (
                    <Group key={key} gap="xs" wrap="nowrap" align="center">
                      <Text size="xs" fw={500} w={110} truncate>
                        {def.name}
                      </Text>
                      <Box style={{ flex: 1 }}>
                        <AttributeInput
                          def={def}
                          value={form.values.setAttributes[key]}
                          onSave={(next) => updateConvAttr(key, next)}
                        />
                      </Box>
                      <ActionIcon
                        size="sm"
                        variant="subtle"
                        color="gray"
                        aria-label={t("inbox.macro_action_remove")}
                        onClick={() => removeConvAttr(key)}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    </Group>
                  )
                })
              )}
            </Stack>

            {/* 会话状态：关闭 / 稍后处理 */}
            <Group grow align="flex-start">
              <Select
                label={t("macros.action_status")}
                allowDeselect={false}
                value={form.values.statusMode}
                onChange={(v) => form.setFieldValue("statusMode", (v ?? "keep") as StatusMode)}
                data={[
                  { value: "keep", label: t("macros.action_status_keep") },
                  { value: "close", label: t("macros.action_status_close") },
                  { value: "snooze", label: t("macros.action_status_snooze") },
                ]}
              />
              {form.values.statusMode === "snooze" && (
                <Select
                  label={t("macros.action_snooze_preset")}
                  allowDeselect={false}
                  value={form.values.snooze}
                  onChange={(v) => form.setFieldValue("snooze", (v ?? "tomorrow") as MacroSnooze)}
                  data={[
                    { value: "later", label: t("inbox.snooze_later") },
                    { value: "tomorrow", label: t("inbox.snooze_tomorrow") },
                    { value: "monday", label: t("inbox.snooze_monday") },
                    { value: "week", label: t("inbox.snooze_week") },
                    { value: "month", label: t("inbox.snooze_month") },
                  ]}
                />
              )}
            </Group>
          </Stack>
        </Paper>

        <ResourceFormButtons ctx={ctx} saving={saving} />
      </Stack>
    </form>
  )
}
