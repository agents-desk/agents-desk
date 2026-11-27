import type {
  ChannelBusinessPolicy,
  WidgetBusinessIntakeConfig,
  WidgetBusinessLinkDTO,
  WidgetLocalization,
} from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Avatar,
  Badge,
  Box,
  Button,
  Center,
  ColorInput,
  CopyButton,
  Divider,
  Group,
  MultiSelect,
  Paper,
  PasswordInput,
  SegmentedControl,
  Select,
  Skeleton,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
  Tooltip,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import {
  IconCheck,
  IconCopy,
  IconDeviceDesktop,
  IconDeviceMobile,
  IconDownload,
  IconExternalLink,
  IconForms,
  IconLock,
  IconMessage,
  IconRefresh,
  IconWorld,
} from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useSearch } from "@tanstack/react-router"
import { applyServerErrors, confirm, FormError, notifyError, notifySuccess, Page, primaryColors, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { kit, tenantApi } from "../../api"
import { useUnsavedGuard } from "../../components/unsaved-guard"
import { useMe, useMembers } from "../../inbox/data"
import { languageName } from "../../languages"
import { ChannelLocalizationSection } from "../widget/channel-localization-section"
import {
  computePreviewLanguages,
  PREVIEW_PREFIX,
  type PreviewBusinessPayload,
  type PreviewMessage,
  type PreviewViewport,
  resolvePreviewCopy,
} from "../widget/preview-protocol"
import classes from "../widget/widget-studio.module.css"

const presetSwatches = Object.values(primaryColors)
const defaultColorHex = primaryColors["bright-blue"]

type IntakeMode = "off" | "optional" | "required"
type BizPreviewScene = "intake" | "conversation" | "passcode"

/** BusinessLinkFormValues 是商务沟通链接工作台的表单草稿结构。 */
interface BusinessLinkFormValues {
  name: string
  public_code: string
  enabled: boolean
  appearance: {
    color: string
  }
  localization: WidgetLocalization
  card: {
    show_owner: boolean
  }
  intake: {
    enabled: boolean
    name: IntakeMode
    company: IntakeMode
    email: IntakeMode
    note: IntakeMode
  }
  access: {
    mode: "public" | "passcode"
    passcode: string
    expires_at_local: string
  }
  business: {
    default_collaborator_ids: string[]
    default_tag_ids: string[]
    translation_tone: "" | "friendly" | "neutral" | "formal"
    auto_translate: "inherit" | "on" | "off"
  }
}

/** emptyLocalization 返回默认多语言配置。 */
function emptyLocalization(): WidgetLocalization {
  return {
    default_language: "en",
    translations: {},
  }
}

/** toLocalDatetimeValue 将 RFC3339 时间戳转为 datetime-local 输入框值（YYYY-MM-DDTHH:mm）。 */
function toLocalDatetimeValue(iso?: string): string {
  if (!iso) return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** fromLocalDatetimeValue 将 datetime-local 值转为 RFC3339 字符串；为空时返回 undefined。 */
function fromLocalDatetimeValue(val: string): string | undefined {
  if (!val.trim()) return undefined
  const d = new Date(val)
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

/** toFormValues 将服务端返回的 BusinessLinkDTO 转为工作台表单初始值。 */
function toFormValues(link: WidgetBusinessLinkDTO): BusinessLinkFormValues {
  const autoTranslate =
    link.business.auto_translate === true ? "on" : link.business.auto_translate === false ? "off" : "inherit"
  return {
    name: link.name,
    public_code: link.public_code,
    enabled: link.enabled,
    appearance: {
      color: link.appearance.color || defaultColorHex,
    },
    localization: {
      default_language: link.localization?.default_language || "en",
      translations: link.localization?.translations ?? {},
    },
    card: {
      show_owner: link.card.show_owner,
    },
    intake: {
      enabled: link.intake.enabled,
      name: (link.intake.name as IntakeMode) || "required",
      company: (link.intake.company as IntakeMode) || "optional",
      email: (link.intake.email as IntakeMode) || "optional",
      note: (link.intake.note as IntakeMode) || "off",
    },
    access: {
      mode: link.access.mode === "passcode" ? "passcode" : "public",
      passcode: "",
      expires_at_local: toLocalDatetimeValue(link.access.expires_at),
    },
    business: {
      default_collaborator_ids: link.business.default_collaborator_ids ?? [],
      default_tag_ids: link.business.default_tag_ids ?? [],
      translation_tone: (link.business.translation_tone ?? "") as "" | "friendly" | "neutral" | "formal",
      auto_translate: autoTranslate,
    },
  }
}

/** SectionCard 是配置页左栏的分组卡片：标题、说明和一组输入项。 */
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

/** IntakeFieldRow 渲染单个开场留资字段的 隐藏/选填/必填 切换条。 */
function IntakeFieldRow({
  label,
  value,
  onChange,
}: {
  label: string
  value: IntakeMode
  onChange: (next: IntakeMode) => void
}): ReactNode {
  const t = useT()
  return (
    <Group justify="space-between" align="center" gap="sm" wrap="nowrap">
      <Text size="sm">{label}</Text>
      <SegmentedControl
        size="xs"
        value={value}
        onChange={(v) => onChange(v as IntakeMode)}
        data={[
          { value: "off", label: t("biz_studio.intake_off") },
          { value: "optional", label: t("biz_studio.intake_optional") },
          { value: "required", label: t("biz_studio.intake_required") },
        ]}
      />
    </Group>
  )
}

/**
 * BusinessLinkPreviewStage 是商务沟通链接的右侧同源实时预览舞台：
 * 通过 postMessage 将表单草稿与当前预览语言推送至 /widget/preview，支持切换开场留资、对话中与口令门禁场景、语言及桌面/手机视口。
 */
function BusinessLinkPreviewStage({
  name,
  greeting,
  appearance,
  language,
  previewLanguages,
  onLanguageChange,
  business,
}: {
  name: string
  greeting: string
  appearance: { color: string }
  language: string
  previewLanguages: string[]
  onLanguageChange: (lang: string) => void
  business: PreviewBusinessPayload
}): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const [ready, setReady] = useState(false)
  const [scene, setScene] = useState<BizPreviewScene>("conversation")
  const [viewport, setViewport] = useState<PreviewViewport>("mobile")

  const postPreview = useCallback(
    (nextScene: BizPreviewScene, nextViewport: PreviewViewport) => {
      const target = iframeRef.current?.contentWindow
      if (!target) return
      const payload: PreviewMessage = {
        type: `${PREVIEW_PREFIX}preview`,
        mode: "business_link",
        lang: language,
        name,
        greeting,
        appearance,
        language,
        scene: nextScene,
        viewport: nextViewport,
        business,
      }
      target.postMessage(payload, window.location.origin)
    },
    [appearance, business, greeting, language, name],
  )
  const latestPostRef = useRef<() => void>(() => {})
  latestPostRef.current = () => postPreview(scene, viewport)

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin || e.source !== iframeRef.current?.contentWindow) return
      if (!e.data || typeof e.data.type !== "string" || !e.data.type.startsWith(PREVIEW_PREFIX)) return
      const kind = e.data.type.slice(PREVIEW_PREFIX.length)
      if (kind === "preview_ready") {
        setReady(true)
        latestPostRef.current()
        return
      }
      if (kind === "preview_scene") {
        const next = e.data.scene
        if (next === "intake" || next === "conversation" || next === "passcode") {
          setScene(next)
        }
      }
    }
    window.addEventListener("message", onMessage)
    return () => window.removeEventListener("message", onMessage)
  }, [])

  useEffect(() => {
    if (!ready) return
    postPreview(scene, viewport)
  }, [postPreview, ready, scene, viewport])

  const languageOptions = useMemo(() => {
    const tags = new Set<string>([...previewLanguages, language])
    return [...tags].filter(Boolean).map((code) => ({
      value: code,
      label: `${languageName(code, uiLang)} (${code})`,
    }))
  }, [language, previewLanguages, uiLang])

  return (
    <Stack gap="sm" className={classes.previewColumn}>
      <Group justify="space-between" gap="xs" wrap="wrap">
        <SegmentedControl
          size="xs"
          value={scene}
          onChange={(v) => setScene(v as BizPreviewScene)}
          data={[
            {
              value: "intake",
              label: (
                <Center style={{ gap: 4 }}>
                  <IconForms size={13} />
                  <span>{t("biz_studio.scene_intake")}</span>
                </Center>
              ),
            },
            {
              value: "conversation",
              label: (
                <Center style={{ gap: 4 }}>
                  <IconMessage size={13} />
                  <span>{t("biz_studio.scene_conversation")}</span>
                </Center>
              ),
            },
            {
              value: "passcode",
              label: (
                <Center style={{ gap: 4 }}>
                  <IconLock size={13} />
                  <span>{t("biz_studio.scene_passcode")}</span>
                </Center>
              ),
            },
          ]}
        />
        <Group gap="xs" wrap="nowrap">
          <Select
            size="xs"
            w={150}
            aria-label={t("widget_i18n.preview_language")}
            leftSection={<IconWorld size={13} />}
            value={language}
            onChange={(v) => v && onLanguageChange(v)}
            data={languageOptions}
            allowDeselect={false}
          />
          <SegmentedControl
            size="xs"
            value={viewport}
            onChange={(v) => setViewport(v === "desktop" ? "desktop" : "mobile")}
            data={[
              {
                value: "mobile",
                label: (
                  <Center style={{ gap: 4 }}>
                    <IconDeviceMobile size={13} />
                    <span>{t("widget_studio.viewport_mobile")}</span>
                  </Center>
                ),
              },
              {
                value: "desktop",
                label: (
                  <Center style={{ gap: 4 }}>
                    <IconDeviceDesktop size={13} />
                    <span>{t("widget_studio.viewport_desktop")}</span>
                  </Center>
                ),
              },
            ]}
          />
        </Group>
      </Group>

      <Box className={classes.stage}>
        <Box className={viewport === "mobile" ? classes.mobileShell : classes.desktopShell}>
          <iframe
            ref={iframeRef}
            src="/widget/preview"
            title={t("biz_studio.title")}
            className={classes.frame}
            onLoad={() => {
              setReady(true)
              latestPostRef.current()
            }}
          />
        </Box>
      </Box>

      <Text size="xs" c="dimmed" ta="center">
        {t("biz_studio.preview_hint")}
      </Text>
    </Stack>
  )
}

/** ShareAndQRPanel 展示独立短链（支持附带预设打开语言参数）、商务名片二维码卡片下载与一键重置短码操作。 */
function ShareAndQRPanel({
  link,
  ownerName,
  ownerSubtitle,
  ownerAvatar,
  availableLanguages,
  onRotated,
}: {
  link: WidgetBusinessLinkDTO
  ownerName: string
  ownerSubtitle: string
  ownerAvatar?: string
  availableLanguages: string[]
  onRotated: (updated: WidgetBusinessLinkDTO) => void
}): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const [shareLang, setShareLang] = useState("")
  const rotate = useMutation({
    mutationFn: () => tenantApi.business.linkRotateCode(link.id),
    onSuccess: (updated) => {
      notifySuccess(t("biz_studio.rotated"))
      onRotated(updated)
    },
    onError: (err) => notifyError(err),
  })

  const handleRotate = async () => {
    const ok = await confirm({
      title: t("biz_studio.rotate_confirm"),
      message: t("biz_studio.rotate_message"),
      danger: true,
      confirmLabel: t("biz_studio.rotate_code"),
    })
    if (ok) rotate.mutate()
  }

  const shareURL = shareLang
    ? `${link.chat_url}${link.chat_url.includes("?") ? "&" : "?"}lang=${encodeURIComponent(shareLang)}`
    : link.chat_url

  return (
    <Stack gap="lg">
      <SectionCard title={t("biz_studio.share_link_title")} description={t("biz_studio.share_link_desc")}>
        <Select
          label={t("widget_i18n.share_lang_label")}
          value={shareLang}
          onChange={(v) => setShareLang(v ?? "")}
          data={[
            { value: "", label: t("widget_i18n.share_lang_auto") },
            ...availableLanguages.map((code) => ({
              value: code,
              label: `${languageName(code, uiLang)} (${code})`,
            })),
          ]}
        />
        <Group align="flex-end" gap="xs">
          <TextInput label={t("business_channels.col_url")} value={shareURL} readOnly style={{ flex: 1 }} />
          <CopyButton value={shareURL} timeout={1500}>
            {({ copied, copy }) => (
              <Tooltip label={t(copied ? "common.copied" : "business_channels.copy_url")} withArrow>
                <ActionIcon variant="default" size="input-sm" color={copied ? "green" : undefined} onClick={copy} aria-label={t("business_channels.copy_url")}>
                  {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                </ActionIcon>
              </Tooltip>
            )}
          </CopyButton>
          <Tooltip label={t("business_channels.open_preview")} withArrow>
            <ActionIcon
              component="a"
              href={shareURL}
              target="_blank"
              rel="noopener noreferrer"
              variant="default"
              size="input-sm"
              aria-label={t("business_channels.open_preview")}
            >
              <IconExternalLink size={16} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </SectionCard>

      <SectionCard title={t("biz_studio.qr_title")} description={t("biz_studio.qr_desc")}>
        <Group align="center" gap="lg" wrap="wrap">
          {link.qr_data_url && (
            <Paper withBorder p="sm" radius="md" bg="white" style={{ flex: "none" }}>
              <img
                src={link.qr_data_url}
                alt={link.name}
                width={200}
                height={200}
                style={{ display: "block", imageRendering: "pixelated" }}
              />
            </Paper>
          )}
          <Stack gap="xs" style={{ flex: 1, minWidth: 200 }}>
            <Group gap="sm" wrap="nowrap">
              <Avatar size={40} radius="xl" name={ownerName} color="initials" src={ownerAvatar || undefined} />
              <Box style={{ minWidth: 0 }}>
                <Text size="sm" fw={600} truncate>
                  {ownerName}
                </Text>
                {ownerSubtitle && (
                  <Text size="xs" c="dimmed" truncate>
                    {ownerSubtitle}
                  </Text>
                )}
              </Box>
            </Group>
            <Text size="sm" fw={500}>
              {link.name}
            </Text>
            <Text size="xs" c="dimmed" ff="monospace">
              {link.chat_url}
            </Text>
            {link.qr_data_url && (
              <Group mt="xs">
                <Button
                  component="a"
                  href={link.qr_data_url}
                  download={`business-link-${link.public_code || link.id}.png`}
                  variant="default"
                  size="xs"
                  leftSection={<IconDownload size={14} />}
                >
                  {t("biz_studio.qr_download")}
                </Button>
              </Group>
            )}
          </Stack>
        </Group>
      </SectionCard>

      <SectionCard title={t("biz_studio.security_title")} description={t("biz_studio.security_desc")}>
        <Group justify="flex-end">
          <Button
            variant="light"
            color="red"
            size="sm"
            leftSection={<IconRefresh size={14} />}
            loading={rotate.isPending}
            onClick={() => void handleRotate()}
          >
            {t("biz_studio.rotate_code")}
          </Button>
        </Group>
      </SectionCard>
    </Stack>
  )
}

/**
 * BusinessLinkStudio 是个人网页商务沟通链接的可视化配置工作台：
 *   - 配置与预览：左栏分组配置卡片（基础与短链、外观与名片开关、多语言文案与 AI 翻译、开场留资、访问控制与接待协作）+ 右栏同源多语言安全预览舞台；
 *   - 分享与二维码：独立短链（支持预设打开语言）、商务名片二维码下载与一键重置短码。
 */
export function BusinessLinkStudio({ slug, id }: { slug: string; id: string }): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const tab = (useSearch({ strict: false }) as { tab?: string }).tab ?? "config"
  const [serverError, setServerError] = useState<ReturnType<typeof applyServerErrors>>()
  const [activeLang, setActiveLang] = useState("en")
  const [previewLang, setPreviewLang] = useState("en")

  const linkQuery = useQuery(kit.queries.business.linkGet(id))
  const policyQuery = useQuery(kit.queries.business.policyGet())
  const tenantQuery = useQuery({ queryKey: ["tenant", "current", slug], queryFn: () => tenantApi.tenant.current() })
  const tagsQuery = useQuery(kit.queries.tag.list())
  const me = useMe()
  const { list: members } = useMembers()

  const form = useForm<BusinessLinkFormValues>({
    initialValues: {
      name: "",
      public_code: "",
      enabled: true,
      appearance: { color: defaultColorHex },
      localization: emptyLocalization(),
      card: { show_owner: true },
      intake: { enabled: false, name: "required", company: "optional", email: "optional", note: "off" },
      access: { mode: "public", passcode: "", expires_at_local: "" },
      business: { default_collaborator_ids: [], default_tag_ids: [], translation_tone: "", auto_translate: "inherit" },
    },
  })

  useEffect(() => {
    if (!linkQuery.data) return
    const init = toFormValues(linkQuery.data)
    form.initialize(init)
    setActiveLang(init.localization.default_language)
    setPreviewLang(init.localization.default_language)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅在加载或切换链接时重置表单基准
  }, [linkQuery.data?.id])

  const save = useMutation({
    mutationFn: (values: BusinessLinkFormValues) => {
      const intake: WidgetBusinessIntakeConfig = {
        enabled: values.intake.enabled,
        name: values.intake.name,
        company: values.intake.company,
        email: values.intake.email,
        note: values.intake.note,
      }
      const business: ChannelBusinessPolicy = {
        default_collaborator_ids: values.business.default_collaborator_ids,
        default_tag_ids: values.business.default_tag_ids,
        translation_tone: values.business.translation_tone,
        auto_translate:
          values.business.auto_translate === "on"
            ? true
            : values.business.auto_translate === "off"
              ? false
              : undefined,
      }
      return tenantApi.business.linkUpdate(id, {
        name: values.name.trim(),
        public_code: values.public_code.trim() || undefined,
        enabled: values.enabled,
        appearance: {
          color: values.appearance.color.trim(),
        },
        localization: values.localization,
        card: {
          show_owner: values.card.show_owner,
        },
        intake,
        access: {
          mode: values.access.mode,
          passcode: values.access.passcode.trim() || undefined,
          clear_passcode: values.access.mode === "public",
          expires_at: fromLocalDatetimeValue(values.access.expires_at_local),
        },
        business,
      })
    },
    onSuccess: (updated) => {
      setServerError(undefined)
      queryClient.setQueryData(kit.queries.business.linkGet(id).queryKey, updated)
      void queryClient.invalidateQueries({ queryKey: kit.queries.business.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.chat.key() })
      const next = toFormValues(updated)
      form.setValues(next)
      form.resetDirty(next)
      notifySuccess(t("biz_studio.saved"))
    },
    onError: (err) => setServerError(applyServerErrors(form, err)),
  })

  const dirty = form.isDirty()
  useUnsavedGuard(dirty)

  const handleDiscard = () => {
    if (!linkQuery.data) return
    setServerError(undefined)
    form.clearErrors()
    const base = toFormValues(linkQuery.data)
    form.setValues(base)
    form.resetDirty(base)
    setActiveLang(base.localization.default_language)
    setPreviewLang(base.localization.default_language)
  }

  const fallbackCompany = policyQuery.data?.default_company_name || tenantQuery.data?.name || ""
  const fallbackTitle = me.data?.title || ""
  const fallbackBio = me.data?.bio || ""
  const ownerName = me.data?.name || ""

  const resolvedPreview = useMemo(
    () => resolvePreviewCopy(form.values.localization, previewLang),
    [form.values.localization, previewLang],
  )
  const previewLanguages = useMemo(
    () => computePreviewLanguages(form.values.localization),
    [form.values.localization],
  )

  const effectiveTitle = resolvedPreview.owner_title || fallbackTitle
  const effectiveCompany = resolvedPreview.owner_company || fallbackCompany
  const effectiveBio = resolvedPreview.owner_bio || fallbackBio
  const effectiveAway = resolvedPreview.away_message || ""

  const previewBusiness = useMemo<PreviewBusinessPayload>(
    () => ({
      owner: form.values.card.show_owner
        ? {
            name: ownerName,
            title: effectiveTitle || undefined,
            company: effectiveCompany || undefined,
            bio: effectiveBio || undefined,
            avatar_url: me.data?.avatar_url || undefined,
            status: effectiveAway ? "away" : "online",
          }
        : undefined,
      away_message: effectiveAway || undefined,
      intake: {
        enabled: form.values.intake.enabled,
        name: form.values.intake.name,
        company: form.values.intake.company,
        email: form.values.intake.email,
        note: form.values.intake.note,
      },
      starters: resolvedPreview.starters ?? [],
      requires_passcode: form.values.access.mode === "passcode",
    }),
    [
      effectiveAway,
      effectiveBio,
      effectiveCompany,
      effectiveTitle,
      form.values.access.mode,
      form.values.card.show_owner,
      form.values.intake,
      me.data?.avatar_url,
      ownerName,
      resolvedPreview.starters,
    ],
  )

  const memberOptions = useMemo(
    () => members.filter((m) => m.id !== me.data?.id).map((m) => ({ value: m.id, label: m.name })),
    [me.data?.id, members],
  )
  const tagOptions = useMemo(
    () => (tagsQuery.data?.items ?? []).map((tg) => ({ value: tg.id, label: tg.name })),
    [tagsQuery.data?.items],
  )

  return (
    <Page
      title={linkQuery.data ? linkQuery.data.name : t("biz_studio.title")}
      back={() => void navigate({ to: "/$slug/settings/business-channels", params: { slug } })}
      actions={
        linkQuery.data ? (
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
          label: t("biz_studio.tab_config"),
          content: (
            <div className={classes.tabPanel}>
              {linkQuery.isPending ? (
                <Box className={classes.layout}>
                  <Stack gap="md">
                    <Skeleton height={180} radius="md" />
                    <Skeleton height={240} radius="md" />
                    <Skeleton height={300} radius="md" />
                  </Stack>
                  <Skeleton height={680} radius="md" />
                </Box>
              ) : (
                <form onSubmit={form.onSubmit((v) => save.mutate(v))}>
                  <Box className={classes.layout}>
                    <Stack gap="md">
                      <FormError error={serverError} />

                      <SectionCard title={t("biz_studio.section_basic")} description={t("biz_studio.section_basic_desc")}>
                        <TextInput
                          label={t("business_channels.link_name")}
                          placeholder={t("business_channels.link_name_placeholder")}
                          required
                          maxLength={60}
                          {...form.getInputProps("name")}
                        />
                        <TextInput
                          label={t("biz_studio.public_code")}
                          description={t("biz_studio.public_code_hint")}
                          leftSection={
                            <Text size="xs" c="dimmed" pl={4}>
                              /c/
                            </Text>
                          }
                          leftSectionWidth={32}
                          maxLength={40}
                          {...form.getInputProps("public_code")}
                        />
                        <Switch
                          label={t("biz_studio.enabled")}
                          description={t("biz_studio.enabled_hint")}
                          {...form.getInputProps("enabled", { type: "checkbox" })}
                        />
                      </SectionCard>

                      <SectionCard title={t("biz_studio.section_appearance")} description={t("biz_studio.section_appearance_desc")}>
                        <ColorInput
                          label={t("business_channels.link_color")}
                          format="hex"
                          swatches={presetSwatches}
                          swatchesPerRow={7}
                          fixOnBlur={false}
                          {...form.getInputProps("appearance.color")}
                        />
                        <Switch
                          label={t("biz_studio.show_owner")}
                          {...form.getInputProps("card.show_owner", { type: "checkbox" })}
                        />
                      </SectionCard>

                      <SectionCard title={t("widget_i18n.section_title")} description={t("widget_i18n.section_desc")}>
                        <ChannelLocalizationSection
                          mode="business_link"
                          channelId={id}
                          internalName={form.values.name}
                          value={form.values.localization}
                          onChange={(next) => form.setFieldValue("localization", next)}
                          activeLanguage={activeLang}
                          onActiveLanguageChange={(lang) => {
                            setActiveLang(lang)
                            setPreviewLang(lang)
                          }}
                          showOwner={form.values.card.show_owner}
                          fallbackOwnerTitle={fallbackTitle}
                          fallbackOwnerCompany={fallbackCompany}
                          fallbackOwnerBio={fallbackBio}
                        />
                      </SectionCard>

                      <SectionCard title={t("biz_studio.section_intake")} description={t("biz_studio.section_intake_desc")}>
                        <Switch
                          label={t("biz_studio.intake_enabled")}
                          {...form.getInputProps("intake.enabled", { type: "checkbox" })}
                        />
                        {form.values.intake.enabled && (
                          <Stack gap="xs" pt={4}>
                            <IntakeFieldRow
                              label={t("biz_studio.intake_name")}
                              value={form.values.intake.name}
                              onChange={(v) => form.setFieldValue("intake.name", v)}
                            />
                            <IntakeFieldRow
                              label={t("biz_studio.intake_company")}
                              value={form.values.intake.company}
                              onChange={(v) => form.setFieldValue("intake.company", v)}
                            />
                            <IntakeFieldRow
                              label={t("biz_studio.intake_email")}
                              value={form.values.intake.email}
                              onChange={(v) => form.setFieldValue("intake.email", v)}
                            />
                            <IntakeFieldRow
                              label={t("biz_studio.intake_note")}
                              value={form.values.intake.note}
                              onChange={(v) => form.setFieldValue("intake.note", v)}
                            />
                          </Stack>
                        )}
                      </SectionCard>

                      <SectionCard title={t("biz_studio.section_access")} description={t("biz_studio.section_access_desc")}>
                        <Stack gap={4}>
                          <Text size="sm" fw={500}>
                            {t("biz_studio.access_mode")}
                          </Text>
                          <SegmentedControl
                            value={form.values.access.mode}
                            onChange={(v) => form.setFieldValue("access.mode", v === "passcode" ? "passcode" : "public")}
                            data={[
                              { value: "public", label: t("biz_studio.access_public") },
                              { value: "passcode", label: t("biz_studio.access_passcode") },
                            ]}
                          />
                        </Stack>
                        {form.values.access.mode === "passcode" && (
                          <PasswordInput
                            label={t("biz_studio.passcode")}
                            placeholder={
                              linkQuery.data?.access.has_passcode
                                ? t("biz_studio.passcode_placeholder_keep")
                                : t("biz_studio.passcode_placeholder_set")
                            }
                            maxLength={64}
                            {...form.getInputProps("access.passcode")}
                          />
                        )}
                        <Group align="flex-end" gap="xs">
                          <TextInput
                            type="datetime-local"
                            style={{ flex: 1 }}
                            label={t("biz_studio.expires_at")}
                            description={t("biz_studio.expires_at_hint")}
                            {...form.getInputProps("access.expires_at_local")}
                          />
                          {form.values.access.expires_at_local && (
                            <Button
                              variant="subtle"
                              color="gray"
                              size="sm"
                              onClick={() => form.setFieldValue("access.expires_at_local", "")}
                            >
                              {t("biz_studio.clear_expires")}
                            </Button>
                          )}
                        </Group>
                      </SectionCard>

                      <SectionCard title={t("biz_studio.section_policy")} description={t("biz_studio.section_policy_desc")}>
                        <MultiSelect
                          label={t("biz_studio.collaborators")}
                          description={t("biz_studio.collaborators_hint")}
                          placeholder={t("biz_studio.collaborators_placeholder")}
                          data={memberOptions}
                          searchable
                          clearable
                          {...form.getInputProps("business.default_collaborator_ids")}
                        />
                        <MultiSelect
                          label={t("biz_studio.default_tags")}
                          description={t("biz_studio.default_tags_hint")}
                          placeholder={t("biz_studio.default_tags_placeholder")}
                          data={tagOptions}
                          searchable
                          clearable
                          {...form.getInputProps("business.default_tag_ids")}
                        />
                        <Divider />
                        <Select
                          label={t("biz_studio.translation_tone")}
                          data={[
                            { value: "", label: t("biz_studio.tone_inherit") },
                            { value: "formal", label: kit.label("TranslationTone", "formal") },
                            { value: "neutral", label: kit.label("TranslationTone", "neutral") },
                            { value: "friendly", label: kit.label("TranslationTone", "friendly") },
                          ]}
                          value={form.values.business.translation_tone}
                          onChange={(v) =>
                            form.setFieldValue("business.translation_tone", (v ?? "") as "" | "friendly" | "neutral" | "formal")
                          }
                        />
                        <Select
                          label={t("biz_studio.auto_translate")}
                          data={[
                            { value: "inherit", label: t("biz_studio.auto_translate_inherit") },
                            { value: "on", label: t("biz_studio.auto_translate_on") },
                            { value: "off", label: t("biz_studio.auto_translate_off") },
                          ]}
                          value={form.values.business.auto_translate}
                          onChange={(v) => form.setFieldValue("business.auto_translate", (v ?? "inherit") as "inherit" | "on" | "off")}
                        />
                      </SectionCard>
                    </Stack>

                    <BusinessLinkPreviewStage
                      name={resolvedPreview.title || form.values.name}
                      greeting={resolvedPreview.greeting || ""}
                      appearance={form.values.appearance}
                      language={previewLang}
                      previewLanguages={previewLanguages}
                      onLanguageChange={setPreviewLang}
                      business={previewBusiness}
                    />
                  </Box>
                </form>
              )}
            </div>
          ),
        },
        {
          value: "share",
          label: t("biz_studio.tab_share"),
          content: (
            <div className={classes.tabPanel}>
              {linkQuery.data ? (
                <ShareAndQRPanel
                  link={linkQuery.data}
                  ownerName={ownerName}
                  ownerSubtitle={[effectiveTitle, effectiveCompany].filter(Boolean).join(" · ")}
                  ownerAvatar={me.data?.avatar_url}
                  availableLanguages={previewLanguages}
                  onRotated={(updated) => {
                    queryClient.setQueryData(kit.queries.business.linkGet(id).queryKey, updated)
                    void queryClient.invalidateQueries({ queryKey: kit.queries.business.key() })
                    const next = toFormValues(updated)
                    form.setValues(next)
                    form.resetDirty(next)
                  }}
                />
              ) : (
                <Skeleton height={320} radius="md" />
              )}
            </div>
          ),
        },
      ]}
    />
  )
}
