import type { ConversationDTO } from "@agents-desk/client/tenant"
import { ActionIcon, Anchor, Badge, Box, Button, CopyButton, Group, Modal, Rating, Select, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import { IconCheck, IconCopy, IconExternalLink, IconGitMerge, IconX } from "@tabler/icons-react"
import {
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
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../api"
import { ContactNotesCard } from "../contacts/ContactNotesCard"
import { contactDisplayName } from "../contacts/display"
import { MergeContactModal } from "../contacts/MergeContactModal"
import { LanguageSelect } from "../LanguageSelect"
import { languageName } from "../languages"
import { formatLocalTime } from "../timezone"
import { previewSummary } from "./Attachments"
import { ChannelIcon } from "./channel-badge"
import { useAttributes, useContactHistory, useMe, useMembers, useTags, useTeams } from "./data"
import { AttributeInput, TagsEditor } from "./fields"
import { activity } from "./format"

/** AUTO 是对方语言中的“自动识别”：按收到的消息识别。 */
const AUTO = "auto"

/**
 * Details 是会话的详情栏（第五栏）：使用框架的 PaneHeader/PaneBody 与 Inspector 分区展示并就地编辑
 * 会话属性、联系人资料、内部备注便签和历史会话。阅读态与编辑控件统一保持 13px 字号。
 */
export function Details({ c, onClose, onOpen }: { c: ConversationDTO; onClose: () => void; onOpen: (id: string) => void }): ReactNode {
  const t = useT()
  const can = useCan()
  const language = useLanguage()
  const zone = useTimeZone()
  const { slug } = useParams({ from: "/t/$slug" })
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams()
  const allTags = useTags()
  const convAttrs = useAttributes("conversation")
  const contactAttrs = useAttributes("contact")
  const history = useContactHistory(c.contact?.id)
  const contactDetail = useQuery({
    ...kit.queries.contact.get(c.contact?.id ?? ""),
    enabled: !!c.contact?.id,
  })
  const serverNotes = contactDetail.data?.notes ?? ""
  const canEdit = can("conversations:reply")
  const invalidate = () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
  const assign = useMutation({
    mutationFn: (body: { assignee_id?: string; team_id?: string }) => tenantApi.conversation.assign(c.id, body),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const priority = useMutation({
    mutationFn: (p: "normal" | "high") => tenantApi.conversation.priority(c.id, { priority: p }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const setLanguage = useMutation({
    mutationFn: (lang: string) => tenantApi.conversation.language(c.id, { language: lang }),
    onSuccess: () => {
      void invalidate()
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
    },
    onError: (err) => notifyError(err),
  })
  const saveNotes = useMutation({
    mutationFn: (next: string) => tenantApi.contact.update(c.contact!.id, { notes: next }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      notifySuccess(t("common.saved"))
    },
    onError: (err) => notifyError(err),
  })
  const addConvTag = useMutation({
    mutationFn: (tag_id: string) => tenantApi.conversation.tagAdd(c.id, { tag_id }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const removeConvTag = useMutation({
    mutationFn: (tagId: string) => tenantApi.conversation.tagRemove(c.id, tagId),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const patchConvAttr = useMutation({
    mutationFn: (attributes: Record<string, unknown>) => tenantApi.conversation.attributesPatch(c.id, { attributes }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const addContactTag = useMutation({
    mutationFn: (tag_id: string) => tenantApi.conversation.contactTagAdd(c.id, { tag_id }),
    onSuccess: () => {
      void invalidate()
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
    },
    onError: (err) => notifyError(err),
  })
  const removeContactTag = useMutation({
    mutationFn: (tagId: string) => tenantApi.conversation.contactTagRemove(c.id, tagId),
    onSuccess: () => {
      void invalidate()
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
    },
    onError: (err) => notifyError(err),
  })
  const patchContactAttr = useMutation({
    mutationFn: (attributes: Record<string, unknown>) => tenantApi.conversation.contactAttributesPatch(c.id, { attributes }),
    onSuccess: () => {
      void invalidate()
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
    },
    onError: (err) => notifyError(err),
  })
  const [mergeContactOpen, setMergeContactOpen] = useState(false)
  const [mergeSource, setMergeSource] = useState<ConversationDTO | null>(null)
  const mergeConversation = useMutation({
    mutationFn: (sourceId: string) => tenantApi.conversation.merge(c.id, { source_id: sourceId }),
    onSuccess: () => {
      notifySuccess(t("inbox.merge_conversation_done"))
      setMergeSource(null)
      void invalidate()
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
    },
    onError: (err) => notifyError(err),
  })
  // 对方的语言：手动设置的显示那种语言，否则显示“自动识别”（带上识别出的语言）
  const manual = c.contact?.language_manual ?? false
  const detected = c.language ? languageName(c.language, language) : ""
  // 没有“改派给别人”的权限时，负责人只能选自己
  const assignees = can("conversations:assign") ? members : members.filter((m) => m.id === me.data?.id)
  const others = (history.data?.items ?? []).filter((x) => x.id !== c.id)
  const location = contactDetail.data?.location || c.contact?.location || ""
  const timeZone = contactDetail.data?.time_zone || c.contact?.time_zone || ""
  const localTime = formatLocalTime(timeZone)
  const locationText = [location, localTime ? t("contacts.local_time", { time: localTime }) : ""].filter(Boolean).join(" · ")
  const referrer = contactDetail.data?.referrer ?? ""

  return (
    <Stack gap={0} h="100%" className="inbox-details">
      <PaneHeader
        title={t("inbox.details")}
        actions={
          <ActionIcon variant="subtle" color="gray" onClick={onClose} aria-label={t("inbox.close_details")}>
            <IconX size={16} />
          </ActionIcon>
        }
      />
      <PaneBody>
        <Inspector>
          <InspectorSection title={t("inbox.details_conversation")} collapsible storeKey="inbox-details-conv">
            <InspectorField label={t("inbox.assignee")}>
              <Select
                placeholder={t("inbox.no_assignee")}
                value={c.assignee?.type === "member" ? c.assignee.id : null}
                data={assignees.map((m) => ({ value: m.id, label: m.id === me.data?.id ? t("inbox.you") : m.name }))}
                onChange={(v) => assign.mutate({ assignee_id: v ?? "" })}
                clearable={can("conversations:assign")}
                disabled={assign.isPending}
                searchable
              />
            </InspectorField>
            <InspectorField label={t("inbox.team")}>
              <Select
                placeholder={t("inbox.no_team")}
                value={c.team_id ?? null}
                data={teams
                  .filter((x) => x.status === "active" || x.id === c.team_id)
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
                value={c.priority}
                data={[
                  { value: "normal", label: t("inbox.priority_normal") },
                  { value: "high", label: t("inbox.priority_high") },
                ]}
                disabled={priority.isPending}
                onChange={(v) => v && priority.mutate(v as "normal" | "high")}
              />
            </InspectorField>
            <InspectorField label={t("inbox.status")}>
              <Text size="sm">{t(`inbox.status_${c.status}` as const)}</Text>
            </InspectorField>
            {c.reply_channel && c.channel && c.reply_channel.id !== c.channel.id ? (
              <>
                <InspectorField label={t("inbox.channel_origin")}>
                  <Group gap={5} wrap="nowrap">
                    <ChannelIcon kind={c.channel.kind} size={13} />
                    <Text size="sm" truncate>
                      {c.channel.name}
                    </Text>
                  </Group>
                </InspectorField>
                <InspectorField label={t("inbox.channel_reply")}>
                  <Group gap={5} wrap="nowrap">
                    <ChannelIcon kind={c.reply_channel.kind} size={13} />
                    <Text size="sm" truncate>
                      {c.reply_channel.name}
                    </Text>
                  </Group>
                </InspectorField>
              </>
            ) : (
              <InspectorField label={t("inbox.channel")}>
                {c.channel ? (
                  <Group gap={5} wrap="nowrap">
                    <ChannelIcon kind={c.channel.kind} size={13} />
                    <Text size="sm" truncate>
                      {c.channel.name}
                    </Text>
                  </Group>
                ) : (
                  <Text size="sm">—</Text>
                )}
              </InspectorField>
            )}
            <InspectorField label={t("inbox.tags")} align="start">
              <TagsEditor
                tags={c.tags}
                allTags={allTags}
                editable={canEdit}
                disabled={addConvTag.isPending || removeConvTag.isPending}
                onAdd={(tagId) => addConvTag.mutate(tagId)}
                onRemove={(tagId) => removeConvTag.mutate(tagId)}
              />
            </InspectorField>
            {convAttrs.map((def) => (
              <InspectorField key={def.id} label={def.name}>
                <AttributeInput
                  key={`${c.id}:${def.key}`}
                  def={def}
                  value={c.attributes[def.key]}
                  disabled={!canEdit || patchConvAttr.isPending}
                  onSave={(val) => patchConvAttr.mutate({ [def.key]: val })}
                />
              </InspectorField>
            ))}
            {c.rating && (
              <InspectorField label={t("inbox.rating")} align="start">
                <Stack gap={2}>
                  <Group gap={6} wrap="nowrap">
                    <Rating value={c.rating.score} readOnly size="xs" color="yellow" />
                    <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {c.rating.score}/5
                    </Text>
                  </Group>
                  {c.rating.comment && (
                    <Text size="xs" c="dimmed">
                      {c.rating.comment}
                    </Text>
                  )}
                </Stack>
              </InspectorField>
            )}
          </InspectorSection>

          <InspectorSection
            title={t("inbox.details_contact")}
            collapsible
            storeKey="inbox-details-contact"
            extra={
              c.contact && can("contacts:update") ? (
                <Tooltip label={t("contacts.merge")} openDelay={250}>
                  <ActionIcon
                    size={22}
                    variant="subtle"
                    color="gray"
                    aria-label={t("contacts.merge")}
                    onClick={(e) => {
                      e.stopPropagation()
                      setMergeContactOpen(true)
                    }}
                  >
                    <IconGitMerge size={13} />
                  </ActionIcon>
                </Tooltip>
              ) : undefined
            }
          >
            <InspectorField label={t("inbox.contact_name")}>
              {c.contact ? (
                <Group gap={6} wrap="nowrap">
                  <Link
                    to="/t/$slug/contacts/$id"
                    params={{ slug, id: c.contact.id }}
                    search={{ sort: "recent" }}
                    className="inbox-contact-link"
                  >
                    <Text size="sm" fw={500} c="brand" truncate>
                      {contactDisplayName(c.contact, t)}
                    </Text>
                  </Link>
                  <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                    {kit.label("ContactKind", c.contact.kind)}
                  </Badge>
                </Group>
              ) : (
                <Text size="sm">{t("inbox.visitor")}</Text>
              )}
            </InspectorField>
            <InspectorField label={t("inbox.contact_email")}>
              <Group gap={4} wrap="nowrap" justify="space-between">
                <Text size="sm" c={c.contact?.email ? undefined : "dimmed"} truncate style={{ flex: 1, minWidth: 0 }}>
                  {c.contact?.email || "—"}
                </Text>
                {c.contact?.email && (
                  <CopyButton value={c.contact.email} timeout={1500}>
                    {({ copied, copy }) => (
                      <Tooltip label={t(copied ? "contacts.copied" : "contacts.copy")} withArrow>
                        <ActionIcon variant="subtle" color={copied ? "green" : "gray"} size={22} onClick={copy} aria-label={t("contacts.copy")}>
                          {copied ? <IconCheck size={13} /> : <IconCopy size={13} />}
                        </ActionIcon>
                      </Tooltip>
                    )}
                  </CopyButton>
                )}
              </Group>
            </InspectorField>
            <InspectorField label={t("inbox.contact_language")} align={manual ? "start" : "center"}>
              <LanguageSelect
                before={[{ value: AUTO, label: !manual && detected ? t("inbox.language_auto_detected", { lang: detected }) : t("inbox.language_auto") }]}
                first={c.language ? [c.language] : []}
                value={manual ? c.language : AUTO}
                onChange={(v) => v && setLanguage.mutate(v === AUTO ? "" : v)}
                allowDeselect={false}
                disabled={!can("conversations:reply") || setLanguage.isPending}
                aria-label={t("inbox.contact_language")}
              />
              {manual && (
                <Text size="xs" c="dimmed" mt={2}>
                  {t("inbox.language_manual_hint")}
                </Text>
              )}
            </InspectorField>
            {locationText && (
              <InspectorField label={t("inbox.contact_location")}>
                <Text size="sm" truncate>
                  {locationText}
                </Text>
              </InspectorField>
            )}
            {referrer && (
              <InspectorField label={t("inbox.contact_referrer")}>
                <Anchor href={referrer} target="_blank" rel="noopener noreferrer" size="sm" style={{ display: "inline-flex", alignItems: "center", gap: 4, maxWidth: "100%" }}>
                  <Text span size="sm" truncate>
                    {referrer}
                  </Text>
                  <IconExternalLink size={12} style={{ flex: "none" }} />
                </Anchor>
              </InspectorField>
            )}
            {c.available_channels && c.available_channels.length > 0 && (
              <InspectorField label={t("contacts.channel_identities")} align="start">
                <Group gap={4} wrap="wrap">
                  {c.available_channels.map((ch) => (
                    <Badge
                      key={ch.id}
                      size="xs"
                      variant="light"
                      color="gray"
                      leftSection={<ChannelIcon kind={ch.kind} size={10} />}
                      style={{ textTransform: "none" }}
                    >
                      {ch.name}
                    </Badge>
                  ))}
                </Group>
              </InspectorField>
            )}
            {c.contact && (
              <>
                <InspectorField label={t("inbox.tags")} align="start">
                  <TagsEditor
                    tags={c.contact.tags}
                    allTags={allTags}
                    editable={canEdit}
                    disabled={addContactTag.isPending || removeContactTag.isPending}
                    onAdd={(tagId) => addContactTag.mutate(tagId)}
                    onRemove={(tagId) => removeContactTag.mutate(tagId)}
                  />
                </InspectorField>
                {contactAttrs.map((def) => (
                  <InspectorField key={def.id} label={def.name}>
                    <AttributeInput
                      key={`${c.contact?.id}:${def.key}`}
                      def={def}
                      value={c.contact?.attributes[def.key]}
                      disabled={!canEdit || patchContactAttr.isPending}
                      onSave={(val) => patchContactAttr.mutate({ [def.key]: val })}
                    />
                  </InspectorField>
                ))}
                <Box mt={4}>
                  <ContactNotesCard
                    contactId={c.contact.id}
                    value={serverNotes}
                    title={t("inbox.contact_notes")}
                    placeholder={t("inbox.contact_notes_placeholder")}
                    editable={can("contacts:update")}
                    disabled={contactDetail.isPending}
                    saving={saveNotes.isPending}
                    maxRows={5}
                    onSave={(next) => saveNotes.mutate(next)}
                  />
                </Box>
              </>
            )}
          </InspectorSection>

          <InspectorSection
            title={t("inbox.details_history")}
            collapsible
            storeKey="inbox-details-history"
            extra={
              others.length > 0 ? (
                <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
                  {others.length}
                </Text>
              ) : undefined
            }
          >
            {others.length === 0 ? (
              <Text size="xs" c="dimmed">
                {t("inbox.no_history")}
              </Text>
            ) : (
              <Stack gap={4}>
                {others.map((x) => (
                  <Group key={x.id} gap={4} wrap="nowrap" align="center">
                    <UnstyledButton onClick={() => onOpen(x.id)} className="inbox-history" px={6} py={5} style={{ flex: 1, minWidth: 0 }}>
                      <Text size="sm" truncate>
                        {previewSummary(x.preview, x.preview_attachment, t) || "—"}
                      </Text>
                      <Group gap={5} wrap="nowrap" mt={2}>
                        {x.channel && <ChannelIcon kind={x.channel.kind} size={11} />}
                        <Text size="xs" c="dimmed" truncate>
                          {[x.channel?.name, t(`inbox.status_${x.status}` as const), activity(x.last_message_at, language, t, zone)]
                            .filter(Boolean)
                            .join(" · ")}
                        </Text>
                      </Group>
                    </UnstyledButton>
                    {canEdit && (
                      <Tooltip label={t("inbox.merge_conversation")} openDelay={250}>
                        <ActionIcon
                          size={24}
                          variant="subtle"
                          color="gray"
                          aria-label={t("inbox.merge_conversation")}
                          onClick={() => setMergeSource(x)}
                        >
                          <IconGitMerge size={14} />
                        </ActionIcon>
                      </Tooltip>
                    )}
                  </Group>
                ))}
              </Stack>
            )}
          </InspectorSection>
        </Inspector>
      </PaneBody>
      <MergeContactModal
        opened={mergeContactOpen}
        target={
          c.contact
            ? {
                ...c.contact,
                external_user_id: contactDetail.data?.external_user_id,
              }
            : undefined
        }
        onClose={() => setMergeContactOpen(false)}
      />
      <Modal
        opened={Boolean(mergeSource)}
        onClose={() => setMergeSource(null)}
        title={t("inbox.merge_conversation_title")}
        size="sm"
        centered
      >
        <Stack gap="sm">
          <Text size="sm" fw={500}>
            {t("inbox.merge_conversation_confirm", { channel: mergeSource?.channel?.name ?? "—" })}
          </Text>
          <Text size="xs" c="dimmed">
            {t("inbox.merge_conversation_desc")}
          </Text>
          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={() => setMergeSource(null)} disabled={mergeConversation.isPending}>
              {t("common.cancel")}
            </Button>
            <Button
              leftSection={<IconGitMerge size={14} />}
              loading={mergeConversation.isPending}
              onClick={() => mergeSource && mergeConversation.mutate(mergeSource.id)}
            >
              {t("inbox.merge_conversation_submit")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}
