import type { ContactKind } from "@agents-desk/client/tenant"
import { NavLink, Text } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { useMatchRoute, useNavigate, useParams, useSearch } from "@tanstack/react-router"
import { IconUserCheck, IconUserQuestion, IconUsers } from "@tabler/icons-react"
import { Fill, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import type { ContactsSearch } from "./search"

/**
 * ContactsNav 是放进二级导航（nav 插槽）的联系人分类列表：全部、用户、线索，每一项带数量。
 * 无论是列表页还是联系人资料页都显示，点击切回对应分类的列表并重置页码。
 */
export function ContactsNav(): ReactNode {
  const t = useT()
  const { slug } = useParams({ from: "/t/$slug" })
  const navigate = useNavigate()
  const matchRoute = useMatchRoute()
  const isList = Boolean(matchRoute({ to: "/t/$slug/contacts", params: { slug } }))
  const search = useSearch({ strict: false }) as Partial<ContactsSearch>
  const summary = useQuery(kit.queries.contact.summary())
  const d = summary.data

  const go = (kind?: ContactKind) => {
    void navigate({
      to: "/t/$slug/contacts",
      params: { slug },
      search: {
        kind,
        q: search.q,
        language: search.language,
        channel: search.channel,
        sort: search.sort === "created" ? "created" : "recent",
        page: 1,
      },
    })
  }

  const count = (n?: number) =>
    n === undefined ? null : (
      <Text size="xs" c="dimmed">
        {n}
      </Text>
    )

  return (
    <Fill name="nav">
      <NavLink
        label={t("contacts.nav_all")}
        leftSection={<IconUsers size={16} />}
        rightSection={count(d?.all)}
        active={isList && !search.kind}
        onClick={() => go(undefined)}
      />
      <NavLink
        label={t("contacts.nav_users")}
        leftSection={<IconUserCheck size={16} />}
        rightSection={count(d?.users)}
        active={isList && search.kind === "user"}
        onClick={() => go("user")}
      />
      <NavLink
        label={t("contacts.nav_leads")}
        leftSection={<IconUserQuestion size={16} />}
        rightSection={count(d?.leads)}
        active={isList && search.kind === "lead"}
        onClick={() => go("lead")}
      />
    </Fill>
  )
}
