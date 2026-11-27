import type { ConversationTicketDTO, TicketStatus, TicketTypeVisibility } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, Box, Button, Group, SegmentedControl, Select, Stack, Text, Textarea, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import { IconLinkOff, IconPlus, IconX } from "@tabler/icons-react"
import {
  confirm,
  Inspector,
  InspectorField,
  InspectorSection,
  notifyError,
  notifySuccess,
  PaneBody,
  PaneHeader,
  useCan,
  useLanguage,
  useT,
  useTimeZone,
} from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { contactDisplayName } from "../contacts/display"
import { ChannelIcon } from "../inbox/channel-badge"
import { useMe, useMembers, useTags, useTeams } from "../inbox/data"
import { AttributeInput, TagsEditor } from "../inbox/fields"
import { activity } from "../inbox/format"
import { LinkContactModal } from "./LinkContactModal"
import { invalidateTicket, TICKET_STATUSES, ticketStatusLabel, useTicketAttributes, useTicketTypes } from "./meta"

/**
 * TicketDetails 是工单详情页的右侧栏：
 * 展示并就地编辑工单状态、可选工单类型、可见范围、描述、团队收件箱、负责人、优先级、标签、类型专属字段，
 * 以及当前成员有权限看到的受影响客户列表（支持新增关联和取消关联）。
 */
export function TicketDetails({ ticket, onClose }: { ticket: ConversationTicketDTO; onClose: () => void }): ReactNode {
  const t = useT()
  const can = useCan()
  const ui = useLanguage()
  const zone = useTimeZone()
  const { slug } = useParams({ from: "/t/$slug" })
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams()
  const allTags = useTags()
  const ticketTypes = useTicketTypes(true)
  const ticketAttrs = useTicketAttributes(ticket.type_id)

  const [editingDesc, setEditingDesc] = useState(false)
  const [descDraft, setDescDraft] = useState(ticket.description)
  const [linkOpen, setLinkOpen] = useState(false)

  useEffect(() => {
    setDescDraft(ticket.description)
  }, [ticket.description, ticket.id])

  const canEdit = can("conversations:reply")
  const invalidate = () => invalidateTicket(queryClient, ticket.id)

  const setStatus = useMutation({
    mutationFn: (status: TicketStatus) => tenantApi.ticket.status(ticket.id, { status }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const updateMeta = useMutation({
    mutationFn: (body: { type_id?: string; description?: string; visibility?: TicketTypeVisibility }) => tenantApi.ticket.update(ticket.id, body),
    onSuccess: () => {
      setEditingDesc(false)
      invalidate()
    },
    onError: (err) => notifyError(err),
  })
  const assign = useMutation({
    mutationFn: (body: { assignee_id?: string; team_id?: string }) => tenantApi.conversation.assign(ticket.id, body),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const priority = useMutation({
    mutationFn: (p: "normal" | "high") => tenantApi.conversation.priority(ticket.id, { priority: p }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const addTag = useMutation({
    mutationFn: (tag_id: string) => tenantApi.conversation.tagAdd(ticket.id, { tag_id }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const removeTag = useMutation({
    mutationFn: (tagId: string) => tenantApi.conversation.tagRemove(ticket.id, tagId),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const patchAttr = useMutation({
    mutationFn: (attributes: Record<string, unknown>) => tenantApi.conversation.attributesPatch(ticket.id, { attributes }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const unlink = useMutation({
    mutationFn: (contactId: string) => tenantApi.ticket.contactRemove(ticket.id, contactId),
    onSuccess: () => {
      notifySuccess(t("tickets.unlink_done"))
      invalidateTicket(queryClient, ticket.id, { relate: true })
    },
    onError: (err) => notifyError(err),
  })

  const handleUnlink = async (contactId: string) => {
    const ok = await confirm({
      title: t("tickets.unlink_confirm"),
      message: t("tickets.unlink_message"),
      danger: true,
      confirmLabel: t("tickets.unlink"),
    })
    if (ok) unlink.mutate(contactId)
  }

  const assignees = can("conversations:assign") ? members : members.filter((m) => m.id === me.data?.id)
  const linked = ticket.linked_contacts ?? []
  const linkedIds = new Set(linked.map((x) => x.contact.id))
  const hasHiddenLinks = ticket.linked_count > linked.length
  const typeOptions = ticketTypes.map((x) => ({ value: x.id, label: x.name }))
  if (ticket.type_id && ticket.type_name && !typeOptions.some((x) => x.value === ticket.type_id)) {
    typeOptions.unshift({ value: ticket.type_id, label: ticket.type_name })
  }

  return (
    <Stack gap={0} h="100%" className="inbox-details">
      <PaneHeader
        title={t("inbox.details")}
        actions={
          <ActionIcon variant="subtle" color="gray" size="sm" onClick={onClose} aria-label={t("inbox.close_details")}>
            <IconX size={16} />
          </ActionIcon>
        }
      />
      <PaneBody>
        <Inspector>
          <InspectorSection title={t("tickets.details_basic")} collapsible storeKey="ticket-details-basic">
            <InspectorField label={t("tickets.status")}>
              <Select
                allowDeselect={false}
                value={ticket.status}
                data={TICKET_STATUSES.map((st) => ({ value: st, label: ticketStatusLabel(st, t) }))}
                disabled={!canEdit || setStatus.isPending}
                onChange={(v) => v && setStatus.mutate(v as TicketStatus)}
              />
            </InspectorField>
            {(typeOptions.length > 0 || ticket.type_id) && (
              <InspectorField label={t("tickets.type")}>
                <Select
                  placeholder={t("tickets.type_generic")}
                  value={ticket.type_id ?? null}
                  data={typeOptions}
                  clearable
                  disabled={!canEdit || updateMeta.isPending}
                  onChange={(v) => updateMeta.mutate({ type_id: v ?? "" })}
                />
              </InspectorField>
            )}
            <InspectorField label={t("tickets.visibility")} align="start">
              <SegmentedControl
                size="xs"
                fullWidth
                value={ticket.visibility}
                disabled={!canEdit || updateMeta.isPending}
                onChange={(v) => updateMeta.mutate({ visibility: v as TicketTypeVisibility })}
                data={[
                  { value: "all", label: t("tickets.visibility_all") },
                  { value: "members", label: t("tickets.visibility_members") },
                ]}
              />
            </InspectorField>
            <InspectorField label={t("inbox.assignee")}>
              <Select
                placeholder={t("inbox.no_assignee")}
                value={ticket.assignee?.type === "member" ? ticket.assignee.id : null}
                data={assignees.map((m) => ({ value: m.id, label: m.id === me.data?.id ? t("inbox.you") : m.name }))}
                onChange={(v) => assign.mutate({ assignee_id: v ?? "" })}
                clearable={can("conversations:assign")}
                disabled={!canEdit || assign.isPending}
                searchable
              />
            </InspectorField>
            <InspectorField label={t("inbox.team")}>
              <Select
                placeholder={t("inbox.no_team")}
                value={ticket.team_id ?? null}
                data={teams
                  .filter((x) => x.status === "active" || x.id === ticket.team_id)
                  .map((x) => ({
                    value: x.id,
                    label: x.status === "disabled" ? t("inbox.team_disabled_suffix", { name: x.name }) : x.name,
                    disabled: x.status === "disabled",
                  }))}
                onChange={(v) => assign.mutate({ team_id: v ?? "" })}
                clearable
                disabled={!can("conversations:assign") || assign.isPending}
              />
            </InspectorField>
            <InspectorField label={t("inbox.priority")}>
              <Select
                allowDeselect={false}
                value={ticket.priority}
                data={[
                  { value: "normal", label: t("inbox.priority_normal") },
                  { value: "high", label: t("inbox.priority_high") },
                ]}
                disabled={!canEdit || priority.isPending}
                onChange={(v) => v && priority.mutate(v as "normal" | "high")}
              />
            </InspectorField>
            <InspectorField label={t("inbox.tags")} align="start">
              <TagsEditor
                tags={ticket.tags}
                allTags={allTags}
                editable={canEdit}
                disabled={addTag.isPending || removeTag.isPending}
                onAdd={(tagId) => addTag.mutate(tagId)}
                onRemove={(tagId) => removeTag.mutate(tagId)}
              />
            </InspectorField>
            <InspectorField label={t("tickets.description")} align="start">
              {editingDesc ? (
                <Stack gap={6} w="100%">
                  <Textarea
                    value={descDraft}
                    onChange={(e) => setDescDraft(e.currentTarget.value)}
                    placeholder={t("tickets.description_placeholder")}
                    autosize
                    minRows={3}
                    maxRows={8}
                    maxLength={5000}
                    autoFocus
                  />
                  <Group gap={6} justify="flex-end">
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      onClick={() => {
                        setDescDraft(ticket.description)
                        setEditingDesc(false)
                      }}
                    >
                      {t("common.cancel")}
                    </Button>
                    <Button
                      size="compact-xs"
                      loading={updateMeta.isPending}
                      onClick={() => updateMeta.mutate({ description: descDraft.trim() })}
                    >
                      {t("common.save")}
                    </Button>
                  </Group>
                </Stack>
              ) : (
                <UnstyledButton
                  w="100%"
                  disabled={!canEdit}
                  onClick={() => canEdit && setEditingDesc(true)}
                  style={{ textAlign: "left" }}
                  aria-label={t("tickets.edit_description")}
                >
                  <Text
                    size="sm"
                    c={ticket.description ? undefined : "dimmed"}
                    style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                  >
                    {ticket.description || t("tickets.no_description")}
                  </Text>
                </UnstyledButton>
              )}
            </InspectorField>
          </InspectorSection>

          {ticket.type_id && ticketAttrs.length > 0 && (
            <InspectorSection title={t("tickets.details_fields")} collapsible storeKey="ticket-details-fields">
              {ticketAttrs.map((def) => (
                <InspectorField key={def.id} label={def.name}>
                  <AttributeInput
                    key={`${ticket.id}:${def.key}`}
                    def={def}
                    value={ticket.attributes[def.key]}
                    disabled={!canEdit || patchAttr.isPending}
                    onSave={(val) => patchAttr.mutate({ [def.key]: val })}
                  />
                </InspectorField>
              ))}
            </InspectorSection>
          )}

          <InspectorSection
            title={t("tickets.linked_section")}
            collapsible
            storeKey="ticket-details-links"
            extra={
              <Group gap={4} wrap="nowrap">
                <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                  {hasHiddenLinks
                    ? t("tickets.linked_visibility_summary", { visible: linked.length, total: ticket.linked_count })
                    : ticket.linked_count}
                </Text>
                {canEdit && (
                  <Tooltip label={t("tickets.link_contact")} openDelay={250}>
                    <ActionIcon
                      size={22}
                      variant="subtle"
                      color="gray"
                      aria-label={t("tickets.link_contact")}
                      onClick={(e) => {
                        e.stopPropagation()
                        setLinkOpen(true)
                      }}
                    >
                      <IconPlus size={14} />
                    </ActionIcon>
                  </Tooltip>
                )}
              </Group>
            }
          >
            {linked.length === 0 ? (
              <Text size="xs" c="dimmed">
                {t("tickets.linked_empty")}
              </Text>
            ) : (
              <Stack gap={6}>
                {linked.map((lc) => {
                  const content = (
                    <Box px={6} py={5}>
                      <Group gap={5} wrap="nowrap">
                        <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                          {contactDisplayName(lc.contact, t)}
                        </Text>
                        {lc.is_source && (
                          <Badge size="xs" variant="light" color="brand" style={{ flex: "none" }}>
                            {t("tickets.relation_source")}
                          </Badge>
                        )}
                      </Group>
                      <Text size="xs" c="dimmed" truncate mt={2}>
                        {lc.preview || lc.contact.email || "—"}
                      </Text>
                      {(lc.channel || lc.conversation_status || lc.last_message_at) && (
                        <Group gap={5} wrap="nowrap" mt={2}>
                          {lc.channel && <ChannelIcon kind={lc.channel.kind} size={11} />}
                          <Text size="xs" c="dimmed" truncate>
                            {[
                              lc.channel?.name,
                              lc.conversation_status ? t(`inbox.status_${lc.conversation_status}` as const) : undefined,
                              lc.last_message_at ? activity(lc.last_message_at, ui, t, zone) : undefined,
                            ]
                              .filter(Boolean)
                              .join(" · ")}
                          </Text>
                        </Group>
                      )}
                    </Box>
                  )
                  return (
                    <Group key={lc.contact.id} gap={4} wrap="nowrap" align="flex-start">
                      {lc.conversation_id ? (
                        <Link
                          to="/t/$slug/inbox/$id"
                          params={{ slug, id: lc.conversation_id }}
                          search={{ box: "all", status: lc.conversation_status || "open", sort: "waiting" }}
                          className="inbox-history"
                          style={{ flex: 1, minWidth: 0, textDecoration: "none", color: "inherit" }}
                        >
                          {content}
                        </Link>
                      ) : (
                        <Link
                          to="/t/$slug/contacts/$id"
                          params={{ slug, id: lc.contact.id }}
                          search={{ sort: "recent" }}
                          className="inbox-history"
                          style={{ flex: 1, minWidth: 0, textDecoration: "none", color: "inherit" }}
                        >
                          {content}
                        </Link>
                      )}
                      {canEdit && (
                        <Tooltip label={t("tickets.unlink")} openDelay={250}>
                          <ActionIcon
                            size={24}
                            variant="subtle"
                            color="gray"
                            aria-label={t("tickets.unlink")}
                            disabled={unlink.isPending}
                            onClick={() => void handleUnlink(lc.contact.id)}
                            mt={4}
                          >
                            <IconLinkOff size={14} />
                          </ActionIcon>
                        </Tooltip>
                      )}
                    </Group>
                  )
                })}
              </Stack>
            )}
          </InspectorSection>
        </Inspector>
      </PaneBody>

      <LinkContactModal opened={linkOpen} ticketId={ticket.id} linkedContactIds={linkedIds} onClose={() => setLinkOpen(false)} />
    </Stack>
  )
}
