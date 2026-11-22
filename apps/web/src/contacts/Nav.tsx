import type { ContactKind } from "@agents-desk/client/tenant"
import { NavLink, Text } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { IconUserCheck, IconUserQuestion, IconUsers } from "@tabler/icons-react"
import { Fill, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { kit } from "../api"
import type { ContactsSearch } from "./search"

/**
 * ContactsNav 是放进二级导航（nav 插槽）的联系人分类列表：全部、用户、线索，每一项带数量。
 * 切换分类时保留其他筛选条件，并将页码重置到第 1 页（与 InboxNav 保持一致）。
 */
export function ContactsNav({ search, go }: { search: ContactsSearch; go: (patch: Partial<ContactsSearch>) => void }): ReactNode {
  const t = useT()
  const summary = useQuery(kit.queries.contact.summary())
  const d = summary.data

  const pick = (kind?: ContactKind) => go({ kind })
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
        active={!search.kind}
        onClick={() => pick(undefined)}
      />
      <NavLink
        label={t("contacts.nav_users")}
        leftSection={<IconUserCheck size={16} />}
        rightSection={count(d?.users)}
        active={search.kind === "user"}
        onClick={() => pick("user")}
      />
      <NavLink
        label={t("contacts.nav_leads")}
        leftSection={<IconUserQuestion size={16} />}
        rightSection={count(d?.leads)}
        active={search.kind === "lead"}
        onClick={() => pick("lead")}
      />
    </Fill>
  )
}
