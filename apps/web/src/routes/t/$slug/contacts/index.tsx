import type { ContactDTO, ContactSort } from "@agents-desk/client/tenant"
import { ActionIcon, Avatar, Badge, Box, Center, Group, Loader, Pagination, Select, Stack, Table, Text, TextInput } from "@mantine/core"
import { useDebouncedValue } from "@mantine/hooks"
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { IconSearch, IconX } from "@tabler/icons-react"
import { Page, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit } from "../../../../api"
import { type ContactsSearch, validateContactsSearch } from "../../../../contacts/search"
import { activity } from "../../../../inbox/format"
import { LanguageSelect } from "../../../../LanguageSelect"
import { languageName } from "../../../../languages"

/** PER_PAGE 是联系人列表每页条数。 */
const PER_PAGE = 20

// 联系人列表页：按类型（来自二级导航）、关键词、语言、渠道筛选，按最近活跃或创建时间排序；点击行进入资料页。
export const Route = createFileRoute("/t/$slug/contacts/")({
  validateSearch: validateContactsSearch,
  component: ContactListPage,
})

function ContactListPage(): ReactNode {
  const t = useT()
  const ui = useLanguage()
  const zone = useTimeZone()
  const { slug } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate()

  const [keyword, setKeyword] = useState(search.q ?? "")
  const [debounced] = useDebouncedValue(keyword, 250)

  // 地址参数外部变化（如点击浏览器返回）时同步搜索框
  useEffect(() => {
    setKeyword(search.q ?? "")
  }, [search.q])

  const updateSearch = (patch: Partial<ContactsSearch>) => {
    void navigate({
      to: "/t/$slug/contacts",
      params: { slug },
      search: {
        kind: search.kind,
        q: search.q,
        language: search.language,
        channel: search.channel,
        sort: search.sort,
        page: 1,
        ...patch,
      },
      replace: true,
    })
  }

  // 防抖后的关键词写入地址参数并重置到第 1 页
  useEffect(() => {
    const trimmed = debounced.trim() || undefined
    if (trimmed !== search.q) {
      updateSearch({ q: trimmed, page: 1 })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced])

  const summary = useQuery(kit.queries.contact.summary())
  const list = useQuery(
    kit.queries.contact.list({
      kind: search.kind,
      q: search.q,
      language: search.language,
      channel_id: search.channel,
      sort: search.sort,
      page: search.page,
      per_page: PER_PAGE,
    }),
  )

  const title = search.kind === "user" ? t("contacts.nav_users") : search.kind === "lead" ? t("contacts.nav_leads") : t("nav.contacts")
  const channels = (summary.data?.channels ?? []).map((ch) => ({ value: ch.id, label: ch.name }))
  const sorts = kit.enums.ContactSort.values.map((v) => ({ value: v, label: kit.label("ContactSort", v) }))
  const items = list.data?.items ?? []
  const total = list.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PER_PAGE))

  const openContact = (c: ContactDTO) => {
    void navigate({ to: "/t/$slug/contacts/$id", params: { slug, id: c.id } })
  }

  return (
    <Page title={title}>
      <Stack gap="md">
        <Group gap="sm" wrap="wrap">
          <TextInput
            value={keyword}
            onChange={(e) => setKeyword(e.currentTarget.value)}
            placeholder={t("contacts.search_placeholder")}
            leftSection={<IconSearch size={16} />}
            rightSection={
              keyword ? (
                <ActionIcon variant="subtle" color="gray" size="sm" onClick={() => setKeyword("")} aria-label={t("common.cancel")}>
                  <IconX size={14} />
                </ActionIcon>
              ) : null
            }
            style={{ flex: 1, minWidth: 220 }}
          />
          <LanguageSelect
            w={180}
            clearable
            placeholder={t("contacts.filter_language")}
            value={search.language ?? null}
            onChange={(v) => updateSearch({ language: v || undefined, page: 1 })}
            aria-label={t("contacts.language")}
          />
          {channels.length > 0 && (
            <Select
              w={180}
              clearable
              placeholder={t("contacts.filter_channel")}
              data={channels}
              value={search.channel ?? null}
              onChange={(v) => updateSearch({ channel: v || undefined, page: 1 })}
              aria-label={t("contacts.filter_channel")}
            />
          )}
          <Select
            w={150}
            allowDeselect={false}
            data={sorts}
            value={search.sort}
            onChange={(v) => v && updateSearch({ sort: v as ContactSort, page: 1 })}
            aria-label={t("contacts.sort")}
          />
        </Group>

        {list.isPending ? (
          <Center py="xl">
            <Loader size="sm" />
          </Center>
        ) : items.length === 0 ? (
          <Text c="dimmed" ta="center" py="xl">
            {t("contacts.empty")}
          </Text>
        ) : (
          <Box style={{ overflowX: "auto" }}>
            <Table highlightOnHover verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("contacts.col_contact")}</Table.Th>
                  <Table.Th w={90}>{t("contacts.col_kind")}</Table.Th>
                  <Table.Th>{t("contacts.col_email")}</Table.Th>
                  <Table.Th w={140}>{t("contacts.col_language")}</Table.Th>
                  <Table.Th w={80}>{t("contacts.col_conversations")}</Table.Th>
                  <Table.Th w={130}>{t("contacts.col_last_seen")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {items.map((c) => {
                  const displayName = c.name || t("inbox.visitor")
                  return (
                    <Table.Tr key={c.id} onClick={() => openContact(c)} style={{ cursor: "pointer" }}>
                      <Table.Td>
                        <Group gap="sm" wrap="nowrap">
                          <Avatar size={32} name={displayName} color="initials" />
                          <Box style={{ minWidth: 0 }}>
                            <Text size="sm" fw={500} truncate>
                              {displayName}
                            </Text>
                            {c.external_user_id && (
                              <Text size="xs" c="dimmed" truncate>
                                {c.external_user_id}
                              </Text>
                            )}
                          </Box>
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        <Badge size="sm" variant="light" color={c.kind === "user" ? "bright-blue" : "gray"}>
                          {kit.label("ContactKind", c.kind)}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" c={c.email ? undefined : "dimmed"} truncate>
                          {c.email || "—"}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" c={c.language ? undefined : "dimmed"}>
                          {c.language ? languageName(c.language, ui) : "—"}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{c.conversation_count}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" c="dimmed">
                          {activity(c.last_seen_at ?? c.created_at, ui, t, zone)}
                        </Text>
                      </Table.Td>
                    </Table.Tr>
                  )
                })}
              </Table.Tbody>
            </Table>
          </Box>
        )}

        {total > 0 && (
          <Group justify="space-between" align="center">
            <Text size="xs" c="dimmed">
              {t("contacts.total", { n: total })}
            </Text>
            {totalPages > 1 && (
              <Pagination
                size="sm"
                total={totalPages}
                value={search.page}
                onChange={(p) => updateSearch({ page: p })}
              />
            )}
          </Group>
        )}
      </Stack>
    </Page>
  )
}
