import type { ContactKind, ContactViewSummary } from "@agents-desk/client/tenant"
import { ActionIcon, Menu, NavLink, Text, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  IconDots,
  IconFilter,
  IconPencil,
  IconPlus,
  IconTrash,
  IconUserCheck,
  IconUserQuestion,
  IconUsers,
} from "@tabler/icons-react"
import { confirm, Fill, notifyError, notifySuccess, RailSection, useT } from "@go-tenon/ui"
import { type ReactNode, useState } from "react"
import { kit, tenantApi } from "../api"
import type { ContactsSearch } from "./search"
import { ContactViewModal } from "./ViewModal"

/**
 * ContactsNav 是放进二级导航（nav 插槽）的联系人分类和保存筛选列表：全部、用户、线索以及共享/个人保存的筛选，每一项带实时数量。
 */
export function ContactsNav({ search, go }: { search: ContactsSearch; go: (patch: Partial<ContactsSearch>) => void }): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const summary = useQuery(kit.queries.contact.summary())
  const d = summary.data
  const views = d?.views ?? []

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ContactViewSummary | undefined>()

  const remove = useMutation({
    mutationFn: (id: string) => tenantApi.contact.viewDelete(id),
    onSuccess: (_res, id) => {
      notifySuccess(t("contacts.view_deleted"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      if (search.view === id) {
        go({ view: undefined, kind: undefined, q: undefined, language: undefined, channel: undefined, tag: undefined })
      }
    },
    onError: (err) => notifyError(err),
  })

  const handleDelete = async (v: ContactViewSummary) => {
    const ok = await confirm({
      title: t("contacts.view_delete_confirm", { name: v.name }),
      message: t(v.scope === "shared" ? "views.delete_message_shared" : "views.delete_message_personal"),
      danger: true,
      confirmLabel: t("views.delete"),
    })
    if (ok) remove.mutate(v.id)
  }

  const pick = (kind?: ContactKind) => go({ view: undefined, kind })
  const count = (n?: number) =>
    n === undefined ? null : (
      <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
        {n}
      </Text>
    )

  return (
    <>
      <Fill name="nav">
        <NavLink
          label={t("contacts.nav_all")}
          leftSection={<IconUsers size={16} />}
          rightSection={count(d?.all)}
          active={!search.view && !search.kind}
          onClick={() => pick(undefined)}
        />
        <NavLink
          label={t("contacts.nav_users")}
          leftSection={<IconUserCheck size={16} />}
          rightSection={count(d?.users)}
          active={!search.view && search.kind === "user"}
          onClick={() => pick("user")}
        />
        <NavLink
          label={t("contacts.nav_leads")}
          leftSection={<IconUserQuestion size={16} />}
          rightSection={count(d?.leads)}
          active={!search.view && search.kind === "lead"}
          onClick={() => pick("lead")}
        />

        <RailSection
          title={t("contacts.view_section")}
          collapsible
          storeKey="contacts-views"
          action={
            <Tooltip label={t("contacts.view_create")} openDelay={300}>
              <ActionIcon
                size="xs"
                variant="subtle"
                color="gray"
                aria-label={t("contacts.view_create")}
                onClick={() => {
                  setEditing(undefined)
                  setModalOpen(true)
                }}
              >
                <IconPlus size={14} stroke={1.8} />
              </ActionIcon>
            </Tooltip>
          }
        >
          {views.length === 0 ? (
            <Text size="xs" c="dimmed" px={10} py={4}>
              {t("contacts.view_empty")}
            </Text>
          ) : (
            views.map((v) => {
              const active = search.view === v.id
              return (
                <div key={v.id} className="ct-view-row" data-editable={v.editable || undefined}>
                  <NavLink
                    label={v.name}
                    leftSection={v.scope === "shared" ? <IconUsers size={15} stroke={1.6} /> : <IconFilter size={15} stroke={1.6} />}
                    rightSection={<span className="ct-view-count">{count(v.count)}</span>}
                    active={active}
                    onClick={() =>
                      go({
                        view: v.id,
                        kind: undefined,
                        q: undefined,
                        language: undefined,
                        channel: undefined,
                        tag: undefined,
                        sort: v.config.sort ?? "recent",
                      })
                    }
                  />
                  {v.editable && (
                    <div className="ct-view-actions">
                      <Menu position="bottom-end" withinPortal>
                        <Menu.Target>
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="gray"
                            aria-label={t("views.more_actions")}
                            onClick={(e) => e.stopPropagation()}
                          >
                            <IconDots size={14} />
                          </ActionIcon>
                        </Menu.Target>
                        <Menu.Dropdown>
                          <Menu.Item
                            leftSection={<IconPencil size={13} />}
                            onClick={() => {
                              setEditing(v)
                              setModalOpen(true)
                            }}
                          >
                            {t("views.edit")}
                          </Menu.Item>
                          <Menu.Item leftSection={<IconTrash size={13} />} color="red" onClick={() => void handleDelete(v)}>
                            {t("views.delete")}
                          </Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
                    </div>
                  )}
                </div>
              )
            })
          )}
        </RailSection>
      </Fill>

      <ContactViewModal
        opened={modalOpen}
        onClose={() => setModalOpen(false)}
        editing={editing}
        onSaved={(saved) =>
          go({
            view: saved.id,
            kind: undefined,
            q: undefined,
            language: undefined,
            channel: undefined,
            tag: undefined,
            sort: saved.config.sort ?? "recent",
          })
        }
      />
    </>
  )
}
