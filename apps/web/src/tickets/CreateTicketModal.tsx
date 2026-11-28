import type { ConversationDTO, ConversationTicketDTO, TicketTypeVisibility } from "@agents-desk/client/tenant"
import {
  Badge,
  Button,
  Center,
  Group,
  Loader,
  Modal,
  Paper,
  Pill,
  ScrollArea,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Text,
  Textarea,
  TextInput,
  UnstyledButton,
} from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { IconChevronDown, IconChevronRight, IconPlus, IconSearch, IconTicket } from "@tabler/icons-react"
import { notifyError, notifySuccess, useCan, useT } from "@go-tenon/ui"
import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from "react"
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
  const [extraConversations, setExtraConversations] = useState<ConversationDTO[]>([])
  const [pickerOpen, setPickerOpen] = useState(false)
  const [convQuery, setConvQuery] = useState("")
  const [debouncedConvQuery] = useDebouncedValue(convQuery.trim(), 200)

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
    setExtraConversations([])
    setPickerOpen(false)
    setConvQuery("")
  }, [opened, sourceConversation?.priority])

  // 展开“添加其他会话”面板时加载当前可见的活跃服务型会话，输入关键词时同时走全文搜索
  const recentConvs = useQuery({
    queryKey: [...kit.queries.conversation.key(), "ticket-create-picker-recent"],
    queryFn: () => tenantApi.conversation.list({ inbox: "all", status: "open", sort: "recent", per_page: 25 }),
    enabled: opened && pickerOpen,
  })
  const searchedConvs = useQuery({
    queryKey: [...kit.queries.conversation.key(), "ticket-create-picker-search", debouncedConvQuery],
    queryFn: () => tenantApi.conversation.search({ q: debouncedConvQuery, per_page: 20 }),
    enabled: opened && pickerOpen && debouncedConvQuery.length > 0,
  })

  // 合并候选会话并排除主来源会话、已选会话以及同一联系人的其他会话
  const candidateConversations = useMemo(() => {
    const excludedConvIds = new Set<string>()
    const excludedContactIds = new Set<string>()
    if (sourceConversation) {
      excludedConvIds.add(sourceConversation.id)
      if (sourceConversation.contact?.id) excludedContactIds.add(sourceConversation.contact.id)
    }
    for (const conv of extraConversations) {
      excludedConvIds.add(conv.id)
      if (conv.contact?.id) excludedContactIds.add(conv.contact.id)
    }

    const recent = recentConvs.data?.items ?? []
    const needle = debouncedConvQuery.toLowerCase()
    const merged: ConversationDTO[] = []
    const seen = new Set<string>()
    const pushIfAllowed = (conv: ConversationDTO) => {
      if (seen.has(conv.id) || excludedConvIds.has(conv.id)) return
      if (conv.contact?.id && excludedContactIds.has(conv.contact.id)) return
      seen.add(conv.id)
      merged.push(conv)
    }

    if (!needle) {
      for (const conv of recent) pushIfAllowed(conv)
      return merged
    }

    // 先匹配本地活跃会话中的客户名称、邮箱、渠道名或摘要，再补充全文搜索命中的会话
    for (const conv of recent) {
      const name = contactDisplayName(conv.contact, t).toLowerCase()
      const email = conv.contact?.email?.toLowerCase() ?? ""
      const channel = conv.channel?.name?.toLowerCase() ?? ""
      const preview = conv.preview?.toLowerCase() ?? ""
      if (name.includes(needle) || email.includes(needle) || channel.includes(needle) || preview.includes(needle)) {
        pushIfAllowed(conv)
      }
    }
    for (const hit of searchedConvs.data?.items ?? []) {
      pushIfAllowed(hit.conversation)
    }
    return merged
  }, [sourceConversation, extraConversations, recentConvs.data?.items, searchedConvs.data?.items, debouncedConvQuery, t])

  const pickerLoading = recentConvs.isPending || (debouncedConvQuery.length > 0 && searchedConvs.isPending)

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
        source_conversation_ids: extraConversations.length > 0 ? extraConversations.map((x) => x.id) : undefined,
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
      closeOnClickOutside={false}
    >
      <form noValidate onSubmit={onSubmit}>
        <Stack gap="sm">
          {(sourceConversation || extraConversations.length > 0 || pickerOpen) && (
            <Paper withBorder radius="md" px="sm" py="xs" bg="var(--tn-canvas)">
              <Stack gap={8}>
                {sourceConversation ? (
                  <Group justify="space-between" gap="xs" wrap="nowrap">
                    <Text size="xs" c="dimmed" truncate>
                      {t("tickets.source_conversation_hint", {
                        contact: contactDisplayName(sourceConversation.contact, t),
                        channel: sourceConversation.channel?.name ?? "—",
                      })}
                    </Text>
                    <Button
                      type="button"
                      variant="subtle"
                      size="compact-xs"
                      leftSection={<IconPlus size={12} />}
                      onClick={() => setPickerOpen((v) => !v)}
                      style={{ flex: "none" }}
                    >
                      {t("tickets.add_more_conversations")}
                    </Button>
                  </Group>
                ) : (
                  <Group justify="space-between" gap="xs" wrap="nowrap">
                    <Text size="xs" c="dimmed">
                      {t("tickets.extra_conversations_count", { n: extraConversations.length })}
                    </Text>
                    <Button
                      type="button"
                      variant="subtle"
                      size="compact-xs"
                      leftSection={<IconPlus size={12} />}
                      onClick={() => setPickerOpen((v) => !v)}
                      style={{ flex: "none" }}
                    >
                      {t("tickets.add_conversations")}
                    </Button>
                  </Group>
                )}

                {extraConversations.length > 0 && (
                  <Group gap={6} wrap="wrap">
                    {extraConversations.map((conv) => (
                      <Pill
                        key={conv.id}
                        size="sm"
                        withRemoveButton
                        onRemove={() => setExtraConversations((prev) => prev.filter((x) => x.id !== conv.id))}
                      >
                        {contactDisplayName(conv.contact, t)}
                        {conv.channel?.name ? ` · ${conv.channel.name}` : ""}
                      </Pill>
                    ))}
                  </Group>
                )}

                {pickerOpen && (
                  <Stack gap={6} pt={4} style={{ borderTop: "1px solid var(--tn-border)" }}>
                    <TextInput
                      size="xs"
                      value={convQuery}
                      onChange={(e) => setConvQuery(e.currentTarget.value)}
                      placeholder={t("tickets.pick_conversations_placeholder")}
                      leftSection={<IconSearch size={14} />}
                    />
                    <ScrollArea.Autosize mah={180}>
                      {pickerLoading ? (
                        <Center py="sm">
                          <Loader size="xs" />
                        </Center>
                      ) : candidateConversations.length === 0 ? (
                        <Text size="xs" c="dimmed" ta="center" py="sm">
                          {t("tickets.pick_conversations_empty")}
                        </Text>
                      ) : (
                        <Stack gap={4}>
                          {candidateConversations.map((conv) => (
                            <UnstyledButton
                              key={conv.id}
                              type="button"
                              onClick={() => setExtraConversations((prev) => [...prev, conv])}
                              className="inbox-history"
                              px="xs"
                              py={6}
                              style={{ borderRadius: "var(--mantine-radius-sm)", border: "1px solid var(--tn-border)" }}
                            >
                              <Group gap={6} wrap="nowrap">
                                <Text size="xs" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                                  {contactDisplayName(conv.contact, t)}
                                </Text>
                                {conv.channel?.name && (
                                  <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                                    {conv.channel.name}
                                  </Badge>
                                )}
                                <IconPlus size={13} color="var(--tn-muted)" style={{ flex: "none" }} />
                              </Group>
                              {conv.preview && (
                                <Text size="xs" c="dimmed" truncate mt={2}>
                                  {conv.preview}
                                </Text>
                              )}
                            </UnstyledButton>
                          ))}
                        </Stack>
                      )}
                    </ScrollArea.Autosize>
                  </Stack>
                )}
              </Stack>
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
            {!sourceConversation && !pickerOpen && extraConversations.length === 0 && (
              <Button
                type="button"
                variant="subtle"
                color="gray"
                size="compact-xs"
                leftSection={<IconPlus size={12} />}
                onClick={() => setPickerOpen(true)}
              >
                {t("tickets.add_conversations")}
              </Button>
            )}
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

              <SimpleGrid cols={{ base: 1, sm: 3 }} style={{ alignItems: "flex-start" }}>
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
              </SimpleGrid>

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

          <Group justify="flex-end" gap="xs" mt="xs">
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
