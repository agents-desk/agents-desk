import type { WidgetDTO } from "@agents-desk/client/tenant"
import { Badge, Box, Button, ColorInput, Group, Paper, Skeleton, Stack, Switch, TagsInput, Text, Textarea, TextInput, Title } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { applyServerErrors, FormError, notifySuccess, Page, primaryColors, ResourceSelect, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { tenantApi } from "../../api"
import { useUnsavedGuard } from "../../components/unsaved-guard"
import { teams } from "../../resources"
import { WidgetInstallPanel } from "./widget-install-panel"
import { WidgetPreviewStage } from "./widget-preview-stage"
import classes from "./widget-studio.module.css"

/** presetSwatches 是 26 种主题预设色的十六进制色块，供颜色选择器快捷点选。 */
const presetSwatches = Object.values(primaryColors)

/** defaultColorHex 是聊天插件默认主题色的十六进制值。 */
const defaultColorHex = primaryColors["bright-blue"]

/** StudioFormValues 是聊天插件配置工作台的表单草稿结构。 */
interface StudioFormValues {
  name: string
  team_id: string | null
  enabled: boolean
  appearance: {
    color: string
    greeting: string
  }
  allowed_origins: string[]
}

/** toFormValues 将服务端返回的 WidgetDTO 转换为工作台表单初始值。 */
function toFormValues(w: WidgetDTO): StudioFormValues {
  return {
    name: w.name,
    team_id: w.team_id ?? null,
    enabled: w.enabled,
    appearance: {
      color: w.appearance.color || defaultColorHex,
      greeting: w.appearance.greeting ?? "",
    },
    allowed_origins: w.allowed_origins ?? [],
  }
}

/** SectionCard 是工作台左栏的分组配置卡片，便于后续按能力分组扩展新配置项。 */
function SectionCard({ title, description, children }: { title: string; description: string; children: ReactNode }): ReactNode {
  return (
    <Paper withBorder p="md" radius="md">
      <Stack gap="sm">
        <Box>
          <Title order={5}>{title}</Title>
          <Text size="xs" c="dimmed" mt={2}>
            {description}
          </Text>
        </Box>
        {children}
      </Stack>
    </Paper>
  )
}

/**
 * WidgetStudio 是网页聊天插件的统一配置工作台：
 *   - 配置与预览：左栏分组表单 + 右栏同源实时可交互预览舞台，支持未保存检测与一键发布；
 *   - 安装与接入：网页嵌入代码、App WebView 桥接示例与身份验证密钥管理。
 */
export function WidgetStudio({ slug, id }: { slug: string; id: string }): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  // 当前标签记在地址的 ?tab= 中（Page 的 tabs），只有“配置”标签显示保存按钮
  const tab = ((useSearch({ strict: false }) as { tab?: string }).tab ?? "config")
  const [serverError, setServerError] = useState<ReturnType<typeof applyServerErrors>>()

  const widget = useQuery({
    queryKey: ["widget", id],
    queryFn: () => tenantApi.widget.get(id),
  })

  const form = useForm<StudioFormValues>({
    initialValues: {
      name: "",
      team_id: null,
      enabled: true,
      appearance: { color: defaultColorHex, greeting: "" },
      allowed_origins: [],
    },
  })

  // 首次加载到插件数据时同步为表单基准值。
  useEffect(() => {
    if (!widget.data) return
    form.initialize(toFormValues(widget.data))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅在服务端数据到达或切换插件时重置基准
  }, [widget.data?.id])

  const save = useMutation({
    mutationFn: (values: StudioFormValues) =>
      tenantApi.widget.update(id, {
        name: values.name.trim(),
        team_id: values.team_id || undefined,
        enabled: values.enabled,
        appearance: {
          color: values.appearance.color.trim(),
          greeting: values.appearance.greeting,
        },
        allowed_origins: values.allowed_origins,
      }),
    onSuccess: (updated) => {
      setServerError(undefined)
      queryClient.setQueryData(["widget", id], updated)
      void queryClient.invalidateQueries({ queryKey: ["resource", "widget"] })
      const next = toFormValues(updated)
      form.setValues(next)
      form.resetDirty(next)
      notifySuccess(t("widget_studio.saved"))
    },
    onError: (err) => {
      setServerError(applyServerErrors(form, err))
    },
  })

  const dirty = form.isDirty()
  useUnsavedGuard(dirty)

  const handleDiscard = () => {
    if (!widget.data) return
    setServerError(undefined)
    form.clearErrors()
    const base = toFormValues(widget.data)
    form.setValues(base)
    form.resetDirty(base)
  }

  return (
    <Page
      title={widget.data ? widget.data.name : t("widget_install.title")}
      back={() => void navigate({ to: "/t/$slug/settings/widgets", params: { slug } })}
      actions={
        widget.data ? (
          <Group
            gap="xs"
            style={{
              visibility: tab === "config" ? "visible" : "hidden",
              pointerEvents: tab === "config" ? "auto" : "none",
            }}
          >
            {dirty && (
              <Badge variant="light" color="yellow">
                {t("widget_studio.unsaved")}
              </Badge>
            )}
            <Button variant="default" size="sm" disabled={!dirty || save.isPending} onClick={handleDiscard}>
              {t("widget_studio.discard")}
            </Button>
            <Button size="sm" disabled={!dirty} loading={save.isPending} onClick={() => void form.onSubmit((v) => save.mutate(v))()}>
              {t("widget_studio.save")}
            </Button>
          </Group>
        ) : undefined
      }
      tabs={[
        {
          value: "config",
          label: t("widget_studio.tab_config"),
          content: (
            <div className={classes.tabPanel}>
              {widget.isPending ? (
                <Box className={classes.layout}>
                  <Stack gap="md">
                    <Skeleton height={160} radius="md" />
                    <Skeleton height={220} radius="md" />
                    <Skeleton height={150} radius="md" />
                    <Skeleton height={200} radius="md" />
                  </Stack>
                  <Skeleton height={680} radius="md" />
                </Box>
              ) : (
                <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
                  <Box className={classes.layout}>
                    <Stack gap="md">
                      <FormError error={serverError} />

                      <SectionCard title={t("widget_studio.section_basic")} description={t("widget_studio.section_basic_desc")}>
                        <TextInput
                          label={t("widget_studio.name")}
                          description={t("widget_studio.name_hint")}
                          required
                          maxLength={60}
                          {...form.getInputProps("name")}
                        />
                        <Switch
                          label={t("widget_studio.enabled")}
                          description={t("widget_studio.enabled_hint")}
                          {...form.getInputProps("enabled", { type: "checkbox" })}
                        />
                      </SectionCard>

                      <SectionCard title={t("widget_studio.section_appearance")} description={t("widget_studio.section_appearance_desc")}>
                        <ColorInput
                          label={t("widget_studio.color")}
                          description={t("widget_studio.color_hint")}
                          format="hex"
                          swatches={presetSwatches}
                          swatchesPerRow={7}
                          fixOnBlur={false}
                          {...form.getInputProps("appearance.color")}
                        />
                        <Textarea
                          label={t("widget_studio.greeting")}
                          description={t("widget_studio.greeting_hint")}
                          placeholder={t("widget_studio.greeting_placeholder")}
                          autosize
                          minRows={2}
                          maxRows={5}
                          maxLength={500}
                          {...form.getInputProps("appearance.greeting")}
                        />
                      </SectionCard>

                      <SectionCard title={t("widget_studio.section_routing")} description={t("widget_studio.section_routing_desc")}>
                        <ResourceSelect
                          model={teams}
                          label={t("widgets.team_hint")}
                          placeholder={t("widgets.team_unassigned")}
                          filterRow={(row) => row.status === "active" || String(row.id) === String(form.values.team_id ?? "")}
                          getOption={(row) => ({
                            value: String(row.id),
                            label:
                              row.status === "disabled"
                                ? t("widgets.team_disabled_suffix", { name: String(row.name ?? "") })
                                : String(row.name ?? ""),
                          })}
                          value={form.values.team_id}
                          onChange={(v) => form.setFieldValue("team_id", v)}
                          error={form.errors.team_id}
                          clearable
                        />
                        <TagsInput
                          label={t("widget_studio.allowed_origins")}
                          description={t("widget_studio.allowed_origins_hint")}
                          placeholder={t("widget_studio.allowed_origins_placeholder")}
                          clearable
                          {...form.getInputProps("allowed_origins")}
                        />
                      </SectionCard>
                    </Stack>

                    <WidgetPreviewStage
                      name={form.values.name}
                      appearance={form.values.appearance}
                    />
                  </Box>
                </form>
              )}
            </div>
          ),
        },
        {
          value: "install",
          label: t("widget_studio.tab_install"),
          content: (
            <div className={classes.tabPanel}>
              <WidgetInstallPanel id={id} />
            </div>
          ),
        },
      ]}
    />
  )
}
