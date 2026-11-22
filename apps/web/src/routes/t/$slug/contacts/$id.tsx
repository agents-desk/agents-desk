import type { ContactDetailDTO } from "@agents-desk/client/tenant"
import {
  Anchor,
  Avatar,
  Badge,
  Box,
  Button,
  Card,
  Center,
  Code,
  Grid,
  Group,
  Loader,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
  UnstyledButton,
} from "@mantine/core"
import { useForm } from "@mantine/form"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { IconArrowLeft, IconExternalLink } from "@tabler/icons-react"
import { applyServerErrors, FormError, notifySuccess, Page, useCan, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useMemo, useState } from "react"
import { kit, tenantApi } from "../../../../api"
import { activity } from "../../../../inbox/format"
import { LanguageSelect } from "../../../../LanguageSelect"
import { languageName } from "../../../../languages"
import { allZones, formatLocalTime, offsetOf } from "../../../../timezone"

/** AUTO 是语言选择器中的“自动识别”。 */
const AUTO = "auto"

// 联系人资料页：左侧是可编辑的基本资料与备注以及渠道身份列表，右侧是该联系人的全部历史会话（点击直达收件箱）。
export const Route = createFileRoute("/t/$slug/contacts/$id")({
  component: ContactDetailPage,
})

function ContactDetailPage(): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const { slug, id } = Route.useParams()
  const navigate = useNavigate()

  const detail = useQuery(kit.queries.contact.get(id))
  const conversations = useQuery({
    queryKey: [...kit.queries.conversation.key(), "contact-detail", id],
    queryFn: () => tenantApi.conversation.list({ contact_id: id, per_page: 50, sort: "recent" }),
  })

  const backSearch = { sort: "recent" as const, page: 1 }

  if (detail.isPending) {
    return (
      <Page title={t("nav.contacts")}>
        <Center py="xl">
          <Loader size="sm" />
        </Center>
      </Page>
    )
  }
  if (!detail.data) {
    return (
      <Page title={t("nav.contacts")}>
        <Stack gap="md">
          <Link to="/t/$slug/contacts" params={{ slug }} search={backSearch} style={{ textDecoration: "none" }}>
            <Group gap={4}>
              <IconArrowLeft size={14} />
              <Text size="sm" c="dimmed">
                {t("contacts.back")}
              </Text>
            </Group>
          </Link>
          <Text c="dimmed">{t("contacts.not_found")}</Text>
        </Stack>
      </Page>
    )
  }

  const c = detail.data
  const displayName = c.name || t("inbox.visitor")
  const localTime = formatLocalTime(c.time_zone)

  return (
    <Page title={displayName}>
      <Stack gap="lg">
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <Stack gap="xs">
            <Link to="/t/$slug/contacts" params={{ slug }} search={backSearch} style={{ textDecoration: "none", width: "fit-content" }}>
              <Group gap={4}>
                <IconArrowLeft size={14} />
                <Text size="sm" c="dimmed">
                  {t("contacts.back")}
                </Text>
              </Group>
            </Link>
            <Group gap="md" wrap="nowrap">
              <Avatar size={48} name={displayName} color="initials" />
              <Stack gap={2}>
                <Group gap="xs">
                  <Text size="lg" fw={600}>
                    {displayName}
                  </Text>
                  <Badge variant="light" color={c.kind === "user" ? "bright-blue" : "gray"}>
                    {kit.label("ContactKind", c.kind)}
                  </Badge>
                </Group>
                <Group gap="xs">
                  {c.external_user_id && (
                    <Text size="xs" c="dimmed">
                      {t("contacts.external_user_id")}: {c.external_user_id}
                    </Text>
                  )}
                  {c.location && (
                    <Text size="xs" c="dimmed">
                      {c.location}
                      {localTime ? ` · ${t("contacts.local_time", { time: localTime })}` : ""}
                    </Text>
                  )}
                  {!c.location && localTime && (
                    <Text size="xs" c="dimmed">
                      {t("contacts.local_time", { time: localTime })}
                    </Text>
                  )}
                  <Text size="xs" c="dimmed">
                    {t("contacts.created_at")}: {activity(c.created_at, ui, t, zone)}
                  </Text>
                </Group>
              </Stack>
            </Group>
          </Stack>
        </Group>

        <Grid gap="lg">
          <Grid.Col span={{ base: 12, md: 5 }}>
            <Stack gap="md">
              <Card withBorder padding="md">
                <Text fw={600} size="sm" mb="md">
                  {t("contacts.profile")}
                </Text>
                <ProfileForm key={`${c.id}-${c.updated_at}`} contact={c} />
              </Card>

              <Card withBorder padding="md">
                <Text fw={600} size="sm" mb="sm">
                  {t("contacts.channel_identities")}
                </Text>
                {c.identities.length === 0 ? (
                  <Text size="sm" c="dimmed">
                    {t("contacts.identities_empty")}
                  </Text>
                ) : (
                  <Stack gap="xs">
                    {c.identities.map((ident) => (
                      <Group key={`${ident.channel.id}:${ident.external_id}`} justify="space-between" wrap="nowrap">
                        <Box style={{ minWidth: 0 }}>
                          <Group gap={6} wrap="nowrap">
                            <Text size="sm" fw={500} truncate>
                              {ident.channel.name}
                            </Text>
                            <Badge size="xs" variant="light" color="gray">
                              {kit.label("ChannelKind", ident.channel.kind)}
                            </Badge>
                          </Group>
                          <Code mt={2}>{ident.external_id}</Code>
                        </Box>
                        <Text size="xs" c="dimmed" style={{ flex: "none" }}>
                          {activity(ident.created_at, ui, t, zone)}
                        </Text>
                      </Group>
                    ))}
                  </Stack>
                )}
              </Card>
            </Stack>
          </Grid.Col>

          <Grid.Col span={{ base: 12, md: 7 }}>
            <Card withBorder padding="md">
              <Group justify="space-between" mb="sm">
                <Text fw={600} size="sm">
                  {t("contacts.conversations")}
                </Text>
                <Badge variant="light" color="gray" size="sm">
                  {c.conversation_count}
                </Badge>
              </Group>
              {conversations.isPending ? (
                <Center py="lg">
                  <Loader size="sm" />
                </Center>
              ) : (conversations.data?.items.length ?? 0) === 0 ? (
                <Text size="sm" c="dimmed" py="md">
                  {t("contacts.no_conversations")}
                </Text>
              ) : (
                <Stack gap="xs">
                  {conversations.data?.items.map((conv) => (
                    <UnstyledButton
                      key={conv.id}
                      onClick={() =>
                        void navigate({
                          to: "/t/$slug/inbox/$id",
                          params: { slug, id: conv.id },
                          search: { box: "all", status: conv.status, sort: "waiting" },
                        })
                      }
                      p="sm"
                      style={{
                        borderRadius: "var(--mantine-radius-sm)",
                        border: "1px solid var(--mantine-color-default-border)",
                      }}
                    >
                      <Group justify="space-between" wrap="nowrap" mb={4}>
                        <Group gap="xs" wrap="nowrap">
                          <Badge
                            size="xs"
                            variant="light"
                            color={conv.status === "open" ? "bright-blue" : conv.status === "snoozed" ? "yellow" : "gray"}
                          >
                            {t(`inbox.status_${conv.status}` as const)}
                          </Badge>
                          {conv.channel && (
                            <Text size="xs" c="dimmed">
                              {conv.channel.name}
                            </Text>
                          )}
                          {conv.rating && (
                            <Text size="xs" c="yellow">
                              {"★".repeat(conv.rating.score)}
                            </Text>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed" style={{ flex: "none" }}>
                          {activity(conv.last_message_at, ui, t, zone)}
                        </Text>
                      </Group>
                      <Text size="sm" truncate c={conv.preview ? undefined : "dimmed"}>
                        {conv.preview || "—"}
                      </Text>
                    </UnstyledButton>
                  ))}
                </Stack>
              )}
            </Card>
          </Grid.Col>
        </Grid>
      </Stack>
    </Page>
  )
}

/** ProfileForm 是联系人资料页的基本信息与备注表单。 */
function ProfileForm({ contact: c }: { contact: ContactDetailDTO }): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const can = useCan()
  const editable = can("contacts:update")
  const queryClient = useQueryClient()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<ReturnType<typeof applyServerErrors>>()

  const form = useForm({
    initialValues: {
      name: c.name,
      email: c.email,
      language: c.language_manual ? c.language : AUTO,
      location: c.location,
      time_zone: c.time_zone,
      notes: c.notes,
    },
  })

  const zones = useMemo(() => allZones().map((z) => ({ value: z, label: `${z} (${offsetOf(z)})` })), [])
  const detected = c.language ? languageName(c.language, ui) : ""
  const liveTime = formatLocalTime(form.values.time_zone)

  const submit = form.onSubmit(async (values) => {
    setSaving(true)
    setError(undefined)
    try {
      await tenantApi.contact.update(c.id, {
        name: values.name,
        email: values.email,
        language: values.language === AUTO ? "" : values.language,
        location: values.location,
        time_zone: values.time_zone,
        notes: values.notes,
      })
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() }),
        queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
      ])
      form.resetDirty()
      notifySuccess(t("common.saved"))
    } catch (err) {
      setError(applyServerErrors(form, err))
    } finally {
      setSaving(false)
    }
  })

  return (
    <form noValidate onSubmit={submit}>
      <Stack gap="sm">
        <FormError error={error} />
        <TextInput
          label={t("contacts.name")}
          placeholder={t("contacts.name_placeholder")}
          maxLength={120}
          disabled={!editable}
          {...form.getInputProps("name")}
        />
        <TextInput
          label={t("contacts.email")}
          placeholder={t("contacts.email_placeholder")}
          maxLength={254}
          disabled={!editable}
          {...form.getInputProps("email")}
        />
        <Box>
          <LanguageSelect
            label={t("contacts.language")}
            before={[
              {
                value: AUTO,
                label: !c.language_manual && detected ? t("inbox.language_auto_detected", { lang: detected }) : t("inbox.language_auto"),
              },
            ]}
            first={c.language ? [c.language] : []}
            allowDeselect={false}
            disabled={!editable}
            {...form.getInputProps("language")}
          />
          {form.values.language !== AUTO && (
            <Text size="xs" c="dimmed" mt={4}>
              {t("inbox.language_manual_hint")}
            </Text>
          )}
        </Box>
        <TextInput
          label={t("contacts.location")}
          placeholder={t("contacts.location_placeholder")}
          maxLength={120}
          disabled={!editable}
          {...form.getInputProps("location")}
        />
        <Select
          label={t("contacts.time_zone")}
          placeholder={t("contacts.time_zone_placeholder")}
          description={liveTime ? t("contacts.local_time", { time: liveTime }) : undefined}
          data={zones}
          searchable
          clearable
          nothingFoundMessage={t("preferences.no_time_zone")}
          disabled={!editable}
          value={form.values.time_zone || null}
          onChange={(v) => form.setFieldValue("time_zone", v ?? "")}
          error={form.errors.time_zone}
        />
        {c.referrer && (
          <Stack gap={2}>
            <Text size="sm" fw={500}>
              {t("contacts.referrer")}
            </Text>
            <Anchor href={c.referrer} target="_blank" rel="noopener noreferrer" size="sm" style={{ wordBreak: "break-all" }}>
              <Group gap={4} wrap="nowrap" align="center">
                <Text span size="sm" truncate>
                  {c.referrer}
                </Text>
                <IconExternalLink size={13} style={{ flex: "none" }} />
              </Group>
            </Anchor>
          </Stack>
        )}
        <Textarea
          label={t("contacts.notes")}
          placeholder={t("contacts.notes_placeholder")}
          autosize
          minRows={3}
          maxLength={4000}
          disabled={!editable}
          {...form.getInputProps("notes")}
        />
        {editable && (
          <Group justify="flex-end" mt="xs">
            <Button type="submit" loading={saving} disabled={!form.isDirty()}>
              {t("common.save")}
            </Button>
          </Group>
        )}
      </Stack>
    </form>
  )
}
