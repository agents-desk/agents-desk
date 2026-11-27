import { ActionIcon, Box, Button, Group, Loader, Text, Textarea, Tooltip, UnstyledButton } from "@mantine/core"
import { IconLanguage, IconPencil } from "@tabler/icons-react"
import { useLanguage, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { tenantApi } from "../api"
import { languageName } from "../languages"

/**
 * ContactNotesCard 是联系人长期备注卡片（收件箱详情与联系人档案共用）：
 * 存在跨语言译文时默认展示当前成员语言的译文（支持切换原文与点击进入原文编辑），
 * 缺译文且翻译启用时自动触发后台补译；编辑态支持 Cmd/Ctrl+Enter 保存。
 */
export function ContactNotesCard({
  contactId,
  value: serverValue,
  translatedValue,
  sourceLanguage,
  untranslated,
  translating,
  title,
  placeholder,
  editable,
  disabled,
  saving,
  maxRows = 6,
  onSave,
}: {
  contactId?: string
  value: string
  translatedValue?: string
  sourceLanguage?: string
  untranslated?: boolean
  translating?: boolean
  title: string
  placeholder: string
  editable: boolean
  disabled?: boolean
  saving: boolean
  maxRows?: number
  onSave: (notes: string) => void
}): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const [value, setValue] = useState(serverValue)
  const [editing, setEditing] = useState(false)
  const [showOriginal, setShowOriginal] = useState(false)
  const filledRef = useRef("")

  useEffect(() => {
    setValue(serverValue)
    setEditing(false)
    setShowOriginal(false)
  }, [contactId, serverValue])

  useEffect(() => {
    if (!contactId || !untranslated || !translating || !serverValue.trim()) return
    const key = `${contactId}:${serverValue}`
    if (filledRef.current === key) return
    filledRef.current = key
    void tenantApi.translation.fill({ contact_ids: [contactId] })
  }, [contactId, untranslated, translating, serverValue])

  const dirty = value !== serverValue
  const showReadTranslation = Boolean(translatedValue && !editing)

  return (
    <Box p="sm" className="desk-note">
      <Group justify="space-between" mb={4} wrap="nowrap">
        <Text size="xs" fw={600}>
          {title}
        </Text>
        {showReadTranslation && editable ? (
          <Tooltip label={t("contacts.edit")} openDelay={250}>
            <ActionIcon
              size={20}
              variant="subtle"
              color="gray"
              aria-label={t("contacts.edit")}
              disabled={disabled}
              onClick={() => setEditing(true)}
            >
              <IconPencil size={13} />
            </ActionIcon>
          </Tooltip>
        ) : (
          <Text size="xs" c="dimmed">
            {t("contacts.notes_hint")}
          </Text>
        )}
      </Group>

      {showReadTranslation ? (
        <>
          <Text size="sm" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
            {showOriginal ? serverValue : translatedValue}
          </Text>
          <Group gap={4} mt={6} wrap="wrap">
            <IconLanguage size={12} className="inbox-caption-icon" />
            {sourceLanguage && (
              <>
                <Text size="xs" c="dimmed">
                  {languageName(sourceLanguage, ui)}
                </Text>
                <Text size="xs" c="dimmed">
                  ·
                </Text>
              </>
            )}
            <UnstyledButton className="inbox-caption-btn" onClick={() => setShowOriginal((v) => !v)}>
              {t(showOriginal ? "inbox.view_translation" : "inbox.view_original")}
            </UnstyledButton>
          </Group>
        </>
      ) : (
        <>
          <Textarea
            variant="unstyled"
            size="sm"
            autosize
            autoFocus={editing}
            minRows={2}
            maxRows={maxRows}
            maxLength={4000}
            placeholder={placeholder}
            value={value}
            onChange={(e) => setValue(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape" && editing && !dirty) {
                setEditing(false)
              }
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && dirty && editable && !saving) {
                e.preventDefault()
                onSave(value)
              }
            }}
            disabled={!editable || disabled}
          />
          {untranslated && translating && !dirty && serverValue.trim() && (
            <Group gap={4} mt={4} wrap="nowrap">
              <Loader size={10} />
              <Text size="xs" c="dimmed">
                {t("inbox.translating")}
              </Text>
            </Group>
          )}
          {editable && (dirty || editing) && (
            <Group justify="flex-end" gap="xs" mt="xs" data-tn-density="inspector">
              <Button
                variant="subtle"
                color="gray"
                onClick={() => {
                  setValue(serverValue)
                  setEditing(false)
                }}
              >
                {t("common.cancel")}
              </Button>
              <Button loading={saving} disabled={!dirty} onClick={() => onSave(value)}>
                {t("common.save")}
              </Button>
            </Group>
          )}
        </>
      )}
    </Box>
  )
}
