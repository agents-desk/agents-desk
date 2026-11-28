import type { ContactKind, ContactSort, ContactViewConfig, ContactViewDTO, ContactViewSummary, SavedviewScope } from "@agents-desk/client/tenant"
import { Button, Group, Modal, SegmentedControl, Select, Stack, TextInput } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { notifyError, notifySuccess, useCan, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { useTags } from "../inbox/data"
import { LanguageSelect } from "../LanguageSelect"

/** ContactViewModal 是新建或编辑联系人保存筛选视图的弹窗。 */
export function ContactViewModal({
  opened,
  onClose,
  editing,
  initialConfig,
  onSaved,
}: {
  opened: boolean
  onClose: () => void
  editing?: ContactViewSummary
  initialConfig?: ContactViewConfig
  onSaved?: (v: ContactViewDTO) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const can = useCan()
  const canManageShared = can("views:manage")
  const queryClient = useQueryClient()
  const summary = useQuery(kit.queries.contact.summary())
  const tags = useTags()

  const [name, setName] = useState("")
  const [scope, setScope] = useState<SavedviewScope>("personal")
  const [kind, setKind] = useState<ContactKind | "">("")
  const [q, setQ] = useState("")
  const [language, setLanguage] = useState("")
  const [channelId, setChannelId] = useState("")
  const [tagId, setTagId] = useState("")
  const [sort, setSort] = useState<ContactSort>("recent")

  useEffect(() => {
    if (!opened) return
    const cfg = editing?.config ?? initialConfig
    setName(editing?.name ?? "")
    setScope(editing?.scope ?? "personal")
    setKind(cfg?.kind ?? "")
    setQ(cfg?.q ?? "")
    setLanguage(cfg?.language ?? "")
    setChannelId(cfg?.channel_id ?? "")
    setTagId(cfg?.tag_id ?? "")
    setSort(cfg?.sort ?? "recent")
  }, [opened, editing, initialConfig])

  const buildConfig = (): ContactViewConfig => ({
    kind: kind || undefined,
    q: q.trim() || undefined,
    language: language || undefined,
    channel_id: channelId || undefined,
    tag_id: tagId || undefined,
    sort,
  })

  const save = useMutation({
    mutationFn: () => {
      const trimmed = name.trim()
      const config = buildConfig()
      if (editing) {
        return tenantApi.contact.viewUpdate(editing.id, { name: trimmed, config })
      }
      return tenantApi.contact.viewCreate({ name: trimmed, scope, config })
    },
    onSuccess: (saved) => {
      notifySuccess(t(editing ? "contacts.view_updated" : "contacts.view_created"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      onClose()
      onSaved?.(saved)
    },
    onError: (err) => notifyError(err),
  })

  const kindOptions = kit.enums.ContactKind.values.map((v) => ({ value: v, label: kit.label("ContactKind", v) }))
  const activeLanguages = summary.data?.languages ?? []
  const channelOptions = (summary.data?.channels ?? []).map((ch) => ({ value: ch.id, label: ch.name }))
  const tagOptions = tags.map((tg) => ({ value: tg.id, label: tg.name }))
  const sortOptions = kit.enums.ContactSort.values.map((v) => ({ value: v, label: kit.label("ContactSort", v) }))

  return (
    <Modal opened={opened} onClose={onClose} title={t(editing ? "contacts.view_edit" : "contacts.view_create")} size="md" radius="md" closeOnClickOutside={false}>
      <Stack gap="sm">
        <TextInput
          label={t("contacts.view_name")}
          placeholder={t("contacts.view_name_placeholder")}
          required
          maxLength={50}
          data-autofocus
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
        />
        {!editing && canManageShared && (
          <SegmentedControl
            size="xs"
            value={scope}
            onChange={(v) => setScope(v as SavedviewScope)}
            data={[
              { value: "personal", label: t("views.scope_personal") },
              { value: "shared", label: t("views.scope_shared") },
            ]}
          />
        )}
        <Select
          label={t("contacts.col_kind")}
          placeholder={t("contacts.filter_kind")}
          clearable
          data={kindOptions}
          value={kind || null}
          onChange={(v) => setKind((v as ContactKind) || "")}
        />
        <TextInput
          label={t("contacts.view_keyword")}
          placeholder={t("contacts.search_placeholder")}
          maxLength={100}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
        />
        <LanguageSelect
          label={t("contacts.language")}
          placeholder={t("contacts.filter_language")}
          clearable
          first={activeLanguages}
          value={language || null}
          onChange={(v) => setLanguage(v || "")}
        />
        {channelOptions.length > 0 && (
          <Select
            label={t("contacts.col_channel")}
            placeholder={t("contacts.filter_channel")}
            clearable
            data={channelOptions}
            value={channelId || null}
            onChange={(v) => setChannelId(v || "")}
          />
        )}
        {tagOptions.length > 0 && (
          <Select
            label={t("inbox.tags")}
            placeholder={t("contacts.filter_tag")}
            clearable
            searchable
            data={tagOptions}
            value={tagId || null}
            onChange={(v) => setTagId(v || "")}
          />
        )}
        <Select
          label={t("contacts.sort")}
          allowDeselect={false}
          data={sortOptions}
          value={sort}
          onChange={(v) => v && setSort(v as ContactSort)}
        />
        <Group justify="flex-end" gap="xs" mt="xs">
          <Button variant="default" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button disabled={!name.trim()} loading={save.isPending} onClick={() => save.mutate()}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
