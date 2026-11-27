import { Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { AnchorLink, applyServerErrors, DataTable, FormError, Page, preloadQueries, ResourceSelect, StatusDot, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../../api"
import { RowActions } from "../../../../../../components/row-actions"
import { teams } from "../../../../../../resources"

// 网页聊天插件列表：浏览工作区中的聊天插件，新建后直接进入实时预览配置工作台。
export const Route = createFileRoute("/t/$slug/settings/_channels/widgets/")({
  // 预取页面的数据，最多等一小会儿：切换到这一页时内容一次完整出现，不先空白再出现
  loader: ({ context }) => preloadQueries(context.app.queryClient, [{ queryKey: ["resource", "widget", "list"], queryFn: () => tenantApi.widget.list() }]),
  component: Widgets,
})

function Widgets(): ReactNode {
  const { slug } = Route.useParams()
  const navigate = useNavigate()
  const t = useT()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<ReturnType<typeof applyServerErrors>>()

  const list = useQuery({
    queryKey: ["resource", "widget", "list"],
    queryFn: () => tenantApi.widget.list(),
  })

  const form = useForm<{ name: string; team_id: string | null }>({
    initialValues: { name: "", team_id: null },
  })

  const create = useMutation({
    mutationFn: (v: { name: string; team_id: string | null }) =>
      tenantApi.widget.create({
        name: v.name.trim(),
        team_id: v.team_id || undefined,
      }),
    onSuccess: (created) => {
      setCreating(false)
      setCreateError(undefined)
      form.reset()
      void queryClient.invalidateQueries({ queryKey: ["resource", "widget"] })
      void navigate({ to: "/t/$slug/settings/widgets/$id", params: { slug, id: created.id } })
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
      title={t("widgets.title")}
      actions={
        <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
          {t("widgets.new")}
        </Button>
      }
    >
      <DataTable
        records={list.data?.items ?? []}
        loading={list.isPending}
        empty={t("widgets.empty")}
        columns={[
          {
            accessor: "name",
            title: t("widget_studio.name"),
            render: (row) => (
              <AnchorLink to="/t/$slug/settings/widgets/$id" params={{ slug, id: row.id }} size="sm" fw={500}>
                {row.name}
              </AnchorLink>
            ),
          },
          {
            accessor: "team_name",
            title: t("common.team_inbox"),
            render: (row) => row.team_name || <Text c="dimmed" size="sm">{t("widgets.team_unassigned")}</Text>,
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
                editLabel={t("widgets.configure")}
                onEdit={() => void navigate({ to: "/t/$slug/settings/widgets/$id", params: { slug, id: row.id } })}
              />
            ),
          },
        ]}
      />

      <Modal opened={creating} onClose={() => setCreating(false)} title={t("widgets.create_title")}>
        <form onSubmit={form.onSubmit((v) => create.mutate(v))}>
          <Stack gap="md">
            <FormError error={createError} />
            <TextInput
              label={t("widget_studio.name")}
              description={t("widget_studio.name_hint")}
              required
              maxLength={60}
              data-autofocus
              {...form.getInputProps("name")}
            />
            <ResourceSelect
              model={teams}
              label={t("widgets.team_hint")}
              placeholder={t("widgets.team_unassigned")}
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
                {t("widgets.create_submit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Page>
  )
}
