import { Alert, Button, Group, PasswordInput, SimpleGrid, Stack, Text, TextInput, Title } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, Page, useSchemaForm } from "@go-tenon/ui"
import type { LlmSettings } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api } from "../../../auth"
import { kit } from "../../../resources"

export const Route = createFileRoute("/_app/settings/model")({
  staticData: { title: "模型接口", order: 1, permission: "platform-settings:manage" },
  component: ModelSettings,
})

// Values 是表单的值，与 LlmSettings 相同；写成映射类型，满足 useSchemaForm 对 Record<string, unknown> 的要求。
type Values = { [K in keyof LlmSettings]: LlmSettings[K] }

const empty = { base_url: "", api_key: "", model: "" }

// purposes 是各用途：设置中的字段名、显示的名称和说明。新增用途时在这里加一行。
const purposes = [
  { key: "translation", title: "翻译", description: "把客户和坐席的消息译成对方的语言" },
  { key: "language_detection", title: "语言识别", description: "识别每条消息的语言和书写方式" },
] as const

// ModelSettings 是各用途的模型接口：每种用途一个 OpenAI 兼容的接口（地址、密钥）和模型名，不同用途可以用不同的接口。
function ModelSettings() {
  const queryClient = useQueryClient()
  const current = useQuery({ queryKey: ["llm", "settings"], queryFn: () => api.llm.settingsGet() })
  const form = useSchemaForm({
    schema: kit.schemas.llm.settingsSave.body,
    rules: kit,
    initialValues: { translation: empty, language_detection: empty } as Values,
  })
  const [error, setError] = useState<ErrorInfo>()
  const [saved, setSaved] = useState(false)

  // 读到当前的设置后作为表单的初始值
  const load = (s: Values) => {
    form.setInitialValues(s)
    form.reset()
  }
  useEffect(() => {
    if (current.data) load(current.data)
  }, [current.data])

  return (
    <Page title="模型接口" description="每种用途配置一个 OpenAI 兼容的接口和模型名，不同用途可以使用不同的接口。地址为空表示不使用这种用途。">
      <form
        onSubmit={form.onSubmit(async (values) => {
          setSaved(false)
          setError(undefined)
          try {
            const s = await api.llm.settingsSave(values)
            queryClient.setQueryData(["llm", "settings"], s)
            load(s)
            setSaved(true)
          } catch (err) {
            setError(applyServerErrors(form, err))
          }
        })}
      >
        <Stack>
          <FormError error={error} />
          {saved && (
            <Alert color="green" withCloseButton onClose={() => setSaved(false)}>
              已保存
            </Alert>
          )}
          {purposes.map((p) => (
            <Stack key={p.key} gap="xs">
              <div>
                <Title order={5}>{p.title}</Title>
                <Text size="sm" c="dimmed">
                  {p.description}
                </Text>
              </div>
              <SimpleGrid cols={{ base: 1, md: 3 }}>
                <TextInput label="接口地址" placeholder="https://gateway.example.com/v1" {...form.getInputProps(`${p.key}.base_url`)} />
                <PasswordInput label="密钥" {...form.getInputProps(`${p.key}.api_key`)} />
                <TextInput label="模型名" {...form.getInputProps(`${p.key}.model`)} />
              </SimpleGrid>
            </Stack>
          ))}
          <Group>
            <Button type="submit" disabled={!form.isDirty()}>
              保存
            </Button>
          </Group>
        </Stack>
      </form>
    </Page>
  )
}
