import { Alert, Button, Group, Stack, Tabs, Text, TextInput } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, Page, SecretInput, useSchemaForm, type MessageId, useT } from "@go-tenon/ui"
import type { LlmCallType, LlmEndpoint, LlmSettings } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api, kit } from "../../../api"

export const Route = createFileRoute("/_app/settings/model")({
  staticData: { title: "nav.model", order: 2, permission: "platform-settings:manage" },
  component: ModelSettings,
})

// callTypes 是各调用类型：接口中的取值和说明的消息 ID，名称是枚举的显示文字。新增调用类型时在这里加一行。
const callTypes: { key: LlmCallType; description: MessageId }[] = [
  { key: "translation", description: "model.translation_description" },
]

const queryKey = ["llm", "settings"]

// ModelSettings 是各调用类型的模型接口：每种调用类型一个标签，标签中是这种调用类型的接口地址、密钥和模型名，各自保存。
function ModelSettings() {
  const current = useQuery({ queryKey, queryFn: () => api.llm.settingsGet() })
  const t = useT()
  return (
    <Page title={t("nav.model")} description={t("model.description")}>
      <Tabs defaultValue={callTypes[0].key} keepMounted>
        <Tabs.List>
          {callTypes.map((p) => (
            <Tabs.Tab key={p.key} value={p.key}>
              {kit.label("LlmCallType", p.key)}
            </Tabs.Tab>
          ))}
        </Tabs.List>
        {callTypes.map((p) => (
          <Tabs.Panel key={p.key} value={p.key} pt="md">
            <EndpointForm callType={p.key} description={t(p.description)} saved={current.data?.[p.key]} />
          </Tabs.Panel>
        ))}
      </Tabs>
    </Page>
  )
}

// EndpointForm 是一种调用类型的表单：saved 是已保存的值，读到后作为初始值；保存时只提交这种调用类型。
function EndpointForm({ callType, description, saved }: { callType: LlmCallType; description: string; saved?: LlmEndpoint }) {
  const queryClient = useQueryClient()
  const t = useT()
  const form = useSchemaForm({
    schema: kit.schemas.llm.endpointSave.body,
    rules: kit,
    initialValues: { base_url: "", api_key: "", model: "" } as LlmEndpoint,
  })
  const [error, setError] = useState<ErrorInfo>()
  const [done, setDone] = useState(false)

  const load = (e: LlmEndpoint) => {
    form.setInitialValues(e)
    form.reset()
  }
  useEffect(() => {
    if (saved) load(saved)
  }, [saved])

  return (
    <form
      onSubmit={form.onSubmit(async (values) => {
        setDone(false)
        setError(undefined)
        try {
          const e = await api.llm.endpointSave(callType, values)
          queryClient.setQueryData<LlmSettings>(queryKey, (s) => (s ? { ...s, [callType]: e } : s))
          load(e)
          setDone(true)
        } catch (err) {
          setError(applyServerErrors(form, err))
        }
      })}
    >
      <Stack>
        <Text size="sm" c="dimmed">
          {description}
        </Text>
        <FormError error={error} />
        {done && (
          <Alert color="green" withCloseButton onClose={() => setDone(false)}>
            {t("common.saved")}
          </Alert>
        )}
        <TextInput label={t("model.base_url")} placeholder="https://api.example.com/v1" {...form.getInputProps("base_url")} />
        <SecretInput label={t("model.api_key")} {...form.getInputProps("api_key")} />
        <TextInput label={t("model.model")} {...form.getInputProps("model")} />
        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty()} loading={form.submitting}>
            {t("common.save")}
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
