import type { ConversationSummary, ConversationViewSummary } from "@agents-desk/client/tenant"
import { ActionIcon, Menu, NavLink, Text, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useNavigate, useParams } from "@tanstack/react-router"
import {
  IconAt,
  IconDots,
  IconFilter,
  IconInbox,
  IconListDetails,
  IconPencil,
  IconPlus,
  IconTicket,
  IconTrash,
  IconUserQuestion,
  IconUsers,
} from "@tabler/icons-react"
import { confirm, Fill, notifyError, notifySuccess, RailSection, useCan, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { CreateTicketModal } from "../tickets/CreateTicketModal"
import type { TicketBox } from "../tickets/search"
import { type Thresholds, useThresholds } from "./data"
import { waitColor, waitLevel } from "./format"
import type { InboxSearch } from "./search"
import { ViewModal } from "./ViewModal"

/** useNow 返回现在的时间，每 30 秒更新一次：等待时长随之变化。 */
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  return now
}

/** Count 是收件箱列表中一项的数量：等得最久的会话超过时限时，数字用等待时长的颜色。 */
function Count({ s, now, th }: { s?: ConversationSummary; now: number; th?: Thresholds }): ReactNode {
  if (!s) return null
  const color = s.oldest_waiting_since ? waitColor[waitLevel(s.oldest_waiting_since, now, th)] : undefined
  return (
    <Text size="xs" c={color ?? "dimmed"} fw={color ? 600 : undefined} style={{ fontVariantNumeric: "tabular-nums" }}>
      {s.count}
    </Text>
  )
}

/** PlainCount 是纯数字计数（工单未解决数）。 */
function PlainCount({ n }: { n?: number }): ReactNode {
  if (n === undefined) return null
  return (
    <Text size="xs" c="dimmed" style={{ fontVariantNumeric: "tabular-nums" }}>
      {n}
    </Text>
  )
}

/**
 * InboxNav 是放进二级导航（nav 插槽）的统一收件箱导航：
 * 包含会话列表（我的、@我、未指派、全部）、团队收件箱、已保存视图，以及工单分组（我的、未指派、全部）。
 * 在收件箱路由和工单路由中共用，点击任意分组平滑切换。
 */
export function InboxNav({
  search,
  go,
  activeTicketBox,
  onSelectTicketBox,
  onCreateTicket,
}: {
  search?: InboxSearch
  go?: (s: Partial<InboxSearch>) => void
  activeTicketBox?: TicketBox
  onSelectTicketBox?: (box: TicketBox) => void
  onCreateTicket?: () => void
}): ReactNode {
  const t = useT()
  const can = useCan()
  const now = useNow()
  const th = useThresholds()
  const { slug } = useParams({ from: "/t/$slug" })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const inbox = useQuery(kit.queries.conversation.inbox())
  const d = inbox.data

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ConversationViewSummary | undefined>()
  const [createTicketOpen, setCreateTicketOpen] = useState(false)

  const navigateInbox = (patch: Partial<InboxSearch>) => {
    if (go) {
      go(patch)
      return
    }
    void navigate({
      to: "/t/$slug/inbox",
      params: { slug },
      search: {
        box: patch.box ?? "mine",
        team: patch.team,
        view: patch.view,
        filter: patch.filter,
        tag: patch.tag,
        status: patch.status ?? "open",
        sort: patch.sort ?? "waiting",
      },
    })
  }

  const navigateTicket = (box: TicketBox) => {
    if (onSelectTicketBox) {
      onSelectTicketBox(box)
      return
    }
    void navigate({
      to: "/t/$slug/tickets",
      params: { slug },
      search: { box, status: "unresolved" },
    })
  }

  const remove = useMutation({
    mutationFn: (id: string) => tenantApi.view.delete(id),
    onSuccess: (_res, id) => {
      notifySuccess(t("views.deleted"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.view.key() })
      if (search?.view === id) {
        navigateInbox({ view: undefined, filter: undefined, box: "mine", team: undefined })
      }
    },
    onError: (err) => notifyError(err),
  })

  const handleDelete = async (v: ConversationViewSummary) => {
    const ok = await confirm({
      title: t("views.delete_confirm", { name: v.name }),
      message: t(v.scope === "shared" ? "views.delete_message_shared" : "views.delete_message_personal"),
      danger: true,
      confirmLabel: t("views.delete"),
    })
    if (ok) remove.mutate(v.id)
  }

  const openCreateView = () => {
    setEditing(undefined)
    setModalOpen(true)
  }

  const openEditView = (v: ConversationViewSummary) => {
    setEditing(v)
    setModalOpen(true)
  }

  const item = (box: InboxSearch["box"], label: string, icon: ReactNode, s?: ConversationSummary) => (
    <NavLink
      key={box}
      label={label}
      leftSection={icon}
      rightSection={<Count s={s} now={now} th={th} />}
      active={Boolean(search && !activeTicketBox && search.box === box && !search.team && !search.view)}
      onClick={() => navigateInbox({ box, team: undefined, view: undefined, filter: undefined })}
    />
  )

  const ticketItem = (box: TicketBox, label: string, icon: ReactNode, count?: number) => (
    <NavLink
      key={`ticket:${box}`}
      label={label}
      leftSection={icon}
      rightSection={<PlainCount n={count} />}
      active={activeTicketBox === box}
      onClick={() => navigateTicket(box)}
    />
  )

  const views = d?.views ?? []

  return (
    <>
      <Fill name="nav">
        {item("mine", t("inbox.box_mine"), <IconInbox size={16} stroke={1.6} />, d?.mine)}
        {item("mentions", t("inbox.box_mentions"), <IconAt size={16} stroke={1.6} />, d?.mentions)}
        {item("unassigned", t("inbox.box_unassigned"), <IconUserQuestion size={16} stroke={1.6} />, d?.unassigned)}
        {item("all", t("inbox.box_all"), <IconListDetails size={16} stroke={1.6} />, d?.all)}

        <RailSection
          title={t("tickets.nav_section")}
          collapsible
          storeKey="inbox-tickets"
          action={
            can("conversations:reply") ? (
              <Tooltip label={t("tickets.create")} openDelay={300}>
                <ActionIcon
                  size="xs"
                  variant="subtle"
                  color="gray"
                  aria-label={t("tickets.create")}
                  onClick={() => (onCreateTicket ? onCreateTicket() : setCreateTicketOpen(true))}
                >
                  <IconPlus size={14} stroke={1.8} />
                </ActionIcon>
              </Tooltip>
            ) : undefined
          }
        >
          {ticketItem("mine", t("tickets.box_mine"), <IconTicket size={15} stroke={1.6} />, d?.tickets.mine)}
          {ticketItem("unassigned", t("tickets.box_unassigned"), <IconUserQuestion size={15} stroke={1.6} />, d?.tickets.unassigned)}
          {ticketItem("all", t("tickets.box_all"), <IconListDetails size={15} stroke={1.6} />, d?.tickets.all)}
        </RailSection>

        {(d?.teams.length ?? 0) > 0 && (
          <RailSection title={t("inbox.teams")} collapsible storeKey="inbox-teams">
            {d?.teams.map((team) => {
              const active = Boolean(search && !activeTicketBox && search.team === team.id && !search.view)
              return (
                <NavLink
                  key={team.id}
                  label={team.name}
                  leftSection={
                    <span className="inbox-team-badge" data-active={active || undefined}>
                      {team.name.slice(0, 1)}
                    </span>
                  }
                  rightSection={<Count s={team} now={now} th={th} />}
                  active={active}
                  onClick={() => navigateInbox({ box: "all", team: team.id, view: undefined, filter: undefined })}
                />
              )
            })}
          </RailSection>
        )}

        <RailSection
          title={t("views.section")}
          collapsible
          storeKey="inbox-views"
          action={
            <Tooltip label={t("views.create")} openDelay={300}>
              <ActionIcon size="xs" variant="subtle" color="gray" aria-label={t("views.create")} onClick={openCreateView}>
                <IconPlus size={14} stroke={1.8} />
              </ActionIcon>
            </Tooltip>
          }
        >
          {views.length === 0 ? (
            <Text size="xs" c="dimmed" px={10} py={4}>
              {t("views.empty")}
            </Text>
          ) : (
            views.map((v) => {
              const active = Boolean(search && !activeTicketBox && search.view === v.id)
              return (
                <div key={v.id} className="inbox-view-row" data-editable={v.editable || undefined}>
                  <NavLink
                    label={v.name}
                    leftSection={
                      v.scope === "shared" ? <IconUsers size={15} stroke={1.6} /> : <IconFilter size={15} stroke={1.6} />
                    }
                    rightSection={
                      <span className="inbox-view-count">
                        <Count s={v} now={now} th={th} />
                      </span>
                    }
                    active={active}
                    onClick={() =>
                      navigateInbox({
                        box: "all",
                        team: undefined,
                        view: v.id,
                        filter: undefined,
                        tag: undefined,
                        sort: v.config.sort ?? "waiting",
                      })
                    }
                  />
                  {v.editable && (
                    <div className="inbox-view-actions">
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
                          <Menu.Item leftSection={<IconPencil size={13} />} onClick={() => openEditView(v)}>
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

      <ViewModal
        opened={modalOpen}
        onClose={() => setModalOpen(false)}
        editing={editing}
        onSaved={(saved) =>
          navigateInbox({
            box: "all",
            team: undefined,
            view: saved.id,
            filter: undefined,
            tag: undefined,
            sort: saved.config.sort ?? "waiting",
          })
        }
      />

      {!onCreateTicket && (
        <CreateTicketModal
          opened={createTicketOpen}
          onClose={() => setCreateTicketOpen(false)}
          onCreated={(created) =>
            void navigate({
              to: "/t/$slug/tickets/$id",
              params: { slug, id: created.id },
              search: { box: "all", status: "unresolved" },
            })
          }
        />
      )}
    </>
  )
}
