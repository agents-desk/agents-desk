import { Alert, Button, Group, Stack, Tabs, Text, TextInput } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, Page, SecretInput, useSchemaForm } from "@go-tenon/ui"
import type { LlmEndpoint, LlmPurpose, LlmSettings } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api, kit } from "../../../api"

export const Route = createFileRoute("/_app/settings/model")({
  staticData: { title: "模型接口", order: 1, permission: "platform-settings:manage" },
  component: ModelSettings,
})

// purposes 是各用途：接口中的取值、显示的名称和说明。新增用途时在这里加一行。
const purposes: { key: LlmPurpose; title: string; description: string }[] = [
  { key: "translation", title: "翻译", description: "在不同语言之间互译文本，同时识别原文的语言和书写方式" },
]

const queryKey = ["llm", "settings"]

// ModelSettings 是各用途的模型接口：每种用途一个标签，标签中是这种用途的接口地址、密钥和模型名，各自保存。
function ModelSettings() {
  const current = useQuery({ queryKey, queryFn: () => api.llm.settingsGet() })
  return (
    <Page title="模型接口" description="为每种用途配置模型接口和模型名，不同用途可以使用不同的接口和供应商。接口地址留空表示不启用这种用途。">
      <Tabs defaultValue={purposes[0].key} keepMounted>
        <Tabs.List>
          {purposes.map((p) => (
            <Tabs.Tab key={p.key} value={p.key}>
              {p.title}
            </Tabs.Tab>
          ))}
        </Tabs.List>
        {purposes.map((p) => (
          <Tabs.Panel key={p.key} value={p.key} pt="md">
            <EndpointForm purpose={p.key} description={p.description} saved={current.data?.[p.key]} />
          </Tabs.Panel>
        ))}
      </Tabs>
    </Page>
  )
}

// EndpointForm 是一种用途的表单：saved 是已保存的值，读到后作为初始值；保存时只提交这种用途。
function EndpointForm({ purpose, description, saved }: { purpose: LlmPurpose; description: string; saved?: LlmEndpoint }) {
  const queryClient = useQueryClient()
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
          const e = await api.llm.endpointSave(purpose, values)
          queryClient.setQueryData<LlmSettings>(queryKey, (s) => (s ? { ...s, [purpose]: e } : s))
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
            已保存
          </Alert>
        )}
        <TextInput label="接口地址" placeholder="https://api.example.com/v1" {...form.getInputProps("base_url")} />
        <SecretInput label="密钥" {...form.getInputProps("api_key")} />
        <TextInput label="模型名" {...form.getInputProps("model")} />
        <Group justify="flex-end">
          <Button type="submit" disabled={!form.isDirty()} loading={form.submitting}>
            保存
          </Button>
        </Group>
      </Stack>
    </form>
  )
}
