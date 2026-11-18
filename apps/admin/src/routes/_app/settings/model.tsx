import { Alert, Button, Checkbox, Group, PasswordInput, Stack, TextInput, Title } from "@mantine/core"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import type { ErrorInfo } from "@go-tenon/client"
import { applyServerErrors, FormError, Page, useSchemaForm } from "@go-tenon/ui"
import type { LlmView } from "@agents-desk/client/admin"
import { useEffect, useState } from "react"
import { api } from "../../../auth"
import { kit } from "../../../resources"

export const Route = createFileRoute("/_app/settings/model")({
  staticData: { title: "模型接口", order: 1, permission: "platform-settings:manage" },
  component: ModelSettings,
})

// ModelSettings 是模型接口的平台设置：OpenAI 兼容接口的地址、密钥和各用途的模型名。
// 密钥只写不读：页面只显示是否已设置和末尾几位，留空表示保持原来的密钥。
function ModelSettings() {
  const queryClient = useQueryClient()
  const current = useQuery({ queryKey: ["llm", "settings"], queryFn: () => api.llm.settingsGet() })
  const form = useSchemaForm({
    schema: kit.schemas.llm.settingsSave.body,
    rules: kit,
    initialValues: { base_url: "", api_key: "", clear_api_key: false, models: { translation: "", language_detection: "" } },
  })
  const [error, setError] = useState<ErrorInfo>()
  const [saved, setSaved] = useState(false)

  // 读到当前的设置后作为表单的初始值，密钥一栏保持为空
  const load = (v: LlmView) => {
    form.setInitialValues({ base_url: v.base_url, api_key: "", clear_api_key: false, models: v.models })
    form.reset()
  }
  useEffect(() => {
    if (current.data) load(current.data)
  }, [current.data])

  const keySet = current.data?.api_key_set ?? false
  return (
    <Page title="模型接口" description="agents-desk 通过一个 OpenAI 兼容的接口调用模型，每种用途使用一个模型名。">
      <form
        onSubmit={form.onSubmit(async (values) => {
          setSaved(false)
          try {
            const v = await api.llm.settingsSave(values)
            queryClient.setQueryData(["llm", "settings"], v)
            load(v)
            setSaved(true)
          } catch (err) {
            setError(applyServerErrors(form, err))
          }
        })}
      >
        <Stack maw={560}>
          <FormError error={error} />
          {saved && (
            <Alert color="green" withCloseButton onClose={() => setSaved(false)}>
              已保存
            </Alert>
          )}
          <TextInput label="接口地址" placeholder="https://gateway.example.com/v1" {...form.getInputProps("base_url")} />
          <PasswordInput
            label="密钥"
            description={keySet ? `已设置（${current.data?.api_key_hint || "已隐藏"}），留空保持不变` : "还没有设置"}
            placeholder={keySet ? "留空保持不变" : ""}
            disabled={form.values.clear_api_key}
            {...form.getInputProps("api_key")}
          />
          {keySet && <Checkbox label="删除已保存的密钥" {...form.getInputProps("clear_api_key", { type: "checkbox" })} />}
          <Title order={5} mt="sm">
            各用途的模型名
          </Title>
          <TextInput label="翻译" {...form.getInputProps("models.translation")} />
          <TextInput label="语言识别" description="识别消息的语言和书写方式" {...form.getInputProps("models.language_detection")} />
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
