import type { TranslationSettingsDTO, TranslationTerm, TranslationTone } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, Button, Group, Modal, Radio, SegmentedControl, Stack, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus, IconTrash } from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { applyServerErrors, confirm, DataTable, FormError, notifyError, notifySuccess, Page, PageActions, preloadQueries, RowActions, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../../../../api"
import { useUnsavedGuard } from "../../../../components/unsaved-guard"
import { LanguageSelect } from "../../../../LanguageSelect"
import { languageName } from "../../../../languages"

// 翻译设置：合并回复语气与术语表两个页内标签，共用同一份工作区翻译设置。
export const Route = createFileRoute("/$slug/settings/_workspace/translation")({
  staticData: { title: "nav.translation", icon: "language", order: 3, permission: "tenant-settings:manage" },
  loader: ({ context }) => preloadQueries(context.app.queryClient, [kit.queries.translation.settings()]),
  component: TranslationSettingsPage,
})

/** tones 是全部语气选项，按从亲切到正式排列。 */
const tones: TranslationTone[] = ["friendly", "neutral", "formal"]

function TranslationSettingsPage(): ReactNode {
  const t = useT()
  const settings = useQuery(kit.queries.translation.settings())
  const glossaryCount = settings.data?.glossary?.length ?? 0

  return (
    <Page
      title={t("nav.translation")}
      description={t("translation_settings.description")}
      tabs={[
        { value: "tone", label: t("tone.title"), content: <ToneTab /> },
        { value: "glossary", label: t("glossary.title"), count: glossaryCount || undefined, content: <GlossaryTab /> },
      ]}
    />
  )
}

/** ToneTab 配置成员回复译成客户语言时的默认语气。 */
function ToneTab(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const settings = useQuery(kit.queries.translation.settings())
  const form = useForm<{ tone: TranslationTone }>({ initialValues: { tone: "neutral" } })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const saved = settings.data?.tone
  // biome-ignore lint/correctness/useExhaustiveDependencies: 只在读到的值变化时重置表单
  useEffect(() => {
    if (!saved) return
    form.setInitialValues({ tone: saved })
    form.setValues({ tone: saved })
  }, [saved])

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      const next = await tenantApi.translation.toneSave(values)
      queryClient.setQueryData(kit.queries.translation.settings().queryKey, next)
      form.setInitialValues({ tone: next.tone })
      form.resetDirty({ tone: next.tone })
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  useUnsavedGuard(form.isDirty())
  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("tone.description")}
        </Text>
        <FormError error={error} />
        <Radio.Group {...form.getInputProps("tone")} aria-label={t("tone.title")}>
          <Stack gap="sm">
            {tones.map((tone) => (
              <Radio.Card key={tone} value={tone} radius="md" p="md">
                <Group wrap="nowrap" align="flex-start">
                  <Radio.Indicator />
                  <div>
                    <Text fw={600}>{kit.label("TranslationTone", tone)}</Text>
                    <Text size="sm" c="dimmed">
                      {t(`tone.${tone}_hint` as const)}
                    </Text>
                    <Text size="sm" mt={4}>
                      {t(`tone.${tone}_example` as const)}
                    </Text>
                  </div>
                </Group>
              </Radio.Card>
            ))}
          </Stack>
        </Radio.Group>
        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty() || !settings.data} loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}

/** Editing 是正在编辑的术语：index 为其在表中的位置，新增时为空。 */
interface Editing {
  index?: number
  term: TranslationTerm
}

/** GlossaryTab 管理产品名和品牌名的固定译法或保留原样规则。 */
function GlossaryTab(): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const queryClient = useQueryClient()
  const settings = useQuery(kit.queries.translation.settings())
  const [editing, setEditing] = useState<Editing>()
  const glossary = settings.data?.glossary ?? []

  const save = async (next: TranslationTerm[]): Promise<TranslationSettingsDTO> => {
    const saved = await tenantApi.translation.glossarySave({ glossary: next })
    queryClient.setQueryData(kit.queries.translation.settings().queryKey, saved)
    notifySuccess(t("common.saved"))
    return saved
  }
  const remove = async (index: number) => {
    const term = glossary[index]
    if (!(await confirm({ title: t("glossary.delete_confirm", { term: term.term }), danger: true, confirmLabel: t("common.delete") }))) return
    try {
      await save(glossary.filter((_, i) => i !== index))
    } catch (err) {
      notifyError(err)
    }
  }

  return (
    <Stack gap="sm">
      <PageActions>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setEditing({ term: { term: "", keep: false, translations: {} } })} disabled={!settings.data}>
          {t("glossary.add")}
        </Button>
      </PageActions>
      <Text size="sm" c="dimmed">
        {t("glossary.description")}
      </Text>

      <DataTable
        records={glossary.map((term, index) => ({ ...term, index }))}
        rowKey={(row) => row.term}
        loading={settings.isPending}
        empty={t("glossary.empty")}
        columns={[
          { accessor: "term", title: t("glossary.term"), width: "25%" },
          {
            accessor: "keep",
            title: t("glossary.handling"),
            width: 120,
            render: (row) => (
              <Badge variant="light" color={row.keep ? "gray" : "bright-blue"}>
                {t(row.keep ? "glossary.keep" : "glossary.fixed")}
              </Badge>
            ),
          },
          {
            accessor: "translations",
            title: t("glossary.translations"),
            render: (row) =>
              row.keep ? (
                <Text c="dimmed" size="sm">
                  —
                </Text>
              ) : (
                <Group gap={6}>
                  {Object.entries(row.translations ?? {}).map(([lang, text]) => (
                    <Badge key={lang} variant="default" tt="none" fw={400} radius="sm">
                      <Text span size="xs" c="dimmed" mr={6}>
                        {languageName(lang, ui)}
                      </Text>
                      {text}
                    </Badge>
                  ))}
                </Group>
              ),
          },
          {
            accessor: "actions",
            title: "",
            textAlign: "right",
            render: (row) => (
              <RowActions
                onEdit={() => setEditing({ index: row.index, term: row })}
                actions={[{ label: t("common.delete"), icon: <IconTrash size={14} />, danger: true, onClick: () => void remove(row.index) }]}
              />
            ),
          },
        ]}
      />

      <Modal
        opened={!!editing}
        onClose={() => setEditing(undefined)}
        closeOnClickOutside={false}
        title={t(editing?.index === undefined ? "glossary.add" : "glossary.edit")}
        size="lg"
      >
        {editing && (
          <TermForm
            initial={editing.term}
            onCancel={() => setEditing(undefined)}
            onSave={async (term) => {
              const next = editing.index === undefined ? [...glossary, term] : glossary.map((x, i) => (i === editing.index ? term : x))
              await save(next)
              setEditing(undefined)
            }}
          />
        )}
      </Modal>
    </Stack>
  )
}

/** Pair 是术语表单中的一条语言译法。 */
interface Pair {
  lang: string
  text: string
}

/** TermForm 是新增或编辑术语的表单，支持原样保留与按语言指定固定译法。 */
function TermForm({ initial, onSave, onCancel }: { initial: TranslationTerm; onSave: (term: TranslationTerm) => Promise<void>; onCancel: () => void }): ReactNode {
  const t = useT()
  const pairs = Object.entries(initial.translations ?? {}).map(([lang, text]) => ({ lang, text }))
  const form = useForm<{ term: string; keep: boolean; pairs: Pair[] }>({
    initialValues: { term: initial.term, keep: initial.keep, pairs: pairs.length > 0 ? pairs : [{ lang: "", text: "" }] },
    validate: {
      term: (v) => (v.trim() ? null : t("glossary.term_required")),
      pairs: {
        lang: (v, values, path) => {
          const i = Number(path.split(".")[1])
          return values.keep || v || !values.pairs[i]?.text.trim() ? null : t("glossary.language_required")
        },
      },
    },
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    const translations: Record<string, string> = {}
    if (!values.keep) for (const p of values.pairs) if (p.lang && p.text.trim()) translations[p.lang] = p.text.trim()
    try {
      await onSave({ term: values.term.trim(), keep: values.keep, translations })
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="md">
        <FormError error={error} />
        <TextInput label={t("glossary.term")} description={t("glossary.term_hint")} required maxLength={100} data-autofocus {...form.getInputProps("term")} />
        <Stack gap={4}>
          <Text size="sm" fw={500}>
            {t("glossary.handling")}
          </Text>
          <SegmentedControl
            w="fit-content"
            value={form.values.keep ? "keep" : "fixed"}
            onChange={(v) => form.setFieldValue("keep", v === "keep")}
            data={[
              { value: "keep", label: t("glossary.keep") },
              { value: "fixed", label: t("glossary.fixed") },
            ]}
          />
          <Text size="xs" c="dimmed">
            {t(form.values.keep ? "glossary.keep_hint" : "glossary.fixed_hint")}
          </Text>
        </Stack>
        {!form.values.keep && (
          <Stack gap={6}>
            <Text size="sm" fw={500}>
              {t("glossary.translations")}
            </Text>
            {form.values.pairs.map((_p, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: 表单中的行按位置维护
              <Group key={i} gap="xs" wrap="nowrap" align="flex-start">
                <LanguageSelect w={220} placeholder={t("glossary.language")} aria-label={t("glossary.language")} {...form.getInputProps(`pairs.${i}.lang`)} />
                <TextInput style={{ flex: 1 }} maxLength={200} placeholder={t("glossary.translation")} aria-label={t("glossary.translation")} {...form.getInputProps(`pairs.${i}.text`)} />
                <ActionIcon variant="subtle" color="gray" mt={4} onClick={() => form.removeListItem("pairs", i)} aria-label={t("glossary.remove_translation")}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ))}
            <Button variant="subtle" size="xs" w="fit-content" leftSection={<IconPlus size={14} />} onClick={() => form.insertListItem("pairs", { lang: "", text: "" })}>
              {t("glossary.add_translation")}
            </Button>
          </Stack>
        )}
        <Group justify="flex-end" gap="xs">
          <Button variant="default" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" loading={saving}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
