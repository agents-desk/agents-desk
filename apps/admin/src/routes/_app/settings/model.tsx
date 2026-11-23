import { Alert, Button, Card, Group, Stack, Text, TextInput, Title } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, Page, SecretInput, useSchemaForm, useT } from "@go-tenon/ui"
import type { LlmSettings } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api, kit } from "../../../api"

export const Route = createFileRoute("/_app/settings/model")({
  staticData: { title: "nav.model", order: 2, permission: "platform-settings:manage" },
  component: ModelSettings,
})

const queryKey = ["llm", "settings"]

const emptySettings: LlmSettings = {
  gateway: { base_url: "", api_key: "" },
  models: { translation: "" },
}

// ModelSettings 是统一模型网关与各业务调用类型逻辑模型名的配置表单。
function ModelSettings() {
  const queryClient = useQueryClient()
  const current = useQuery({ queryKey, queryFn: () => api.llm.settingsGet() })
  const t = useT()
  const form = useSchemaForm({
    schema: kit.schemas.llm.settingsSave.body,
    rules: kit,
    initialValues: emptySettings,
  })
  const [error, setError] = useState<ErrorInfo>()
  const [done, setDone] = useState(false)

  const load = (s: LlmSettings) => {
    form.setInitialValues(s)
    form.reset()
  }

  useEffect(() => {
    if (current.data) load(current.data)
  }, [current.data])

  return (
    <Page title={t("nav.model")} description={t("model.description")}>
      <form
        onSubmit={form.onSubmit(async (values) => {
          setDone(false)
          setError(undefined)
          try {
            const saved = await api.llm.settingsSave(values)
            queryClient.setQueryData<LlmSettings>(queryKey, saved)
            load(saved)
            setDone(true)
          } catch (err) {
            setError(applyServerErrors(form, err))
          }
        })}
      >
        <Stack gap="md">
          <FormError error={error} />
          {done && (
            <Alert color="green" withCloseButton onClose={() => setDone(false)}>
              {t("common.saved")}
            </Alert>
          )}
          <Card withBorder>
            <Stack gap="sm">
              <Title order={4}>{t("model.gateway_title")}</Title>
              <Text size="sm" c="dimmed">
                {t("model.gateway_description")}
              </Text>
              <TextInput
                label={t("model.base_url")}
                placeholder="https://gateway.example.com/v1"
                {...form.getInputProps("gateway.base_url")}
              />
              <SecretInput label={t("model.api_key")} {...form.getInputProps("gateway.api_key")} />
            </Stack>
          </Card>
          <Card withBorder>
            <Stack gap="sm">
              <Title order={4}>{t("model.models_title")}</Title>
              <TextInput
                label={t("model.translation_label")}
                description={t("model.translation_description")}
                placeholder="translate-fast"
                {...form.getInputProps("models.translation")}
              />
            </Stack>
          </Card>
          <Group justify="flex-end">
            <Button type="submit" disabled={!form.isDirty()} loading={form.submitting}>
              {t("common.save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Page>
  )
}
