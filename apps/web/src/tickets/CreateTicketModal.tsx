import type { ConversationDTO, ConversationTicketDTO, TicketTypeVisibility } from "@agents-desk/client/tenant"
import { Button, Group, Modal, Paper, SegmentedControl, Select, Stack, Text, Textarea, TextInput } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconChevronDown, IconChevronRight, IconTicket } from "@tabler/icons-react"
import { notifyError, notifySuccess, useCan, useT } from "@go-tenon/ui"
import { type FormEvent, type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { contactDisplayName } from "../contacts/display"
import { useMe, useMembers, useTeams } from "../inbox/data"
import { AttributeInput } from "../inbox/fields"
import { useTicketAttributes, useTicketTypes } from "./meta"

/** CreateTicketModalProps 是新建或由会话转成工单弹窗的设置。 */
interface CreateTicketModalProps {
  opened: boolean
  onClose: () => void
  sourceConversation?: ConversationDTO // 传入时将作为来源会话建立受影响客户关联
  onCreated?: (ticket: ConversationTicketDTO) => void
}

/**
 * CreateTicketModal 是新建工单 / 由服务型会话转成工单的弹窗：
 * 默认只呈现标题与描述，实现零配置极速建单；展开更多选项后可按需选择工单类型（带出默认团队、可见范围与专属字段）、
 * 团队收件箱、负责人、优先级和可见范围。
 */
export function CreateTicketModal({ opened, onClose, sourceConversation, onCreated }: CreateTicketModalProps): ReactNode {
  const t = useT()
  const can = useCan()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams().filter((x) => x.status === "active")
  const types = useTicketTypes(true)

  const [typeId, setTypeId] = useState<string | null>(null)
  const [title, setTitle] = useState("")
  const [description, setDescription] = useState("")
  const [visibility, setVisibility] = useState<TicketTypeVisibility>("all")
  const [priority, setPriority] = useState<"normal" | "high">("normal")
  const [teamId, setTeamId] = useState<string | null>(null)
  const [assigneeId, setAssigneeId] = useState<string | null>(null)
  const [attributes, setAttributes] = useState<Record<string, unknown>>({})
  const [moreOpen, setMoreOpen] = useState(false)
  const [titleError, setTitleError] = useState<string | null>(null)

  const typeAttrs = useTicketAttributes(typeId ?? undefined)

  // 每次打开弹窗重置为空白通用工单表单
  useEffect(() => {
    if (!opened) return
    setTypeId(null)
    setVisibility("all")
    setTeamId(null)
    setAssigneeId(null)
    setPriority(sourceConversation?.priority ?? "normal")
    setTitle("")
    setDescription("")
    setAttributes({})
    setMoreOpen(false)
    setTitleError(null)
  }, [opened, sourceConversation?.priority])

  const handleTypeChange = (nextId: string | null) => {
    setTypeId(nextId)
    setAttributes({})
    if (!nextId) return
    const found = types.find((x) => x.id === nextId)
    if (found) {
      setVisibility(found.default_visibility)
      setTeamId(found.default_team_id ?? null)
    }
  }

  const create = useMutation({
    mutationFn: () => {
      const cleanedAttrs: Record<string, unknown> = {}
      if (typeId) {
        for (const [k, v] of Object.entries(attributes)) {
          if (v !== null && v !== undefined && v !== "") cleanedAttrs[k] = v
        }
      }
      return tenantApi.ticket.create({
        type_id: typeId || undefined,
        title: title.trim(),
        description: description.trim() || undefined,
        visibility,
        priority,
        team_id: teamId ?? undefined,
        assignee_id: assigneeId ?? undefined,
        source_conversation_id: sourceConversation?.id,
        attributes: Object.keys(cleanedAttrs).length > 0 ? cleanedAttrs : undefined,
      })
    },
    onSuccess: (created) => {
      notifySuccess(t("tickets.created", { number: created.number }))
      void queryClient.invalidateQueries({ queryKey: kit.queries.ticket.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      onClose()
      onCreated?.(created)
    },
    onError: (err) => notifyError(err),
  })

  const assignees = can("conversations:assign") ? members : members.filter((m) => m.id === me.data?.id)

  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    if (!title.trim()) {
      setTitleError(t("tickets.title_required"))
      return
    }
    setTitleError(null)
    create.mutate()
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={t(sourceConversation ? "tickets.create_from_conversation_title" : "tickets.create_title")}
      size="lg"
    >
      <form onSubmit={onSubmit}>
        <Stack gap="sm">
          {sourceConversation && (
            <Paper withBorder radius="md" px="sm" py="xs" bg="var(--tn-canvas)">
              <Text size="xs" c="dimmed">
                {t("tickets.source_conversation_hint", {
                  contact: contactDisplayName(sourceConversation.contact, t),
                  channel: sourceConversation.channel?.name ?? "—",
                })}
              </Text>
            </Paper>
          )}

          <TextInput
            label={t("tickets.title")}
            placeholder={t("tickets.title_placeholder")}
            value={title}
            onChange={(e) => {
              setTitle(e.currentTarget.value)
              if (titleError) setTitleError(null)
            }}
            error={titleError ?? undefined}
            maxLength={120}
            required
            data-autofocus
          />

          <Textarea
            label={t("tickets.description")}
            placeholder={t("tickets.description_placeholder")}
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
            minRows={3}
            maxRows={6}
            autosize
            maxLength={5000}
          />

          <Group justify="space-between">
            <Button
              type="button"
              variant="subtle"
              color="gray"
              size="compact-xs"
              leftSection={moreOpen ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
              onClick={() => setMoreOpen((v) => !v)}
            >
              {t("tickets.more_options")}
            </Button>
          </Group>

          {moreOpen && (
            <Stack gap="sm" pt={4} style={{ borderTop: "1px solid var(--tn-border)" }}>
              {types.length > 0 && (
                <Select
                  label={t("tickets.type")}
                  placeholder={t("tickets.type_generic")}
                  value={typeId}
                  onChange={handleTypeChange}
                  data={types.map((x) => ({ value: x.id, label: x.name }))}
                  clearable
                />
              )}

              <Stack gap={4}>
                <Text size="sm" fw={500}>
                  {t("tickets.visibility")}
                </Text>
                <SegmentedControl
                  value={visibility}
                  onChange={(v) => setVisibility(v as TicketTypeVisibility)}
                  data={[
                    { value: "all", label: t("tickets.visibility_all") },
                    { value: "members", label: t("tickets.visibility_members") },
                  ]}
                />
                <Text size="xs" c="dimmed">
                  {t(visibility === "all" ? "tickets.visibility_all_hint" : "tickets.visibility_members_hint")}
                </Text>
              </Stack>

              <Group grow align="flex-start">
                <Select
                  label={t("inbox.team")}
                  placeholder={t("inbox.no_team")}
                  value={teamId}
                  onChange={setTeamId}
                  data={teams.map((x) => ({ value: x.id, label: x.name }))}
                  clearable
                  searchable
                />
                <Select
                  label={t("inbox.assignee")}
                  placeholder={t("inbox.no_assignee")}
                  value={assigneeId}
                  onChange={setAssigneeId}
                  data={assignees.map((m) => ({ value: m.id, label: m.id === me.data?.id ? t("inbox.you") : m.name }))}
                  clearable
                  searchable
                />
                <Select
                  label={t("inbox.priority")}
                  value={priority}
                  onChange={(v) => v && setPriority(v as "normal" | "high")}
                  allowDeselect={false}
                  data={[
                    { value: "normal", label: t("inbox.priority_normal") },
                    { value: "high", label: t("inbox.priority_high") },
                  ]}
                />
              </Group>

              {typeId && typeAttrs.length > 0 && (
                <Stack gap="xs" pt="xs" style={{ borderTop: "1px solid var(--tn-border)" }}>
                  <Text size="xs" fw={600} c="dimmed">
                    {t("tickets.details_fields")}
                  </Text>
                  {typeAttrs.map((def) => (
                    <Stack key={def.id} gap={4}>
                      <Text size="sm" fw={500}>
                        {def.name}
                      </Text>
                      <AttributeInput
                        def={def}
                        value={attributes[def.key]}
                        disabled={create.isPending}
                        onSave={(val) => setAttributes((prev) => ({ ...prev, [def.key]: val }))}
                      />
                    </Stack>
                  ))}
                </Stack>
              )}
            </Stack>
          )}

          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={onClose} disabled={create.isPending}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" leftSection={<IconTicket size={16} />} loading={create.isPending}>
              {t("tickets.submit")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
