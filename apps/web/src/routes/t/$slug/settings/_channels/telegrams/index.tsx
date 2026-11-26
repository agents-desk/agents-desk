import { Button, Group, Modal, PasswordInput, SegmentedControl, Stack, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { AnchorLink, applyServerErrors, DataTable, FormError, Page, ResourceSelect, StatusDot, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../../api"
import { RowActions } from "../../../../../../components/row-actions"
import { teams } from "../../../../../../resources"

// Telegram 机器人渠道列表：浏览工作区中的 Telegram 机器人，新建后直接进入渠道配置工作台。
export const Route = createFileRoute("/t/$slug/settings/_channels/telegrams/")({
  component: Telegrams,
})

function Telegrams(): ReactNode {
  const { slug } = Route.useParams()
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<ReturnType<typeof applyServerErrors>>()

  const list = useQuery({
    queryKey: ["resource", "telegram", "list"],
    queryFn: () => tenantApi.telegram.list(),
  })

  const form = useForm<{ name: string; bot_token: string; team_id: string | null; gateway_managed: boolean }>({
    initialValues: { name: "", bot_token: "", team_id: null, gateway_managed: false },
  })

  const create = useMutation({
    mutationFn: (v: { name: string; bot_token: string; team_id: string | null; gateway_managed: boolean }) =>
      tenantApi.telegram.create({
        name: v.name.trim(),
        bot_token: v.bot_token.trim(),
        team_id: v.team_id || undefined,
        gateway_managed: v.gateway_managed,
      }),
    onSuccess: (created) => {
      setCreating(false)
      setCreateError(undefined)
      form.reset()
      void queryClient.invalidateQueries({ queryKey: ["resource", "telegram"] })
      void navigate({ to: "/t/$slug/settings/telegrams/$id", params: { slug, id: created.id } })
    },
    onError: (err) => {
      setCreateError(applyServerErrors(form, err))
    },
  })

  const openCreate = () => {
    setCreateError(undefined)
    form.reset()
    setCreating(true)
  }

  return (
    <Page
      title={t("telegrams.title")}
      actions={
        <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
          {t("telegrams.new")}
        </Button>
      }
    >
      <DataTable
        records={list.data?.items ?? []}
        loading={list.isPending}
        empty={t("telegrams.empty")}
        columns={[
          {
            accessor: "name",
            title: t("telegrams.name"),
            render: (row) => (
              <AnchorLink to="/t/$slug/settings/telegrams/$id" params={{ slug, id: row.id }} size="sm" fw={500}>
                {row.name}
              </AnchorLink>
            ),
          },
          {
            accessor: "bot_username",
            title: t("telegrams.bot_username"),
            render: (row) => (row.bot_username ? `@${row.bot_username}` : <Text c="dimmed" size="sm">—</Text>),
          },
          {
            accessor: "team_name",
            title: t("common.team_inbox"),
            render: (row) => row.team_name || <Text c="dimmed" size="sm">{t("telegrams.team_unassigned")}</Text>,
          },
          {
            accessor: "gateway_managed",
            title: t("telegrams.mode"),
            render: (row) => t(row.gateway_managed ? "telegrams.mode_gateway" : "telegrams.mode_direct"),
          },
          {
            accessor: "enabled",
            title: t("common.status"),
            render: (row) => <StatusDot color={row.enabled ? "green" : "gray"}>{t(row.enabled ? "common.enabled" : "common.disabled")}</StatusDot>,
          },
          {
            accessor: "actions",
            title: "",
            textAlign: "right",
            render: (row) => (
              <RowActions
                editLabel={t("telegrams.configure")}
                onEdit={() => void navigate({ to: "/t/$slug/settings/telegrams/$id", params: { slug, id: row.id } })}
              />
            ),
          },
        ]}
      />

      <Modal opened={creating} onClose={() => setCreating(false)} title={t("telegrams.create_title")}>
        <form onSubmit={form.onSubmit((v) => create.mutate(v))}>
          <Stack gap="md">
            <FormError error={createError} />
            <TextInput label={t("telegrams.name")} required maxLength={60} data-autofocus {...form.getInputProps("name")} />
            <PasswordInput
              label={t("telegrams.bot_token")}
              description={t("telegrams.bot_token_hint")}
              required
              autoComplete="off"
              {...form.getInputProps("bot_token")}
            />
            <Stack gap={6}>
              <Text size="sm" fw={500}>
                {t("telegrams.mode")}
              </Text>
              <SegmentedControl
                fullWidth
                value={form.values.gateway_managed ? "gateway" : "direct"}
                onChange={(v) => form.setFieldValue("gateway_managed", v === "gateway")}
                data={[
                  { value: "direct", label: t("telegrams.mode_direct") },
                  { value: "gateway", label: t("telegrams.mode_gateway") },
                ]}
              />
              <Text size="xs" c="dimmed">
                {t(form.values.gateway_managed ? "telegrams.mode_gateway_desc" : "telegrams.mode_direct_desc")}
              </Text>
            </Stack>
            <ResourceSelect
              model={teams}
              label={t("telegrams.team_hint")}
              placeholder={t("telegrams.team_unassigned")}
              filterRow={(row) => row.status === "active"}
              getOption={(row) => ({ value: String(row.id), label: String(row.name ?? "") })}
              value={form.values.team_id}
              onChange={(v) => form.setFieldValue("team_id", v)}
              error={form.errors.team_id}
              clearable
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={() => setCreating(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={create.isPending}>
                {t("telegrams.create_submit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Page>
  )
}
