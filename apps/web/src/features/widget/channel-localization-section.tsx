import type { WidgetLocalization, WidgetLocalizedCopy } from "@agents-desk/client/tenant"
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Divider,
  Group,
  Menu,
  MultiSelect,
  Stack,
  Switch,
  TagsInput,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from "@mantine/core"
import { IconPlus, IconSparkles, IconTrash } from "@tabler/icons-react"
import { useMutation, useQuery } from "@tanstack/react-query"
import { notifyError, notifySuccess, useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useMemo } from "react"
import { kit, tenantApi } from "../../api"
import { LanguageSelect } from "../../LanguageSelect"
import { languageName, sameLanguage } from "../../languages"
import { WIDGET_BUILTIN_LANGUAGES } from "./preview-protocol"

/** ChannelLocalizationSectionProps 是网页聊天插件与个人商务链接共用的多语言配置区输入。 */
interface ChannelLocalizationSectionProps {
  mode: "widget" | "business_link"
  channelId?: string
  internalName: string
  value: WidgetLocalization
  onChange: (next: WidgetLocalization) => void
  activeLanguage: string
  onActiveLanguageChange: (lang: string) => void
  /** 以下仅 business_link 模式用于展示默认名片占位提示： */
  showOwner?: boolean
  fallbackOwnerTitle?: string
  fallbackOwnerCompany?: string
  fallbackOwnerBio?: string
}

/** emptyCopy 返回一份空白单语种自定义文案。 */
function emptyCopy(): WidgetLocalizedCopy {
  return {
    title: "",
    greeting: "",
    owner_title: "",
    owner_company: "",
    owner_bio: "",
    away_message: "",
    starters: [],
  }
}

/** hasAnyCopy 报告单语种自定义文案是否至少填写了一个非空字段。 */
function hasAnyCopy(copy: WidgetLocalizedCopy | undefined, isBiz: boolean): boolean {
  if (!copy) return false
  if (copy.title?.trim() || copy.greeting?.trim()) return true
  if (!isBiz) return false
  return Boolean(
    copy.owner_title?.trim() ||
      copy.owner_company?.trim() ||
      copy.owner_bio?.trim() ||
      copy.away_message?.trim() ||
      (copy.starters && copy.starters.length > 0),
  )
}

/**
 * ChannelLocalizationSection 是公共网页聊天插件与个人商务沟通链接共用的多语言策略与文案编辑区块：
 *   - 顶部配置渠道主语言、访客语言切换开关与可选语言白名单；
 *   - 中部按语言胶囊切换编辑语种，支持添加/移除语种及一键调用工作区 AI 翻译模型生成多语言草稿；
 *   - 底部渲染当前编辑语种的对外标题、问候语（以及商务链接的名片覆写、离开提示与快捷开场话题），空白项自动提示主语言回退值。
 */
export function ChannelLocalizationSection({
  mode,
  channelId,
  internalName,
  value,
  onChange,
  activeLanguage,
  onActiveLanguageChange,
  showOwner = true,
  fallbackOwnerTitle = "",
  fallbackOwnerCompany = "",
  fallbackOwnerBio = "",
}: ChannelLocalizationSectionProps): ReactNode {
  const t = useT()
  const uiLang = useLanguage()
  const isBiz = mode === "business_link"
  const translationSettings = useQuery({ ...kit.queries.translation.settings(), staleTime: 60_000 })
  const aiEnabled = translationSettings.data?.enabled ?? false

  const defaultLang = value.default_language || "en"
  const translations = value.translations ?? {}

  // 已配置的语言列表：主语言始终排第一位，其余按已存在的键顺序展示
  const configuredLanguages = useMemo(() => {
    const out: string[] = [defaultLang]
    for (const key of Object.keys(translations)) {
      if (!out.some((x) => sameLanguage(x, key))) {
        out.push(key)
      }
    }
    if (activeLanguage && !out.some((x) => sameLanguage(x, activeLanguage))) {
      out.push(activeLanguage)
    }
    return out
  }, [activeLanguage, defaultLang, translations])

  const editingLang = configuredLanguages.find((l) => sameLanguage(l, activeLanguage)) ?? defaultLang
  const isEditingDefault = sameLanguage(editingLang, defaultLang)
  const defaultCopy = translations[defaultLang] ?? emptyCopy()
  const currentCopy = translations[editingLang] ?? emptyCopy()

  const enabledOptions = useMemo(() => {
    const tags = new Set<string>([...WIDGET_BUILTIN_LANGUAGES, ...configuredLanguages, ...(value.enabled_languages ?? [])])
    return [...tags].map((code) => ({
      value: code,
      label: `${languageName(code, uiLang)} (${code})`,
    }))
  }, [configuredLanguages, uiLang, value.enabled_languages])

  const addableBuiltins = useMemo(
    () => WIDGET_BUILTIN_LANGUAGES.filter((code) => !configuredLanguages.some((x) => sameLanguage(x, code))),
    [configuredLanguages],
  )

  const updateCopyField = <K extends keyof WidgetLocalizedCopy>(field: K, fieldVal: WidgetLocalizedCopy[K]) => {
    const prev = translations[editingLang] ?? {}
    onChange({
      ...value,
      translations: {
        ...translations,
        [editingLang]: { ...prev, [field]: fieldVal },
      },
    })
  }

  const handleDefaultLangChange = (nextDefault: string | null) => {
    if (!nextDefault) return
    onChange({
      ...value,
      default_language: nextDefault,
      translations: {
        ...translations,
        ...(translations[nextDefault] ? {} : { [nextDefault]: emptyCopy() }),
      },
    })
    onActiveLanguageChange(nextDefault)
  }

  const handleAddLanguage = (code: string) => {
    if (!code) return
    if (!translations[code]) {
      onChange({
        ...value,
        translations: {
          ...translations,
          [code]: emptyCopy(),
        },
      })
    }
    onActiveLanguageChange(code)
  }

  const handleRemoveLanguage = (code: string) => {
    if (sameLanguage(code, defaultLang)) return
    const nextTranslations = { ...translations }
    delete nextTranslations[code]
    onChange({ ...value, translations: nextTranslations })
    if (sameLanguage(editingLang, code)) {
      onActiveLanguageChange(defaultLang)
    }
  }

  const translateMutation = useMutation({
    mutationFn: (targets: string[]) =>
      tenantApi.widget.translateCopy({
        channel_id: channelId,
        source_language: defaultLang,
        target_languages: targets,
        copy: defaultCopy,
      }),
    onSuccess: (res) => {
      const nextTranslations = { ...translations }
      const keys = Object.keys(res.translations ?? {})
      for (const k of keys) {
        const translated = res.translations[k]
        if (!translated) continue
        nextTranslations[k] = {
          ...(nextTranslations[k] ?? {}),
          ...translated,
        }
      }
      onChange({ ...value, translations: nextTranslations })
      notifySuccess(t("widget_i18n.ai_translated", { count: keys.length }))
    },
    onError: (err) => notifyError(err),
  })

  const runTranslate = (targets: string[]) => {
    if (!hasAnyCopy(defaultCopy, isBiz)) {
      notifyError(new Error(t("widget_i18n.ai_source_empty")))
      return
    }
    const filtered = targets.filter((code) => !sameLanguage(code, defaultLang))
    if (filtered.length === 0) return
    translateMutation.mutate(filtered)
  }

  // 在主语言标签下点击一键翻译时，翻译全部已添加目标语言；若尚未添加其他语言，则一键生成其余内置核心语种。
  const allTargets = useMemo(() => {
    const existing = configuredLanguages.filter((code) => !sameLanguage(code, defaultLang))
    if (existing.length > 0) return existing
    return WIDGET_BUILTIN_LANGUAGES.filter((code) => !sameLanguage(code, defaultLang))
  }, [configuredLanguages, defaultLang])

  const fallbackPlaceholder = (primaryVal?: string, defaultHint = "") => {
    if (!isEditingDefault && primaryVal?.trim()) {
      return t("widget_i18n.fallback_placeholder", { value: primaryVal.trim() })
    }
    return defaultHint
  }

  return (
    <Stack gap="sm">
      <LanguageSelect
        label={t("widget_i18n.default_language")}
        description={t("widget_i18n.default_language_hint")}
        languages={WIDGET_BUILTIN_LANGUAGES}
        includeWorkspace={false}
        value={defaultLang}
        onChange={handleDefaultLangChange}
        allowDeselect={false}
      />

      <Switch
        label={t("widget_i18n.allow_switch")}
        description={t("widget_i18n.allow_switch_hint")}
        checked={value.allow_switch}
        onChange={(e) => onChange({ ...value, allow_switch: e.currentTarget.checked })}
      />

      {value.allow_switch && (
        <MultiSelect
          label={t("widget_i18n.enabled_languages")}
          description={t("widget_i18n.enabled_languages_hint")}
          placeholder={t("widget_i18n.enabled_languages_placeholder")}
          data={enabledOptions}
          value={value.enabled_languages ?? []}
          onChange={(next) => onChange({ ...value, enabled_languages: next })}
          clearable
          searchable
        />
      )}

      <Divider my={4} />

      <Group justify="space-between" align="center" gap="xs" wrap="wrap">
        <Text size="sm" fw={500}>
          {t("widget_i18n.editing_language")}
        </Text>
        <Tooltip label={t("widget_i18n.ai_not_configured")} disabled={aiEnabled}>
          <Button
            size="compact-xs"
            variant="light"
            leftSection={<IconSparkles size={13} />}
            disabled={!aiEnabled || allTargets.length === 0}
            loading={translateMutation.isPending}
            onClick={() => runTranslate(isEditingDefault ? allTargets : [editingLang])}
          >
            {isEditingDefault ? t("widget_i18n.ai_translate_all") : t("widget_i18n.ai_translate_current")}
          </Button>
        </Tooltip>
      </Group>

      <Group gap={6} wrap="wrap">
        {configuredLanguages.map((code) => {
          const active = sameLanguage(code, editingLang)
          const isDefault = sameLanguage(code, defaultLang)
          return (
            <Group key={code} gap={2} wrap="nowrap">
              <Button
                size="compact-xs"
                variant={active ? "filled" : "default"}
                onClick={() => onActiveLanguageChange(code)}
                rightSection={
                  isDefault ? (
                    <Badge size="xs" variant={active ? "white" : "light"} color={active ? "brand" : "gray"}>
                      {t("widget_i18n.default_badge")}
                    </Badge>
                  ) : undefined
                }
              >
                {languageName(code, uiLang)} ({code})
              </Button>
              {!isDefault && active && (
                <Tooltip label={t("widget_i18n.remove_language")}>
                  <ActionIcon
                    size="xs"
                    variant="subtle"
                    color="red"
                    aria-label={t("widget_i18n.remove_language")}
                    onClick={() => handleRemoveLanguage(code)}
                  >
                    <IconTrash size={12} />
                  </ActionIcon>
                </Tooltip>
              )}
            </Group>
          )
        })}

        {addableBuiltins.length > 0 && (
          <Menu withinPortal position="bottom-start">
            <Menu.Target>
              <Button size="compact-xs" variant="subtle" leftSection={<IconPlus size={12} />}>
                {t("widget_i18n.add_language")}
              </Button>
            </Menu.Target>
            <Menu.Dropdown>
              {addableBuiltins.map((code) => (
                <Menu.Item key={code} onClick={() => handleAddLanguage(code)}>
                  {languageName(code, uiLang)} ({code})
                </Menu.Item>
              ))}
            </Menu.Dropdown>
          </Menu>
        )}
      </Group>

      <Box>
        <Stack gap="sm">
          <TextInput
            label={t("widget_i18n.display_title")}
            description={t("widget_i18n.display_title_hint")}
            placeholder={fallbackPlaceholder(defaultCopy.title || internalName, internalName)}
            maxLength={60}
            value={currentCopy.title ?? ""}
            onChange={(e) => updateCopyField("title", e.currentTarget.value)}
          />

          <Textarea
            label={isBiz ? t("business_channels.link_greeting") : t("widget_studio.greeting")}
            description={isBiz ? undefined : t("widget_studio.greeting_hint")}
            placeholder={fallbackPlaceholder(
              defaultCopy.greeting,
              isBiz ? t("business_channels.link_greeting_placeholder") : t("widget_studio.greeting_placeholder"),
            )}
            autosize
            minRows={2}
            maxRows={5}
            maxLength={500}
            value={currentCopy.greeting ?? ""}
            onChange={(e) => updateCopyField("greeting", e.currentTarget.value)}
          />

          {isBiz && showOwner && (
            <>
              <TextInput
                label={t("biz_studio.title_override")}
                placeholder={fallbackPlaceholder(
                  defaultCopy.owner_title,
                  fallbackOwnerTitle
                    ? t("biz_studio.title_override_placeholder", { fallback: fallbackOwnerTitle })
                    : t("biz_studio.title_override_empty"),
                )}
                maxLength={60}
                value={currentCopy.owner_title ?? ""}
                onChange={(e) => updateCopyField("owner_title", e.currentTarget.value)}
              />
              <TextInput
                label={t("biz_studio.company_override")}
                placeholder={fallbackPlaceholder(
                  defaultCopy.owner_company,
                  t("biz_studio.company_override_placeholder", { fallback: fallbackOwnerCompany }),
                )}
                maxLength={80}
                value={currentCopy.owner_company ?? ""}
                onChange={(e) => updateCopyField("owner_company", e.currentTarget.value)}
              />
              <Textarea
                label={t("biz_studio.bio_override")}
                placeholder={fallbackPlaceholder(
                  defaultCopy.owner_bio,
                  fallbackOwnerBio || t("biz_studio.bio_override_placeholder"),
                )}
                autosize
                minRows={2}
                maxRows={4}
                maxLength={240}
                value={currentCopy.owner_bio ?? ""}
                onChange={(e) => updateCopyField("owner_bio", e.currentTarget.value)}
              />
            </>
          )}

          {isBiz && (
            <>
              <Textarea
                label={t("biz_studio.away_message")}
                placeholder={fallbackPlaceholder(defaultCopy.away_message, t("biz_studio.away_message_placeholder"))}
                autosize
                minRows={2}
                maxRows={3}
                maxLength={300}
                value={currentCopy.away_message ?? ""}
                onChange={(e) => updateCopyField("away_message", e.currentTarget.value)}
              />
              <TagsInput
                label={t("biz_studio.starters")}
                description={t("biz_studio.starters_hint")}
                placeholder={t("biz_studio.starters_placeholder")}
                maxTags={5}
                clearable
                value={currentCopy.starters ?? []}
                onChange={(next) => updateCopyField("starters", next)}
              />
            </>
          )}
        </Stack>
      </Box>
    </Stack>
  )
}
