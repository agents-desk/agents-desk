import type { ContactDetailDTO, ContactIdentityDTO, ContactUpdateBody } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Box,
  Button,
  Center,
  Code,
  CopyButton,
  Group,
  Loader,
  Modal,
  Select,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core"
import { type QueryClient, queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { IconArrowUpRight, IconBan, IconCheck, IconCopy, IconExternalLink, IconGitMerge, IconInfoCircle, IconPencil, IconShieldCheck, IconTrash, IconX } from "@tabler/icons-react"
import { confirm, formatTimeZoneLocal, formatTimeZoneOffset, Inspector, InspectorField, InspectorSection, InspectorSkeleton, listTimeZones, notifyError, notifySuccess, PaneBody, PaneHeader, prefetchQueries, type PreloadQuery, StatusDot, useCan, useLanguage, useNarrow, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useMemo, useState } from "react"
import { kit, tenantApi } from "../api"
import { previewSummary } from "../inbox/Attachments"
import { useMembers } from "../inbox/data"
import { activity, conversationStatusColor } from "../inbox/format"
import { LanguageSelect } from "../LanguageSelect"
import { languageName } from "../languages"
import { NARROW_QUERY } from "../layout"
import { contactTicketsQuery, ticketStatusColor, ticketStatusLabel, useContactTickets } from "../tickets/meta"
import { ContactNotesCard } from "./ContactNotesCard"
import { ContactAvatar, contactDisplayName, contactKindColor } from "./display"
import { MergeContactModal } from "./MergeContactModal"

/** AUTO 是语言选择器中的“自动识别”。 */
const AUTO = "auto"

/**
 * ContactDetailPanel 是联系人列表右侧的分栏档案（窄屏占满）：
 * 使用框架的 PaneHeader/PaneBody 与 Inspector 分区展示内部备注便签、可就地编辑的资料属性、历史会话流和渠道身份。
 */
export function ContactDetailPanel({ slug, id, onClose }: { slug: string; id: string; onClose: () => void }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const narrow = useNarrow(NARROW_QUERY)
  const can = useCan()
  const queryClient = useQueryClient()
  const [mergeOpen, setMergeOpen] = useState(false)
  const [blockModalOpen, setBlockModalOpen] = useState(false)
  const [blockReason, setBlockReason] = useState("")

  const detail = useQuery(kit.queries.contact.get(id))
  const translation = useQuery(kit.queries.translation.settings())
  const translating = translation.data?.enabled ?? false
  const conversations = useQuery(contactConversationsQuery(id))
  const contactTickets = useContactTickets(can("conversations:read") ? id : undefined)

  const update = useMutation({
    mutationFn: (body: ContactUpdateBody) => tenantApi.contact.update(id, body),
    onSuccess: (saved) => {
      queryClient.setQueryData(kit.queries.contact.get(id).queryKey, saved)
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      notifySuccess(t("common.saved"))
    },
    onError: (err) => notifyError(err),
  })
  const block = useMutation({
    mutationFn: () => tenantApi.contact.block(id, { reason: blockReason.trim() || undefined }),
    onSuccess: (saved) => {
      setBlockModalOpen(false)
      queryClient.setQueryData(kit.queries.contact.get(id).queryKey, saved)
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      notifySuccess(t("contacts.blocked_done"))
    },
    onError: (err) => notifyError(err),
  })
  const unblock = useMutation({
    mutationFn: () => tenantApi.contact.unblock(id),
    onSuccess: (saved) => {
      queryClient.setQueryData(kit.queries.contact.get(id).queryKey, saved)
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      notifySuccess(t("contacts.unblocked_done"))
    },
    onError: (err) => notifyError(err),
  })
  const remove = useMutation({
    mutationFn: () => tenantApi.contact.delete(id),
    onSuccess: (res) => {
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      if (res.mode === "deleted") {
        notifySuccess(t("contacts.deleted_done"))
        onClose()
      } else {
        void queryClient.invalidateQueries({ queryKey: kit.queries.contact.get(id).queryKey })
        notifySuccess(t("contacts.anonymized_done"))
      }
    },
    onError: (err) => notifyError(err),
  })

  const closeAction = !narrow && (
    <ActionIcon variant="subtle" color="gray" onClick={onClose} aria-label={t("contacts.close_detail")}>
      <IconX size={16} />
    </ActionIcon>
  )

  // 还没有数据时显示档案同样布局的骨架，数据到达后只替换内容
  if (detail.isPending) {
    return <InspectorSkeleton back={narrow ? onClose : undefined} onClose={narrow ? undefined : onClose} closeLabel={t("contacts.close_detail")} />
  }

  if (!detail.data) {
    return (
      <Stack gap={0} h="100%" w="100%">
        <PaneHeader title={t("nav.contacts")} back={narrow ? onClose : undefined} actions={closeAction} />
        <Center style={{ flex: 1 }}>
          <Text size="sm" c="dimmed">
            {t("contacts.not_found")}
          </Text>
        </Center>
      </Stack>
    )
  }

  const c = detail.data
  const displayName = contactDisplayName(c, t)
  const localTime = formatTimeZoneLocal(c.time_zone)
  const subtitle = [c.location, localTime ? t("contacts.local_time", { time: localTime }) : ""].filter(Boolean).join(" · ")
  const editable = can("contacts:update")
  const showNumberBadge = c.number > 0 && Boolean(c.name.trim() || c.email.trim())

  return (
    <Stack gap={0} h="100%" w="100%">
      {/* 顶部人物头：高 52px，与左侧顶栏齐平 */}
      <PaneHeader
        back={narrow ? onClose : undefined}
        left={<ContactAvatar contact={c} size={32} />}
        title={
          <Group gap={6} wrap="nowrap">
            <Text fw={600} size="sm" truncate lh={1.25}>
              {displayName}
            </Text>
            {showNumberBadge && (
              <Text size="xs" c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                #{c.number}
              </Text>
            )}
            <Badge
              size="xs"
              variant="light"
              color={contactKindColor[c.kind]}
              style={{ flex: "none" }}
            >
              {kit.label("ContactKind", c.kind)}
            </Badge>
            {c.blocked_at && (
              <Badge size="xs" variant="light" color="red" style={{ flex: "none" }}>
                {t("contacts.blocked_badge")}
              </Badge>
            )}
          </Group>
        }
        subtitle={subtitle || undefined}
        actions={
          <Group gap={4} wrap="nowrap">
            {editable &&
              (c.blocked_at ? (
                <Tooltip label={t("contacts.unblock")} openDelay={250}>
                  <ActionIcon
                    variant="subtle"
                    color="green"
                    loading={unblock.isPending}
                    onClick={() =>
                      void confirm({
                        title: t("contacts.unblock_confirm", { name: displayName }),
                        message: t("contacts.unblock_message"),
                        confirmLabel: t("contacts.unblock"),
                      }).then((ok) => {
                        if (ok) unblock.mutate()
                      })
                    }
                    aria-label={t("contacts.unblock")}
                  >
                    <IconShieldCheck size={16} />
                  </ActionIcon>
                </Tooltip>
              ) : (
                <Tooltip label={t("contacts.block")} openDelay={250}>
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    onClick={() => {
                      setBlockReason("")
                      setBlockModalOpen(true)
                    }}
                    aria-label={t("contacts.block")}
                  >
                    <IconBan size={16} />
                  </ActionIcon>
                </Tooltip>
              ))}
            {editable && (
              <Tooltip label={t("contacts.merge")} openDelay={250}>
                <ActionIcon variant="subtle" color="gray" onClick={() => setMergeOpen(true)} aria-label={t("contacts.merge")}>
                  <IconGitMerge size={16} />
                </ActionIcon>
              </Tooltip>
            )}
            {editable && (
              <Tooltip label={t("contacts.delete")} openDelay={250}>
                <ActionIcon
                  variant="subtle"
                  color="red"
                  loading={remove.isPending}
                  onClick={() =>
                    void confirm({
                      title: t("contacts.delete_confirm", { name: displayName }),
                      message: t("contacts.delete_message"),
                      danger: true,
                      confirmLabel: t("contacts.delete"),
                    }).then((ok) => {
                      if (ok) remove.mutate()
                    })
                  }
                  aria-label={t("contacts.delete")}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Tooltip>
            )}
            {closeAction}
          </Group>
        }
      />

      {/* 独立滚动的档案正文 */}
      <PaneBody>
        {c.blocked_at && (
          <Alert variant="light" color="red" radius={0} py={8} px="var(--tn-pane-px)" icon={<IconInfoCircle size={16} />}>
            <Text size="xs">
              {c.blocked_reason ? t("contacts.blocked_banner_reason", { reason: c.blocked_reason }) : t("contacts.blocked_banner")}
            </Text>
          </Alert>
        )}
        <Inspector>
          {/* 1. 置顶内部备注便签 */}
          <Box px="var(--tn-pane-px)" pt="md" pb="xs">
            <ContactNotesCard
              contactId={c.id}
              value={c.notes}
              translatedValue={c.notes_translated}
              sourceLanguage={c.notes_language}
              untranslated={c.notes_untranslated}
              translating={translating}
              title={t("contacts.notes")}
              placeholder={t("contacts.notes_placeholder")}
              editable={editable}
              saving={update.isPending}
              onSave={(notes) => update.mutate({ notes })}
            />
          </Box>

          {/* 2. 可就地编辑的资料属性 */}
          <PropertiesSection contact={c} editable={editable} saving={update.isPending} onUpdate={(patch) => update.mutateAsync(patch)} />

          {/* 3. 历史会话流 */}
          <InspectorSection
            title={t("contacts.conversations")}
            extra={
              <Badge size="xs" variant="light" color="gray" radius="sm">
                {c.conversation_count}
              </Badge>
            }
          >
            {conversations.isPending ? (
              <Center py="md">
                <Loader size="xs" />
              </Center>
            ) : (conversations.data?.items.length ?? 0) === 0 ? (
              <Text size="xs" c="dimmed">
                {t("contacts.no_conversations")}
              </Text>
            ) : (
              <Stack gap={8}>
                {conversations.data?.items.map((conv) => (
                  <Link
                    key={conv.id}
                    to="/$slug/inbox/$id"
                    params={{ slug, id: conv.id }}
                    search={{ box: "all", status: conv.status, sort: "waiting" }}
                    className="ct-conv"
                  >
                    <Box p="xs">
                      <Group justify="space-between" wrap="nowrap" mb={4}>
                        <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                          <StatusDot color={conversationStatusColor[conv.status]} />
                          <Text size="xs" fw={500}>
                            {t(`inbox.status_${conv.status}` as const)}
                          </Text>
                          {conv.channel && (
                            <Text size="xs" c="dimmed" truncate>
                              · {conv.channel.name}
                            </Text>
                          )}
                          {conv.rating && (
                            <Text size="xs" c="yellow">
                              {"★".repeat(conv.rating.score)}
                            </Text>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                          {activity(conv.last_message_at, ui, t, zone)}
                        </Text>
                      </Group>
                      <Group justify="space-between" align="flex-end" gap="xs" wrap="nowrap">
                        <Text
                          size="sm"
                          c={conv.preview || conv.preview_attachment ? undefined : "dimmed"}
                          lineClamp={2}
                          style={{ flex: 1, minWidth: 0 }}
                        >
                          {previewSummary(conv.preview, conv.preview_attachment, t, conv.preview_recalled) || "—"}
                        </Text>
                        <Text
                          size="xs"
                          fw={500}
                          c="brand"
                          style={{ flex: "none", display: "inline-flex", alignItems: "center", gap: 2 }}
                        >
                          <span>{t("contacts.open_in_inbox")}</span>
                          <IconArrowUpRight size={12} />
                        </Text>
                      </Group>
                    </Box>
                  </Link>
                ))}
              </Stack>
            )}
          </InspectorSection>

          {/* 4. 关联工单 */}
          {can("conversations:read") && (
            <InspectorSection
              title={t("tickets.contact_section")}
              extra={
                (contactTickets.data?.items.length ?? 0) > 0 ? (
                  <Badge size="xs" variant="light" color="gray" radius="sm">
                    {contactTickets.data?.items.length}
                  </Badge>
                ) : undefined
              }
            >
              {contactTickets.isPending ? (
                <Center py="md">
                  <Loader size="xs" />
                </Center>
              ) : (contactTickets.data?.items.length ?? 0) === 0 ? (
                <Text size="xs" c="dimmed">
                  {t("tickets.contact_empty")}
                </Text>
              ) : (
                <Stack gap={8}>
                  {contactTickets.data?.items.map((tk) => (
                    <Link
                      key={tk.id}
                      to="/$slug/inbox/tickets/$id"
                      params={{ slug, id: tk.id }}
                      search={{ box: "all", status: "unresolved" }}
                      className="ct-conv"
                    >
                      <Box p="xs">
                        <Group justify="space-between" wrap="nowrap" mb={4}>
                          <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                            <Text size="xs" fw={600} c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                              #{tk.number}
                            </Text>
                            <Badge size="xs" variant="light" color={ticketStatusColor[tk.status]} style={{ flex: "none" }}>
                              {ticketStatusLabel(tk.status, t)}
                            </Badge>
                            {tk.type_name && (
                              <Text size="xs" c="dimmed" truncate>
                                · {tk.type_name}
                              </Text>
                            )}
                          </Group>
                          <Text size="xs" c="dimmed" style={{ flex: "none", fontVariantNumeric: "tabular-nums" }}>
                            {activity(tk.last_message_at, ui, t, zone)}
                          </Text>
                        </Group>
                        <Group justify="space-between" align="center" gap="xs" wrap="nowrap">
                          <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }}>
                            {tk.title_translated || tk.title}
                          </Text>
                          <Text size="xs" fw={500} c="brand" style={{ flex: "none", display: "inline-flex", alignItems: "center", gap: 2 }}>
                            <span>{t("tickets.open_ticket")}</span>
                            <IconArrowUpRight size={12} />
                          </Text>
                        </Group>
                      </Box>
                    </Link>
                  ))}
                </Stack>
              )}
            </InspectorSection>
          )}

          {/* 5. 渠道身份 */}
          <IdentitiesSection identities={c.identities} />
        </Inspector>
      </PaneBody>
      <MergeContactModal opened={mergeOpen} target={c} onClose={() => setMergeOpen(false)} />
      <Modal opened={blockModalOpen} onClose={() => setBlockModalOpen(false)} title={t("contacts.block_confirm_title")} centered closeOnClickOutside={false}>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("contacts.block_desc")}
          </Text>
          <TextInput
            label={t("contacts.block_reason_label")}
            placeholder={t("contacts.block_reason_placeholder")}
            maxLength={500}
            value={blockReason}
            onChange={(e) => setBlockReason(e.currentTarget.value)}
          />
          <Group justify="flex-end" gap="xs">
            <Button variant="default" onClick={() => setBlockModalOpen(false)}>
              {t("inbox.cancel")}
            </Button>
            <Button color="red" loading={block.isPending} onClick={() => block.mutate()}>
              {t("contacts.block")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}

/** InlineTextField 是默认纯文本阅读态、点编辑或双击就地修改的单行字段。阅读态与编辑态均为 13px 字号。 */
function InlineTextField({
  value,
  placeholder,
  maxLength,
  editable,
  copyable,
  onSave,
}: {
  value: string
  placeholder: string
  maxLength: number
  editable: boolean
  copyable?: boolean
  onSave: (next: string) => Promise<unknown>
}): ReactNode {
  const t = useT()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)

  useEffect(() => {
    setDraft(value)
  }, [value])

  const commit = async () => {
    const trimmed = draft.trim()
    setEditing(false)
    if (trimmed === value) return
    try {
      await onSave(trimmed)
    } catch {
      setDraft(value)
    }
  }

  if (editing) {
    return (
      <TextInput
        autoFocus
        maxLength={maxLength}
        placeholder={placeholder}
        value={draft}
        onChange={(e) => setDraft(e.currentTarget.value)}
        onBlur={() => void commit()}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            void commit()
          } else if (e.key === "Escape") {
            setDraft(value)
            setEditing(false)
          }
        }}
      />
    )
  }

  return (
    <Group gap={4} wrap="nowrap" justify="space-between">
      <Text
        size="sm"
        c={value ? undefined : "dimmed"}
        truncate
        onDoubleClick={editable ? () => setEditing(true) : undefined}
        style={{ flex: 1, minWidth: 0 }}
      >
        {value || "—"}
      </Text>
      <Group gap={2} wrap="nowrap" className="ct-hover-action" style={{ flex: "none" }}>
        {copyable && value && (
          <CopyButton value={value} timeout={1500}>
            {({ copied, copy }) => (
              <Tooltip label={t(copied ? "contacts.copied" : "contacts.copy")}>
                <ActionIcon variant="subtle" color={copied ? "green" : "gray"} size={22} onClick={copy} aria-label={t("contacts.copy")}>
                  {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                </ActionIcon>
              </Tooltip>
            )}
          </CopyButton>
        )}
        {editable && (
          <Tooltip label={t("contacts.edit")}>
            <ActionIcon variant="subtle" color="gray" size={22} onClick={() => setEditing(true)} aria-label={t("contacts.edit")}>
              <IconPencil size={14} />
            </ActionIcon>
          </Tooltip>
        )}
      </Group>
    </Group>
  )
}

/** PropertiesSection 是可就地编辑的资料属性区。 */
function PropertiesSection({
  contact: c,
  editable,
  saving,
  onUpdate,
}: {
  contact: ContactDetailDTO
  editable: boolean
  saving: boolean
  onUpdate: (patch: ContactUpdateBody) => Promise<unknown>
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const { list: members } = useMembers()
  const [editingZone, setEditingZone] = useState(false)
  const zones = useMemo(() => listTimeZones().map((z) => ({ value: z, label: `${z} (${formatTimeZoneOffset(z)})` })), [])
  const orgsQuery = useQuery({
    queryKey: ["organization", "dropdown"],
    queryFn: () => tenantApi.organization.list({ per_page: 100 }),
  })
  const orgOptions = useMemo(() => {
    const items = (orgsQuery.data?.items ?? []).map((org) => ({ value: org.id, label: org.name }))
    if (c.organization && !items.some((item) => item.value === c.organization?.id)) {
      items.unshift({ value: c.organization.id, label: c.organization.name })
    }
    return items
  }, [orgsQuery.data?.items, c.organization])
  const detected = c.language ? languageName(c.language, ui) : ""
  const localTime = formatTimeZoneLocal(c.time_zone)

  return (
    <InspectorSection title={t("contacts.profile")} withDivider={false}>
      {c.kind === "internal" && (
        <InspectorField label={t("contacts.linked_member")} className="ct-field">
          <Select
            placeholder={t("contacts.linked_member_placeholder")}
            value={c.member_id ?? null}
            data={members.map((m) => ({ value: m.id, label: m.email ? `${m.name} (${m.email})` : m.name }))}
            searchable
            clearable
            disabled={!editable || saving}
            onChange={(v) => void onUpdate({ member_id: v ?? "" })}
            aria-label={t("contacts.linked_member")}
          />
        </InspectorField>
      )}
      <InspectorField label={t("contacts.name")} className="ct-field">
        <InlineTextField
          value={c.name}
          placeholder={t("contacts.name_placeholder")}
          maxLength={120}
          editable={editable}
          onSave={(name) => onUpdate({ name })}
        />
      </InspectorField>

      <InspectorField label={t("contacts.company")} className="ct-field">
        <InlineTextField
          value={c.company}
          placeholder={t("contacts.company_placeholder")}
          maxLength={120}
          editable={editable}
          onSave={(company) => onUpdate({ company })}
        />
      </InspectorField>
      <InspectorField label={t("contacts.organization")} className="ct-field">
        {editable ? (
          <Select
            size="xs"
            searchable
            clearable
            placeholder={t("contacts.organization_placeholder")}
            data={orgOptions}
            value={c.organization_id ?? c.organization?.id ?? null}
            onChange={(v) => {
              void onUpdate({ organization_id: v ?? "" })
            }}
          />
        ) : (
          <Text size="sm" c={c.organization ? undefined : "dimmed"}>
            {c.organization?.name || t("contacts.organization_placeholder")}
          </Text>
        )}
      </InspectorField>

      <InspectorField label={t("contacts.email")} className="ct-field">
        <InlineTextField
          value={c.email}
          placeholder={t("contacts.email_placeholder")}
          maxLength={254}
          editable={editable}
          copyable
          onSave={(email) => onUpdate({ email })}
        />
      </InspectorField>

      {c.external_user_id && (
        <InspectorField label={t("contacts.external_user_id")} className="ct-field">
          <Group gap={4} wrap="nowrap" justify="space-between" style={{ minWidth: 0 }}>
            <Code className="ct-code">{c.external_user_id}</Code>
            <CopyButton value={c.external_user_id} timeout={1500}>
              {({ copied, copy }) => (
                <Tooltip label={t(copied ? "contacts.copied" : "contacts.copy")}>
                  <ActionIcon
                    className="ct-hover-action"
                    variant="subtle"
                    color={copied ? "green" : "gray"}
                    size={22}
                    style={{ flex: "none" }}
                    onClick={copy}
                    aria-label={t("contacts.copy")}
                  >
                    {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
                  </ActionIcon>
                </Tooltip>
              )}
            </CopyButton>
          </Group>
        </InspectorField>
      )}

      <InspectorField label={t("contacts.language")} align={c.language_manual ? "start" : "center"} className="ct-field">
        <LanguageSelect
          before={[
            {
              value: AUTO,
              label: !c.language_manual && detected ? t("inbox.language_auto_detected", { lang: detected }) : t("inbox.language_auto"),
            },
          ]}
          first={c.language ? [c.language] : []}
          value={c.language_manual ? c.language : AUTO}
          onChange={(v) => v && void onUpdate({ language: v === AUTO ? "" : v })}
          allowDeselect={false}
          disabled={!editable || saving}
          aria-label={t("contacts.language")}
        />
        {c.language_manual && (
          <Text size="xs" c="dimmed" mt={2}>
            {t("inbox.language_manual_hint")}
          </Text>
        )}
      </InspectorField>

      <InspectorField label={t("contacts.location")} className="ct-field">
        <InlineTextField
          value={c.location}
          placeholder={t("contacts.location_placeholder")}
          maxLength={120}
          editable={editable}
          onSave={(location) => onUpdate({ location })}
        />
      </InspectorField>

      <InspectorField label={t("contacts.time_zone")} className="ct-field">
        {editingZone ? (
          <Select
            autoFocus
            defaultDropdownOpened
            placeholder={t("contacts.time_zone_placeholder")}
            data={zones}
            searchable
            clearable
            nothingFoundMessage={t("preferences.no_time_zone")}
            value={c.time_zone || null}
            onChange={(v) => {
              setEditingZone(false)
              const next = v ?? ""
              if (next !== c.time_zone) void onUpdate({ time_zone: next })
            }}
            onDropdownClose={() => setEditingZone(false)}
          />
        ) : (
          <Group gap={4} wrap="nowrap" justify="space-between">
            <Text
              size="sm"
              c={c.time_zone ? undefined : "dimmed"}
              truncate
              onDoubleClick={editable ? () => setEditingZone(true) : undefined}
              style={{ flex: 1, minWidth: 0 }}
            >
              {c.time_zone ? (localTime ? `${c.time_zone} · ${localTime}` : c.time_zone) : "—"}
            </Text>
            {editable && (
              <Tooltip label={t("contacts.edit")}>
                <ActionIcon
                  className="ct-hover-action"
                  variant="subtle"
                  color="gray"
                  size={22}
                  onClick={() => setEditingZone(true)}
                  aria-label={t("contacts.edit")}
                >
                  <IconPencil size={14} />
                </ActionIcon>
              </Tooltip>
            )}
          </Group>
        )}
      </InspectorField>

      {c.referrer && (
        <InspectorField label={t("contacts.referrer")} className="ct-field">
          <Anchor
            href={c.referrer}
            target="_blank"
            rel="noopener noreferrer"
            size="sm"
            style={{ display: "inline-flex", alignItems: "center", gap: 4, maxWidth: "100%" }}
          >
            <Text span size="sm" truncate>
              {c.referrer}
            </Text>
            <IconExternalLink size={12} style={{ flex: "none" }} />
          </Anchor>
        </InspectorField>
      )}

      <InspectorField label={t("contacts.created_at")}>
        <Text size="sm" c="dimmed">
          {activity(c.created_at, ui, t, zone)}
        </Text>
      </InspectorField>

      {c.last_seen_at && (
        <InspectorField label={t("contacts.last_seen_at")}>
          <Text size="sm" c="dimmed">
            {activity(c.last_seen_at, ui, t, zone)}
          </Text>
        </InspectorField>
      )}
    </InspectorSection>
  )
}

/** IdentitiesSection 是渠道身份列表区。 */
function IdentitiesSection({ identities }: { identities: ContactIdentityDTO[] }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  return (
    <InspectorSection
      title={t("contacts.channel_identities")}
      extra={
        <Badge size="xs" variant="light" color="gray" radius="sm">
          {identities.length}
        </Badge>
      }
    >
      {identities.length === 0 ? (
        <Text size="xs" c="dimmed">
          {t("contacts.identities_empty")}
        </Text>
      ) : (
        <Stack gap="xs">
          {identities.map((ident) => (
            <Box key={`${ident.channel.id}:${ident.external_id}`} className="ct-identity">
              <Group justify="space-between" wrap="nowrap" gap={6}>
                <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                  <Text size="sm" fw={500} truncate>
                    {ident.channel.name}
                  </Text>
                  <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                    {kit.label("ChannelKind", ident.channel.kind)}
                  </Badge>
                </Group>
                <Text size="xs" c="dimmed" style={{ flex: "none" }}>
                  {activity(ident.created_at, ui, t, zone)}
                </Text>
              </Group>
              <Group gap={4} wrap="nowrap" mt={3} style={{ minWidth: 0 }}>
                <Code className="ct-code">{ident.external_id}</Code>
                <CopyButton value={ident.external_id} timeout={1500}>
                  {({ copied, copy }) => (
                    <Tooltip label={t(copied ? "contacts.copied" : "contacts.copy")}>
                      <ActionIcon
                        className="ct-hover-action"
                        variant="subtle"
                        color={copied ? "green" : "gray"}
                        size={20}
                        style={{ flex: "none" }}
                        onClick={copy}
                        aria-label={t("contacts.copy")}
                      >
                        {copied ? <IconCheck size={12} /> : <IconCopy size={12} />}
                      </ActionIcon>
                    </Tooltip>
                  )}
                </CopyButton>
              </Group>
            </Box>
          ))}
        </Stack>
      )}
    </InspectorSection>
  )
}

/** contactConversationsQuery 是联系人档案中“会话”一栏的查询（档案与预取共用）。 */
function contactConversationsQuery(id: string) {
  return queryOptions({
    queryKey: [...kit.queries.conversation.key(), "contact-detail", id],
    queryFn: () => tenantApi.conversation.list({ contact_id: id, per_page: 30, sort: "recent" }),
  })
}

/** contactQueries 是打开联系人 id 的档案要用的查询：档案、会话和关联工单（路由 loader 与预取共用）。 */
export function contactQueries(id: string): PreloadQuery[] {
  return [kit.queries.contact.get(id), contactConversationsQuery(id), contactTicketsQuery(id)]
}

/** prefetchContact 预取联系人 id 的档案：列表行悬停时用。 */
export function prefetchContact(queryClient: QueryClient, id: string): Promise<void> {
  return prefetchQueries(queryClient, contactQueries(id))
}
