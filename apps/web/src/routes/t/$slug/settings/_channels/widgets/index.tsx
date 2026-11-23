import { Badge, Button, ColorSwatch, Group, Modal, Paper, Skeleton, Stack, Table, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { applyServerErrors, FormError, Page, primaryColors, ResourceSelect, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../../api"
import { teams } from "../../../../../../resources"

// 网页聊天插件列表：浏览工作区中的聊天插件，新建后直接进入实时预览配置工作台。
export const Route = createFileRoute("/t/$slug/settings/_channels/widgets/")({
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
      {list.isPending ? (
        <Stack gap="sm">
          <Skeleton height={48} radius="md" />
          <Skeleton height={48} radius="md" />
        </Stack>
      ) : !list.data?.items.length ? (
        <Paper withBorder p="xl" radius="md">
          <Stack align="center" gap="sm">
            <Text c="dimmed" size="sm">
              {t("widgets.empty")}
            </Text>
            <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
              {t("widgets.new")}
            </Button>
          </Stack>
        </Paper>
      ) : (
        <Paper withBorder radius="md" style={{ overflow: "hidden" }}>
          <Table highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("widget_studio.name")}</Table.Th>
                <Table.Th>{t("widgets.team_hint")}</Table.Th>
                <Table.Th>{t("widgets.enabled")}</Table.Th>
                <Table.Th w={100} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {list.data.items.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td>
                    <Group gap="xs" wrap="nowrap">
                      <ColorSwatch color={row.appearance.color || primaryColors["bright-blue"]} size={14} />
                      <Text size="sm" fw={500}>
                        {row.name}
                      </Text>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    {row.team_name ? (
                      <Text size="sm">{row.team_name}</Text>
                    ) : (
                      <Text c="dimmed" size="sm">
                        {t("widgets.team_unassigned")}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Badge variant="dot" color={row.enabled ? "green" : "gray"}>
                      {t(row.enabled ? "widgets.enabled" : "widgets.disabled")}
                    </Badge>
                  </Table.Td>
                  <Table.Td ta="right">
                    <Button
                      size="xs"
                      variant="subtle"
                      renderRoot={(props) => (
                        <Link {...props} to="/t/$slug/settings/widgets/$id" params={{ slug, id: row.id }} />
                      )}
                    >
                      {t("widgets.configure")}
                    </Button>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Paper>
      )}

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
                {t("widgets.cancel")}
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
