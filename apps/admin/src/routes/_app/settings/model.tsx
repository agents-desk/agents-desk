import { Button, Group, NumberInput, SimpleGrid, Stack, TextInput } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import type { ErrorInfo } from "@go-tenon/client"
import {
  applyServerErrors,
  FormError,
  FormSection,
  notifySuccess,
  Page,
  SecretInput,
  useSchemaForm,
  useT,
} from "@go-tenon/ui"
import type { LlmSettings } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api, kit } from "../../../api"

export const Route = createFileRoute("/_app/settings/model")({
  staticData: { title: "nav.model", icon: "ai", order: 4, permission: "platform-settings:manage" },
  component: ModelSettings,
})

const queryKey = ["llm", "settings"]

const emptySettings: LlmSettings = {
  gateway: { base_url: "", api_key: "" },
  models: {
    chat: "",
    fast_router: "",
    translation: "",
    embedding: "",
    rerank: "",
    transcribe: "",
  },
  default_budget: {
    daily_cost_usd: 0,
    monthly_cost_usd: 0,
    daily_total_tokens: 0,
  },
}

// ModelSettings 是统一模型网关、各业务调用类型逻辑模型名及默认工作区熔断配额的配置表单。
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

  const load = (s: LlmSettings) => {
    form.setInitialValues(s)
    form.reset()
  }

  useEffect(() => {
    if (current.data) load(current.data)
  }, [current.data])

  return (
    <Page title={t("nav.model")} description={t("model.description")} width="narrow">
      <form
        noValidate
        onSubmit={form.onSubmit(async (values) => {
          setError(undefined)
          try {
            const saved = await api.llm.settingsSave(values)
            queryClient.setQueryData<LlmSettings>(queryKey, saved)
            load(saved)
            notifySuccess(t("common.saved"))
          } catch (err) {
            setError(applyServerErrors(form, err))
          }
        })}
      >
        <Stack gap="md">
          <FormError error={error} form={form} />
          <FormSection title={t("model.gateway_title")} description={t("model.gateway_description")}>
            <TextInput
              label={t("model.base_url")}
              placeholder="https://gateway.example.com/v1"
              {...form.getInputProps("gateway.base_url")}
            />
            <SecretInput label={t("model.api_key")} {...form.getInputProps("gateway.api_key")} />
          </FormSection>
          <FormSection title={t("model.models_title")}>
            <TextInput
              label={t("model.chat_label")}
              description={t("model.chat_description")}
              placeholder="chat-pro"
              {...form.getInputProps("models.chat")}
            />
            <TextInput
              label={t("model.fast_router_label")}
              description={t("model.fast_router_description")}
              placeholder="router-fast"
              {...form.getInputProps("models.fast_router")}
            />
            <TextInput
              label={t("model.translation_label")}
              description={t("model.translation_description")}
              placeholder="translate-fast"
              {...form.getInputProps("models.translation")}
            />
            <TextInput
              label={t("model.embedding_label")}
              description={t("model.embedding_description")}
              placeholder="embed-large"
              {...form.getInputProps("models.embedding")}
            />
            <TextInput
              label={t("model.rerank_label")}
              description={t("model.rerank_description")}
              placeholder="rerank-v3"
              {...form.getInputProps("models.rerank")}
            />
            <TextInput
              label={t("model.transcribe_label")}
              description={t("model.transcribe_description")}
              placeholder="whisper-1"
              {...form.getInputProps("models.transcribe")}
            />
          </FormSection>
          <FormSection title={t("model.budget_title")} description={t("model.budget_description")}>
            <SimpleGrid cols={{ base: 1, sm: 3 }}>
              <NumberInput
                label={t("model.budget_daily_cost")}
                min={0}
                max={100000}
                decimalScale={4}
                allowNegative={false}
                value={form.values.default_budget.daily_cost_usd}
                onChange={(v) => form.setFieldValue("default_budget.daily_cost_usd", typeof v === "number" ? v : 0)}
                error={form.errors["default_budget.daily_cost_usd"]}
              />
              <NumberInput
                label={t("model.budget_monthly_cost")}
                min={0}
                max={1000000}
                decimalScale={4}
                allowNegative={false}
                value={form.values.default_budget.monthly_cost_usd}
                onChange={(v) => form.setFieldValue("default_budget.monthly_cost_usd", typeof v === "number" ? v : 0)}
                error={form.errors["default_budget.monthly_cost_usd"]}
              />
              <NumberInput
                label={t("model.budget_daily_tokens")}
                min={0}
                max={10000000000}
                allowDecimal={false}
                allowNegative={false}
                value={form.values.default_budget.daily_total_tokens}
                onChange={(v) => form.setFieldValue("default_budget.daily_total_tokens", typeof v === "number" ? v : 0)}
                error={form.errors["default_budget.daily_total_tokens"]}
              />
            </SimpleGrid>
          </FormSection>
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
