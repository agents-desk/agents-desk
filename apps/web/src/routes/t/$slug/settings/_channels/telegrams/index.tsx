import { Badge, Button, Group, Modal, Paper, PasswordInput, SegmentedControl, Skeleton, Stack, Table, Text, TextInput } from "@mantine/core"
import { useForm } from "@mantine/form"
import { IconPlus } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { applyServerErrors, FormError, Page, ResourceSelect, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { tenantApi } from "../../../../../../api"
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
      {list.isPending ? (
        <Stack gap="sm">
          <Skeleton height={48} radius="md" />
          <Skeleton height={48} radius="md" />
        </Stack>
      ) : !list.data?.items.length ? (
        <Paper withBorder p="xl" radius="md">
          <Stack align="center" gap="sm">
            <Text c="dimmed" size="sm">
              {t("telegrams.empty")}
            </Text>
            <Button leftSection={<IconPlus size={16} />} onClick={openCreate}>
              {t("telegrams.new")}
            </Button>
          </Stack>
        </Paper>
      ) : (
        <Paper withBorder radius="md" style={{ overflow: "hidden" }}>
          <Table highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("telegrams.name")}</Table.Th>
                <Table.Th>{t("telegrams.bot_username")}</Table.Th>
                <Table.Th>{t("telegrams.team_hint")}</Table.Th>
                <Table.Th>{t("telegrams.mode")}</Table.Th>
                <Table.Th>{t("telegrams.enabled")}</Table.Th>
                <Table.Th w={100} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {list.data.items.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Td>
                    <Text size="sm" fw={500}>
                      {row.name}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    {row.bot_username ? (
                      <Text size="sm">@{row.bot_username}</Text>
                    ) : (
                      <Text c="dimmed" size="sm">
                        —
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    {row.team_name ? (
                      <Text size="sm">{row.team_name}</Text>
                    ) : (
                      <Text c="dimmed" size="sm">
                        {t("telegrams.team_unassigned")}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    <Badge variant="light" color={row.gateway_managed ? "bright-blue" : "gray"}>
                      {t(row.gateway_managed ? "telegrams.mode_gateway" : "telegrams.mode_direct")}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <Badge variant="dot" color={row.enabled ? "green" : "gray"}>
                      {t(row.enabled ? "telegrams.enabled" : "telegrams.disabled")}
                    </Badge>
                  </Table.Td>
                  <Table.Td ta="right">
                    <Button
                      size="xs"
                      variant="subtle"
                      renderRoot={(props) => (
                        <Link {...props} to="/t/$slug/settings/telegrams/$id" params={{ slug, id: row.id }} />
                      )}
                    >
                      {t("telegrams.configure")}
                    </Button>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Paper>
      )}

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
                {t("telegrams.cancel")}
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
