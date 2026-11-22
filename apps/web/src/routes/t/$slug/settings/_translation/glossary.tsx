import type { TranslationSettingsDTO, TranslationTerm } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, Button, Group, Modal, SegmentedControl, Stack, Table, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { IconPencil, IconPlus, IconTrash } from "@tabler/icons-react"
import { applyServerErrors, confirm, FormError, notifyError, notifySuccess, Page, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { LanguageSelect } from "../../../../../LanguageSelect"
import { languageName } from "../../../../../languages"

// 术语表：产品名、品牌名按固定的写法翻译。每条是保持原样（任何语言中都不翻译），或者按语言给出固定译法。
// 只在消息中出现这个词时生效；订单号、金额、链接总是原样保留，不用加。添加、编辑、删除后保存整张表。
export const Route = createFileRoute("/t/$slug/settings/_translation/glossary")({
  staticData: { title: "glossary.title", icon: "book", order: 1, permission: "tenant-settings:manage" },
  component: Glossary,
})

/** Editing 是正在编辑的术语：index 是它在表中的位置，新加的为空。 */
interface Editing {
  index?: number
  term: TranslationTerm
}

function Glossary(): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const queryClient = useQueryClient()
  const settings = useQuery(kit.queries.translation.settings())
  const [editing, setEditing] = useState<Editing>()
  const glossary = settings.data?.glossary ?? []

  // save 保存整张表，成功后更新缓存
  const save = async (next: TranslationTerm[]): Promise<TranslationSettingsDTO> => {
    const saved = await tenantApi.translation.glossarySave({ glossary: next })
    queryClient.setQueryData(kit.queries.translation.settings().queryKey, saved)
    notifySuccess(t("common.saved"))
    return saved
  }
  const remove = async (index: number) => {
    const term = glossary[index]
    if (!(await confirm({ title: t("glossary.delete_confirm", { term: term.term }), danger: true, confirmLabel: t("glossary.delete") }))) return
    try {
      await save(glossary.filter((_, i) => i !== index))
    } catch (err) {
      notifyError(err)
    }
  }

  return (
    <Page
      title={t("glossary.title")}
      description={t("glossary.description")}
      actions={
        <Button leftSection={<IconPlus size={16} />} onClick={() => setEditing({ term: { term: "", keep: false, translations: {} } })} disabled={!settings.data}>
          {t("glossary.add")}
        </Button>
      }
    >
      {glossary.length === 0 ? (
        <Text c="dimmed" ta="center" py="xl">
          {t("glossary.empty")}
        </Text>
      ) : (
        <Table verticalSpacing="sm">
          <Table.Thead>
            <Table.Tr>
              <Table.Th w="25%">{t("glossary.term")}</Table.Th>
              <Table.Th w={120}>{t("glossary.handling")}</Table.Th>
              <Table.Th>{t("glossary.translations")}</Table.Th>
              <Table.Th w={80} />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {glossary.map((term, i) => (
              <Table.Tr key={term.term}>
                <Table.Td>{term.term}</Table.Td>
                <Table.Td>
                  <Badge variant="light" color={term.keep ? "gray" : "bright-blue"}>
                    {t(term.keep ? "glossary.keep" : "glossary.fixed")}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  {term.keep ? (
                    <Text c="dimmed">—</Text>
                  ) : (
                    <Group gap={6}>
                      {Object.entries(term.translations ?? {}).map(([lang, text]) => (
                        <Badge key={lang} variant="default" tt="none" fw={400} radius="sm">
                          <Text span size="xs" c="dimmed" mr={6}>
                            {languageName(lang, ui)}
                          </Text>
                          {text}
                        </Badge>
                      ))}
                    </Group>
                  )}
                </Table.Td>
                <Table.Td>
                  <Group gap={4} justify="flex-end" wrap="nowrap">
                    <ActionIcon variant="subtle" color="gray" onClick={() => setEditing({ index: i, term })} aria-label={t("glossary.edit")}>
                      <IconPencil size={16} />
                    </ActionIcon>
                    <ActionIcon variant="subtle" color="red" onClick={() => void remove(i)} aria-label={t("glossary.delete")}>
                      <IconTrash size={16} />
                    </ActionIcon>
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
      <Modal opened={!!editing} onClose={() => setEditing(undefined)} title={t(editing?.index === undefined ? "glossary.add" : "glossary.edit")} size="lg">
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
    </Page>
  )
}

/** Pair 是表单中的一条译法：语言和译文。 */
interface Pair {
  lang: string
  text: string
}

/** TermForm 是添加和编辑术语的表单：术语、处理方式，固定译法时按语言填写译法。保存失败时错误显示在表单中。 */
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
            {form.values.pairs.map((p, i) => {
              return (
                // biome-ignore lint/suspicious/noArrayIndexKey: 表单中的行没有别的标识
                <Group key={i} gap="xs" wrap="nowrap" align="flex-start">
                  <LanguageSelect w={220} placeholder={t("glossary.language")} aria-label={t("glossary.language")} {...form.getInputProps(`pairs.${i}.lang`)} />
                  <TextInput style={{ flex: 1 }} maxLength={200} placeholder={t("glossary.translation")} aria-label={t("glossary.translation")} {...form.getInputProps(`pairs.${i}.text`)} />
                  <ActionIcon variant="subtle" color="gray" mt={6} onClick={() => form.removeListItem("pairs", i)} aria-label={t("glossary.remove_translation")}>
                    <IconTrash size={16} />
                  </ActionIcon>
                </Group>
              )
            })}
            <Button variant="subtle" size="xs" w="fit-content" leftSection={<IconPlus size={14} />} onClick={() => form.insertListItem("pairs", { lang: "", text: "" })}>
              {t("glossary.add_translation")}
            </Button>
          </Stack>
        )}
        <Group justify="flex-end">
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
