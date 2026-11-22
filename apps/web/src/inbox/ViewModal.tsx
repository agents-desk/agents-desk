import type { ConversationSort, ConversationViewConfig, ConversationViewDTO, ConversationViewSummary, SavedviewScope } from "@agents-desk/client/tenant"
import { Button, Group, Modal, SegmentedControl, Stack, Text, TextInput } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { notifyError, notifySuccess, useCan, useT } from "@go-tenon/ui"
import { type FormEvent, type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { FilterFields } from "./FilterFields"
import { type ConversationFilter, normalizeFilter, normalizeViewConfig } from "./search"

/** ViewModalProps 是新建或编辑收件箱视图弹窗的属性。 */
export interface ViewModalProps {
  opened: boolean
  onClose: () => void
  /** 传入已有视图时为编辑模式；不传为新建（或另存为）模式。 */
  editing?: ConversationViewSummary | ConversationViewDTO
  /** 新建或另存为模式下的初始筛选与排序配置。 */
  initialConfig?: ConversationViewConfig
  /** 创建或修改成功后的回调，便于跳转到新建的视图并清除临时筛选。 */
  onSaved?: (view: ConversationViewDTO) => void
}

/** ViewModal 用于新建、另存为或编辑收件箱视图：复用 FilterFields 保持筛选与视图配置完全同构。 */
export function ViewModal({ opened, onClose, editing, initialConfig, onSaved }: ViewModalProps): ReactNode {
  const t = useT()
  const can = useCan()
  const canManageShared = can("views:manage")
  const queryClient = useQueryClient()

  const [name, setName] = useState("")
  const [scope, setScope] = useState<SavedviewScope>("personal")
  const [sort, setSort] = useState<ConversationSort>("waiting")
  const [filter, setFilter] = useState<ConversationFilter>({ statuses: ["open"] })
  const [nameError, setNameError] = useState<string>()

  useEffect(() => {
    if (!opened) return
    setNameError(undefined)
    if (editing) {
      setName(editing.name)
      setScope(editing.scope)
      const norm = normalizeViewConfig(editing.config)
      setSort(norm.sort ?? "waiting")
      const { sort: _s, ...rest } = norm
      setFilter(rest)
    } else {
      setName("")
      setScope(canManageShared ? "shared" : "personal")
      const base = initialConfig ? normalizeViewConfig(initialConfig) : { statuses: ["open" as const], sort: "waiting" as const }
      setSort(base.sort ?? "waiting")
      const { sort: _s, ...rest } = base
      setFilter(rest)
    }
  }, [opened, editing, initialConfig, canManageShared])

  const save = useMutation({
    mutationFn: async () => {
      const config: ConversationViewConfig = {
        ...normalizeFilter(filter),
        sort,
      }
      if (editing) {
        return tenantApi.view.update(editing.id, { name: name.trim(), config })
      }
      return tenantApi.view.create({
        scope: canManageShared ? scope : "personal",
        name: name.trim(),
        config,
      })
    },
    onSuccess: (saved) => {
      notifySuccess(t(editing ? "views.saved" : "views.created"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.view.key() })
      onClose()
      onSaved?.(saved)
    },
    onError: (err) => notifyError(err),
  })

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim()) {
      setNameError(t("views.name_required"))
      return
    }
    setNameError(undefined)
    save.mutate()
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={t(editing ? "views.edit" : "views.create")}
      size="md"
      centered
    >
      <form onSubmit={handleSubmit}>
        <Stack gap="sm">
          <TextInput
            label={t("views.name")}
            placeholder={t("views.name_placeholder")}
            value={name}
            onChange={(e) => {
              setName(e.currentTarget.value)
              if (nameError) setNameError(undefined)
            }}
            error={nameError}
            maxLength={50}
            required
            data-autofocus
          />

          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t("views.scope")}
            </Text>
            <SegmentedControl
              fullWidth
              value={canManageShared || editing ? scope : "personal"}
              disabled={Boolean(editing) || !canManageShared}
              onChange={(v) => setScope(v as SavedviewScope)}
              data={[
                { value: "shared", label: t("views.scope_shared") },
                { value: "personal", label: t("views.scope_personal") },
              ]}
            />
            {editing ? (
              <Text size="xs" c="dimmed">
                {t("views.scope_locked")}
              </Text>
            ) : (
              !canManageShared && (
                <Text size="xs" c="dimmed">
                  {t("views.scope_hint_personal_only")}
                </Text>
              )
            )}
          </Stack>

          <FilterFields value={filter} onChange={setFilter} />

          <Stack gap={4}>
            <Text size="sm" fw={500}>
              {t("views.sort")}
            </Text>
            <SegmentedControl
              fullWidth
              value={sort}
              onChange={(v) => setSort(v as ConversationSort)}
              data={[
                { value: "waiting", label: t("inbox.sort_waiting") },
                { value: "recent", label: t("inbox.sort_recent") },
              ]}
            />
          </Stack>

          <Group justify="flex-end" mt="xs">
            <Button variant="default" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" loading={save.isPending}>
              {t("common.save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
