import type { ConversationDTO } from "@agents-desk/client/tenant"
import { Accordion, ActionIcon, Box, Group, Select, Stack, Text, UnstyledButton } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconX } from "@tabler/icons-react"
import { notifyError, useCan, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit, tenantApi } from "../api"
import { LanguageSelect } from "../LanguageSelect"
import { languageName } from "../languages"
import { useAttributes, useContactHistory, useMe, useMembers, useTags, useTeams } from "./data"
import { AttributeInput, TagsEditor } from "./fields"
import { activity } from "./format"

/** Field 是详情中的一行：左边是名称，右边是值。 */
function Field({ label, children }: { label: string; children: ReactNode }): ReactNode {
  return (
    <Group gap="xs" wrap="nowrap" align="center">
      <Text size="xs" c="dimmed" w={76} style={{ flex: "none" }}>
        {label}
      </Text>
      <Box style={{ flex: 1, minWidth: 0 }}>{children}</Box>
    </Group>
  )
}

/** AUTO 是对方语言中的“自动识别”：按收到的消息识别。 */
const AUTO = "auto"

/**
 * Details 是会话的详情栏（第五栏）：会话（负责人、团队收件箱、优先级、标签和自定义属性可以直接修改）、联系人（含标签和自定义属性）、
 * 同一个联系人的其他会话。联系人的语言可以手动设置，识别错了时改成正确的；选“自动识别”恢复按收到的消息更新。各部分可以折叠。
 * 联系人的所在地和来源页面在 U1 中加入。
 */
export function Details({ c, onClose, onOpen }: { c: ConversationDTO; onClose: () => void; onOpen: (id: string) => void }): ReactNode {
  const t = useT()
  const can = useCan()
  const language = useLanguage()
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const teams = useTeams()
  const allTags = useTags()
  const convAttrs = useAttributes("conversation")
  const contactAttrs = useAttributes("contact")
  const history = useContactHistory(c.contact?.id)
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
    onSuccess: invalidate,
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
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const removeContactTag = useMutation({
    mutationFn: (tagId: string) => tenantApi.conversation.contactTagRemove(c.id, tagId),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  const patchContactAttr = useMutation({
    mutationFn: (attributes: Record<string, unknown>) => tenantApi.conversation.contactAttributesPatch(c.id, { attributes }),
    onSuccess: invalidate,
    onError: (err) => notifyError(err),
  })
  // 对方的语言：手动设置的显示那种语言，否则显示“自动识别”（带上识别出的语言）
  const manual = c.contact?.language_manual ?? false
  const detected = c.language ? languageName(c.language, language) : ""
  // 没有“改派给别人”的权限时，负责人只能选自己
  const assignees = can("conversations:assign") ? members : members.filter((m) => m.id === me.data?.id)
  const others = (history.data?.items ?? []).filter((x) => x.id !== c.id)

  return (
    <Stack gap={0} h="100%" className="inbox-details">
      <Group px="md" py={10} className="inbox-divider">
        <Text fw={600} size="sm">
          {t("inbox.details")}
        </Text>
        <ActionIcon variant="subtle" color="gray" ml="auto" onClick={onClose} aria-label={t("inbox.close_details")}>
          <IconX size={16} />
        </ActionIcon>
      </Group>
      <Box style={{ flex: 1, overflowY: "auto" }}>
        <Accordion multiple defaultValue={["conversation", "contact", "history"]} chevronPosition="right" variant="default">
          <Accordion.Item value="conversation">
            <Accordion.Control>
              <Text size="xs" fw={600}>
                {t("inbox.details_conversation")}
              </Text>
            </Accordion.Control>
            <Accordion.Panel>
              <Stack gap={8}>
                <Field label={t("inbox.assignee")}>
                  <Select
                    size="xs"
                    placeholder={t("inbox.no_assignee")}
                    value={c.assignee?.type === "member" ? c.assignee.id : null}
                    data={assignees.map((m) => ({ value: m.id, label: m.id === me.data?.id ? t("inbox.you") : m.name }))}
                    onChange={(v) => assign.mutate({ assignee_id: v ?? "" })}
                    clearable={can("conversations:assign")}
                    searchable
                  />
                </Field>
                <Field label={t("inbox.team")}>
                  <Select
                    size="xs"
                    placeholder={t("inbox.no_team")}
                    value={c.team_id ?? null}
                    data={teams.map((x) => ({ value: x.id, label: x.name }))}
                    onChange={(v) => assign.mutate({ team_id: v ?? "" })}
                    clearable
                    disabled={!can("conversations:assign")}
                  />
                </Field>
                <Field label={t("inbox.priority")}>
                  <Select
                    size="xs"
                    allowDeselect={false}
                    value={c.priority}
                    data={[
                      { value: "normal", label: t("inbox.priority_normal") },
                      { value: "high", label: t("inbox.priority_high") },
                    ]}
                    onChange={(v) => v && priority.mutate(v as "normal" | "high")}
                  />
                </Field>
                <Field label={t("inbox.status")}>
                  <Text size="sm">{t(`inbox.status_${c.status}` as const)}</Text>
                </Field>
                <Field label={t("inbox.tags")}>
                  <TagsEditor
                    tags={c.tags}
                    allTags={allTags}
                    editable={canEdit}
                    disabled={addConvTag.isPending || removeConvTag.isPending}
                    onAdd={(tagId) => addConvTag.mutate(tagId)}
                    onRemove={(tagId) => removeConvTag.mutate(tagId)}
                  />
                </Field>
                {convAttrs.map((def) => (
                  <Field key={def.id} label={def.name}>
                    <AttributeInput
                      key={`${c.id}:${def.key}`}
                      def={def}
                      value={c.attributes[def.key]}
                      disabled={!canEdit || patchConvAttr.isPending}
                      onSave={(val) => patchConvAttr.mutate({ [def.key]: val })}
                    />
                  </Field>
                ))}
                {c.rating && (
                  <Field label={t("inbox.rating")}>
                    <Text size="sm">
                      {"★".repeat(c.rating.score)}
                      {c.rating.comment && (
                        <Text span size="xs" c="dimmed">
                          {" "}
                          {c.rating.comment}
                        </Text>
                      )}
                    </Text>
                  </Field>
                )}
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
          <Accordion.Item value="contact">
            <Accordion.Control>
              <Text size="xs" fw={600}>
                {t("inbox.details_contact")}
              </Text>
            </Accordion.Control>
            <Accordion.Panel>
              <Stack gap={8}>
                <Field label={t("inbox.contact_name")}>
                  <Text size="sm">{c.contact?.name || t("inbox.visitor")}</Text>
                </Field>
                <Field label={t("inbox.contact_kind")}>
                  <Text size="sm">{c.contact ? kit.label("ContactKind", c.contact.kind) : ""}</Text>
                </Field>
                <Field label={t("inbox.contact_email")}>
                  <Text size="sm" c={c.contact?.email ? undefined : "dimmed"}>
                    {c.contact?.email || "—"}
                  </Text>
                </Field>
                <Field label={t("inbox.contact_language")}>
                  <LanguageSelect
                    size="xs"
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
                </Field>
                <Field label={t("inbox.channel")}>
                  <Text size="sm">{c.channel?.name ?? "—"}</Text>
                </Field>
                {c.contact && (
                  <>
                    <Field label={t("inbox.tags")}>
                      <TagsEditor
                        tags={c.contact.tags}
                        allTags={allTags}
                        editable={canEdit}
                        disabled={addContactTag.isPending || removeContactTag.isPending}
                        onAdd={(tagId) => addContactTag.mutate(tagId)}
                        onRemove={(tagId) => removeContactTag.mutate(tagId)}
                      />
                    </Field>
                    {contactAttrs.map((def) => (
                      <Field key={def.id} label={def.name}>
                        <AttributeInput
                          key={`${c.contact?.id}:${def.key}`}
                          def={def}
                          value={c.contact?.attributes[def.key]}
                          disabled={!canEdit || patchContactAttr.isPending}
                          onSave={(val) => patchContactAttr.mutate({ [def.key]: val })}
                        />
                      </Field>
                    ))}
                  </>
                )}
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
          <Accordion.Item value="history">
            <Accordion.Control>
              <Text size="xs" fw={600}>
                {t("inbox.details_history")}
              </Text>
            </Accordion.Control>
            <Accordion.Panel>
              {others.length === 0 ? (
                <Text size="xs" c="dimmed">
                  {t("inbox.no_history")}
                </Text>
              ) : (
                <Stack gap={2}>
                  {others.map((x) => (
                    <UnstyledButton key={x.id} onClick={() => onOpen(x.id)} className="inbox-history" px={6} py={4}>
                      <Text size="sm" truncate>
                        {x.preview || "—"}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {t(`inbox.status_${x.status}` as const)} · {activity(x.last_message_at, language, t, zone)}
                      </Text>
                    </UnstyledButton>
                  ))}
                </Stack>
              )}
            </Accordion.Panel>
          </Accordion.Item>
        </Accordion>
      </Box>
    </Stack>
  )
}
