import type { TranslationTone } from "@agents-desk/client/tenant"
import { Button, Group, Radio, Stack, Text } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import { applyServerErrors, FormError, notifySuccess, Page, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../../../../../api"
import { useUnsavedGuard } from "../../../../../components/unsaved-guard"

// 语气：系统把成员的回复翻译给客户时用的语气。收到的消息照原意翻译，不受语气影响。
export const Route = createFileRoute("/t/$slug/settings/_translation/tone")({
  staticData: { title: "tone.title", icon: "ai", order: 0, permission: "tenant-settings:manage" },
  component: Tone,
})

/** tones 是全部语气，按从随和到正式排列。 */
const tones: TranslationTone[] = ["friendly", "neutral", "formal"]

function Tone(): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const settings = useQuery(kit.queries.translation.settings())
  const form = useForm<{ tone: TranslationTone }>({ initialValues: { tone: "neutral" } })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  // 读到设置后作为表单的初始值
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
    <Page title={t("tone.title")} description={t("tone.description")}>
      <form noValidate onSubmit={submit}>
        <Stack gap="md">
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
    </Page>
  )
}
